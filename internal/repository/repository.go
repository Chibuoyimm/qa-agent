package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid  = errors.New("invalid repository import")
	ErrUpstream = errors.New("repository provider unavailable")
	ErrAccess   = errors.New("Azure access denied; provide a Code (Read) personal access token and check the repository URL")
	ErrTimeout  = errors.New("repository import timed out")
	ErrBusy     = errors.New("repository importer busy")
)

const (
	maxFiles         = 20
	maxBytes         = 40_000
	maxAPIRequests   = 64
	maxMetadataBytes = 8_000_000
)

type Input struct {
	Provider   string   `json:"provider,omitempty"`
	Repository string   `json:"repository"`
	Ref        string   `json:"ref"`
	Role       string   `json:"role"`
	Paths      []string `json:"paths"`
}

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type Snapshot struct {
	Provider      string `json:"provider"`
	Repository    string `json:"repository"`
	Ref           string `json:"ref"`
	Role          string `json:"role"`
	CommitSHA     string `json:"commit_sha"`
	ContentSHA256 string `json:"content_sha256"`
	Files         []File `json:"files"`
	TotalBytes    int    `json:"total_bytes"`
}

type Client struct {
	http  *http.Client
	slots chan struct{}
}

// New fixes the provider API origins. A custom transport is useful for local tests; it cannot
// change the URLs or the redirect policy constructed by Fetch.
func New(transport http.RoundTripper) *Client {
	if transport == nil {
		base := http.DefaultTransport.(*http.Transport).Clone()
		base.Proxy = nil
		transport = base
	}
	return &Client{
		http:  &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		slots: make(chan struct{}, 2),
	}
}

var (
	repositoryPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	refPart        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	shaPattern     = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
	literalSecret  = regexp.MustCompile(`(?im)(?:api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret|password|passwd|private[_-]?key)["'` + "`" + `]?\s*[=:]\s*["'` + "`" + `]([^"'` + "`" + `\r\n]{8,})["'` + "`" + `]`)
	knownSecret    = regexp.MustCompile(`(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-(?:proj-)?[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{20,}|-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----)`)
)

// EffectiveProvider preserves GitHub as the default for existing requests.
func EffectiveProvider(provider string) string {
	if provider == "" {
		return "github"
	}
	return provider
}

// Validate checks an import request and transient credential before provider access.
func Validate(in Input, token string) error {
	switch EffectiveProvider(in.Provider) {
	case "github":
		parts := strings.Split(in.Repository, "/")
		if len(parts) != 2 || len(parts[0]) > 39 || len(parts[1]) > 100 || !repositoryPart.MatchString(parts[0]) || !repositoryPart.MatchString(parts[1]) || parts[1] == "." || parts[1] == ".." {
			return fmt.Errorf("%w: repository must be owner/name", ErrInvalid)
		}
	case "azure":
		if _, err := azureBase(in.Repository); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: unsupported repository provider", ErrInvalid)
	}
	if in.Ref == "" || len(in.Ref) > 200 || !refPart.MatchString(in.Ref) || strings.Contains(in.Ref, "..") || strings.Contains(in.Ref, "//") || strings.HasSuffix(in.Ref, "/") {
		return fmt.Errorf("%w: invalid ref", ErrInvalid)
	}
	if in.Role != "frontend" && in.Role != "backend" {
		return fmt.Errorf("%w: invalid role", ErrInvalid)
	}
	if len(in.Paths) == 0 || len(in.Paths) > maxFiles {
		return fmt.Errorf("%w: choose 1–20 paths", ErrInvalid)
	}
	if len(token) > 4096 {
		return fmt.Errorf("%w: invalid token", ErrInvalid)
	}
	for _, b := range []byte(token) {
		if b < '!' || b > '~' {
			return fmt.Errorf("%w: invalid token", ErrInvalid)
		}
	}
	seen := make(map[string]bool, len(in.Paths))
	for _, p := range in.Paths {
		if p == "" || len(p) > 1024 || !utf8.ValidString(p) || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00\r\n") || seen[p] {
			return fmt.Errorf("%w: invalid or duplicate path", ErrInvalid)
		}
		seen[p] = true
		parts := strings.Split(p, "/")
		if len(parts) > 20 {
			return fmt.Errorf("%w: path is too deep", ErrInvalid)
		}
		for _, part := range parts {
			if part == "" || part == "." || part == ".." || unsafeName(part) {
				return fmt.Errorf("%w: unsafe path", ErrInvalid)
			}
		}
	}
	return nil
}

func unsafeName(name string) bool {
	n := strings.ToLower(name)
	if n == ".env" || strings.HasPrefix(n, ".env.") || strings.HasPrefix(n, ".env-") || n == ".npmrc" || n == ".pypirc" || n == "id_rsa" || n == "id_ed25519" || n == "credentials" || n == "secrets" {
		return true
	}
	for _, prefix := range []string{"credential.", "credentials.", "secret.", "secrets.", "service-account.", "service_account.", "serviceaccount.", "token.", "tokens."} {
		if strings.HasPrefix(n, prefix) && !strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, ".ts") && !strings.HasSuffix(n, ".js") && !strings.HasSuffix(n, ".py") {
			return true
		}
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".keystore", ".jks", ".env"} {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	return false
}

