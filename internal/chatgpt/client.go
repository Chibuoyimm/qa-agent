package chatgpt

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	authOrigin = "https://auth.openai.com"
	apiOrigin  = "https://api.openai.com"
	resource   = apiOrigin + "/v1"
	scope      = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
)

var (
	ErrInvalid     = errors.New("invalid ChatGPT sign-in request")
	ErrUnavailable = errors.New("ChatGPT sign-in is unavailable")
	ErrBusy        = errors.New("ChatGPT sign-in is already in progress")
	ErrUpstream    = errors.New("ChatGPT service request failed")
	ErrReconnect   = errors.New("Reconnect your ChatGPT account")
	ErrUsageLimit  = errors.New("ChatGPT plan usage limit reached")
)

type Profile struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Label     string `json:"label"`
	Connected bool   `json:"connected"`
	Sharing   bool   `json:"sharing"`
}

type LoginStatus struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type Status struct {
	Enabled         bool         `json:"enabled"`
	ActiveProfileID string       `json:"active_profile_id,omitempty"`
	Profiles        []Profile    `json:"profiles"`
	Login           *LoginStatus `json:"login,omitempty"`
	Message         string       `json:"message,omitempty"`
}

type Login struct {
	ID        string    `json:"id"`
	AuthURL   string    `json:"auth_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Model struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

type credential struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Subject      string    `json:"subject"`
	ClientID     string    `json:"client_id"`
	IDToken      string    `json:"id_token,omitempty"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

type savedState struct {
	HostID          string       `json:"host_id"`
	ActiveProfileID string       `json:"active_profile_id,omitempty"`
	PendingClientID string       `json:"pending_client_id,omitempty"`
	Profiles        []credential `json:"profiles"`
}

type attempt struct {
	id, profileID, clientID, redirectURI, state, nonce, verifier string
	listener                                                     *http.Server
	ctx                                                          context.Context
	cancel                                                       context.CancelFunc
	expiresAt                                                    time.Time
	exchanging                                                   bool
}

type Client struct {
	mu            sync.Mutex
	operations    sync.WaitGroup
	state         savedState
	path          string
	lock          *os.File
	http          *http.Client
	pending       *attempt
	login         *LoginStatus
	message       string
	requests      map[string]map[*request]struct{}
	disconnecting map[string]bool
	closed        bool
}

type request struct{ cancel context.CancelFunc }

// New holds an exclusive file lock for its lifetime so rotating refresh tokens
// cannot be used by two processes at once.
func New(storageDir string, transport http.RoundTripper) (*Client, error) {
	if !filepath.IsAbs(storageDir) {
		return nil, ErrInvalid
	}
	if err := rejectRepositoryPath(filepath.Clean(storageDir)); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(storageDir, 0700); err != nil {
		return nil, ErrUnavailable
	}
	resolved, err := filepath.EvalSymlinks(storageDir)
	if err != nil {
		return nil, ErrUnavailable
	}
	if err := rejectRepositoryPath(resolved); err != nil {
		return nil, err
	}
	storageDir = resolved
	if err := os.Chmod(storageDir, 0700); err != nil {
		return nil, ErrUnavailable
	}
	info, err := os.Lstat(storageDir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, ErrUnavailable
	}
	lockFD, err := syscall.Open(filepath.Join(storageDir, "credentials.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrUnavailable
	}
	lock := os.NewFile(uintptr(lockFD), "credentials.lock")
	lockInfo, err := lock.Stat()
	if err != nil || !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm()&0077 != 0 {
		lock.Close()
		return nil, ErrUnavailable
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, ErrBusy
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	c := &Client{
		path: filepath.Join(storageDir, "credentials.json"), lock: lock,
		http: &http.Client{Timeout: 15 * time.Second, Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		requests:      make(map[string]map[*request]struct{}),
		disconnecting: make(map[string]bool),
	}
	if err := c.load(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func rejectRepositoryPath(path string) error {
	wd, err := os.Getwd()
	if err != nil {
		return ErrUnavailable
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			rel, err := filepath.Rel(dir, path)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return ErrInvalid
			}
			break
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return nil
}

func (c *Client) load() error {
	fd, err := syscall.Open(c.path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		id, err := randomUUID()
		if err != nil {
			return ErrUnavailable
		}
		c.state.HostID = "urn:uuid:" + id
		return c.saveLocked()
	}
	if err != nil {
		return ErrUnavailable
	}
	f := os.NewFile(uintptr(fd), c.path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return ErrUnavailable
	}
	if info.Size() > 1<<20 {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return ErrUnavailable
	}
	if err := json.Unmarshal(data, &c.state); err != nil || !strings.HasPrefix(c.state.HostID, "urn:uuid:") {
		return ErrUnavailable
	}
	if !validUUID(strings.TrimPrefix(c.state.HostID, "urn:uuid:")) {
		return ErrUnavailable
	}
	seen := make(map[string]bool, len(c.state.Profiles))
	activeFound := c.state.ActiveProfileID == ""
	for _, p := range c.state.Profiles {
		if !validUUID(p.ID) || p.Subject == "" || !strings.HasPrefix(p.ClientID, "oaiapp_") || seen[p.ID] {
			return ErrUnavailable
		}
		seen[p.ID] = true
		if p.ID == c.state.ActiveProfileID {
			activeFound = true
		}
	}
	if !activeFound || (c.state.PendingClientID != "" && !strings.HasPrefix(c.state.PendingClientID, "oaiapp_")) {
		return ErrUnavailable
	}
	return nil
}

func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, b := range []byte(s) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if b != '-' {
				return false
			}
			continue
		}
		if (b < '0' || b > '9') && (b < 'a' || b > 'f') {
			return false
		}
	}
	return true
}

func (c *Client) saveLocked() error {
	data, err := json.Marshal(c.state)
	if err != nil {
		return ErrUnavailable
	}
	f, err := os.CreateTemp(filepath.Dir(c.path), ".credentials-*")
	if err != nil {
		return ErrUnavailable
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return ErrUnavailable
	}
	if err := os.Rename(f.Name(), c.path); err != nil {
		return ErrUnavailable
	}
	dir, err := os.Open(filepath.Dir(c.path))
	if err != nil {
		return ErrUnavailable
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	if c.pending != nil {
		c.pending.cancel()
		_ = c.pending.listener.Close()
		c.pending = nil
	}
	for _, set := range c.requests {
		for r := range set {
			r.cancel()
		}
	}
	c.requests = nil
	lock := c.lock
	c.mu.Unlock()
	c.operations.Wait()
	if lock == nil {
		return nil
	}
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return lock.Close()
}

func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Status{Enabled: true, ActiveProfileID: c.state.ActiveProfileID, Profiles: make([]Profile, 0, len(c.state.Profiles)), Message: c.message}
	for _, p := range c.state.Profiles {
		label := p.Email
		if len(p.ID) >= 8 {
			label = fmt.Sprintf("%s (%s)", p.Email, p.ID[:8])
		}
		s.Profiles = append(s.Profiles, Profile{ID: p.ID, Email: p.Email, Label: label, Connected: p.IDToken != "", Sharing: hasScope(p.Scopes, "chatgpt.tokens.use.direct") && p.RefreshToken != ""})
	}
	if c.login != nil {
		copy := *c.login
		s.Login = &copy
	}
	return s
}

func (c *Client) Select(profileID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrUnavailable
	}
	p := c.profileLocked(profileID)
	if p == nil {
		return ErrInvalid
	}
	if c.disconnecting[profileID] {
		return ErrBusy
	}
	if p.IDToken == "" {
		return ErrReconnect
	}
	previous := c.state.ActiveProfileID
	c.state.ActiveProfileID = profileID
	if err := c.saveLocked(); err != nil {
		c.state.ActiveProfileID = previous
		return err
	}
	if previous != "" && previous != profileID {
		c.cancelRequestsLocked(previous)
	}
	return nil
}

func (c *Client) RequestContext(ctx context.Context, profileID string) (context.Context, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, nil, ErrUnavailable
	}
	p := c.profileLocked(profileID)
	if p == nil {
		return nil, nil, ErrInvalid
	}
	if c.state.ActiveProfileID != profileID {
		return nil, nil, ErrInvalid
	}
	if c.disconnecting[profileID] {
		return nil, nil, ErrBusy
	}
	if p.RefreshToken == "" || !hasScope(p.Scopes, "chatgpt.tokens.use.direct") {
		return nil, nil, ErrReconnect
	}
	requestCtx, cancel := context.WithCancel(ctx)
	if c.requests[profileID] == nil {
		c.requests[profileID] = make(map[*request]struct{})
	}
	r := &request{cancel: cancel}
	c.requests[profileID][r] = struct{}{}
	return requestCtx, func() { c.mu.Lock(); delete(c.requests[profileID], r); c.mu.Unlock(); cancel() }, nil
}

func (c *Client) cancelRequestsLocked(profileID string) {
	for r := range c.requests[profileID] {
		r.cancel()
	}
	delete(c.requests, profileID)
}

func (c *Client) profileLocked(id string) *credential {
	for i := range c.state.Profiles {
		if c.state.Profiles[i].ID == id {
			return &c.state.Profiles[i]
		}
	}
	return nil
}

func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return url.QueryEscape(fmt.Sprintf("%x", b)), nil
}

func randomUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