func (c *Client) Fetch(ctx context.Context, in Input, token string) (Snapshot, error) {
	if err := Validate(in, token); err != nil {
		return Snapshot{}, err
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return Snapshot{}, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	in.Provider = EffectiveProvider(in.Provider)
	base := "https://api.github.com/repos/" + in.Repository
	if in.Provider == "azure" {
		base, _ = azureBase(in.Repository)
	}
	budget := requestBudget{}
	commitSHA, treeSHA, err := c.resolveCommit(ctx, base, in, token, &budget)
	if err != nil {
		return Snapshot{}, err
	}

	files := make([]File, 0, len(in.Paths))
	total := 0
	trees := make(map[string]gitTree)
	for _, p := range in.Paths {
		sha := treeSHA
		parts := strings.Split(p, "/")
		for i, part := range parts {
			tree, ok := trees[sha]
			if !ok {
				var err error
				tree, err = c.fetchTree(ctx, base, in.Provider, sha, token, &budget)
				if err != nil {
					return Snapshot{}, err
				}
				if tree.Truncated || tree.SHA != sha {
					return Snapshot{}, fmt.Errorf("%w: incomplete tree", ErrUpstream)
				}
				trees[sha] = tree
			}
			var entry *treeEntry
			for j := range tree.Tree {
				if tree.Tree[j].Path == part {
					entry = &tree.Tree[j]
					break
				}
			}
			if entry == nil {
				return Snapshot{}, fmt.Errorf("%w: selected file missing", ErrInvalid)
			}
			if !shaPattern.MatchString(entry.SHA) {
				return Snapshot{}, fmt.Errorf("%w: invalid tree entry", ErrUpstream)
			}
			if i < len(parts)-1 {
				if entry.Type != "tree" || entry.Mode != "040000" && entry.Mode != "40000" {
					return Snapshot{}, fmt.Errorf("%w: selected path is not a directory", ErrInvalid)
				}
				sha = entry.SHA
				continue
			}
			if entry.Type != "blob" || (entry.Mode != "100644" && entry.Mode != "100755") {
				return Snapshot{}, fmt.Errorf("%w: selected path is not a regular file", ErrInvalid)
			}
			if entry.Size < 0 || entry.Size > maxBytes-total {
				return Snapshot{}, fmt.Errorf("%w: selected content exceeds 40,000 bytes", ErrInvalid)
			}
			content, err := c.fetchBlob(ctx, base, in.Provider, *entry, token, &budget)
			if err != nil {
				return Snapshot{}, err
			}

			if !safeText(content) {
				return Snapshot{}, fmt.Errorf("%w: selected file contains binary data or a recognizable secret", ErrInvalid)
			}
			total += len(content)
			files = append(files, File{Path: p, Content: string(content)})
		}
	}
	hash := sha256.New()
	for _, file := range files {
		hash.Write([]byte(file.Path))
		hash.Write([]byte{0})
		hash.Write([]byte(file.Content))
		hash.Write([]byte{0})
	}
	return Snapshot{Provider: in.Provider, Repository: in.Repository, Ref: in.Ref, Role: in.Role, CommitSHA: commitSHA, ContentSHA256: hex.EncodeToString(hash.Sum(nil)), Files: files, TotalBytes: total}, nil
}

type treeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	Size int    `json:"size"`
	SHA  string `json:"sha"`
}

type gitTree struct {
	SHA       string      `json:"sha"`
	Tree      []treeEntry `json:"tree"`
	Truncated bool        `json:"truncated"`
}

func safeText(content []byte) bool {
	if !utf8.Valid(content) || knownSecret.Match(content) || literalSecret.Match(content) {
		return false
	}
	for _, b := range content {
		if (b < 0x20 && b != '\n' && b != '\r' && b != '\t') || b == 0x7f {
			return false
		}
	}
	return true
}

type requestBudget struct {
	requests int
	bytes    int64
}

func (c *Client) get(ctx context.Context, endpoint, token string, limit int64, budget *requestBudget, target any) error {
	body, err := c.read(ctx, endpoint, token, "application/json", limit, budget)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("%w: invalid provider response", ErrUpstream)
	}
	return nil
}

func (c *Client) read(ctx context.Context, endpoint, token, accept string, limit int64, budget *requestBudget) ([]byte, error) {
	if budget.requests >= maxAPIRequests {
		return nil, fmt.Errorf("%w: selected paths exceed request limit", ErrInvalid)
	}
	remaining := int64(maxMetadataBytes) - budget.bytes
	if remaining <= 0 {
		return nil, fmt.Errorf("%w: provider response budget exceeded", ErrUpstream)
	}
	if limit > remaining {
		limit = remaining
	}
	budget.requests++
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid request", ErrUpstream)
	}
	if req.URL.Scheme != "https" || req.URL.User != nil || (req.URL.Host != "api.github.com" && req.URL.Host != "dev.azure.com") {
		return nil, fmt.Errorf("%w: unsupported provider origin", ErrInvalid)
	}
	req.Header.Set("Accept", accept)
	if req.URL.Host == "api.github.com" {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	} else if token != "" {
		req.SetBasicAuth("", token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ErrTimeout
		}
		return nil, ErrUpstream
	}
	defer resp.Body.Close()
	if req.URL.Host == "dev.azure.com" && (resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 203 || resp.StatusCode == 302 || resp.StatusCode == 303) {
		return nil, ErrAccess
	}
	if resp.StatusCode != http.StatusOK {
		return nil, ErrUpstream
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ErrTimeout
		}
		return nil, ErrUpstream
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: provider response too large", ErrUpstream)
	}
	budget.bytes += int64(len(body))
	return body, nil
}
