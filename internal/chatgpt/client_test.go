package chatgpt

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type authTransport struct {
	key             *rsa.PrivateKey
	mu              sync.Mutex
	nonce           string
	refreshes       int
	refreshCode     string
	tokenCalls      int
	subject         string
	issuer          string
	audience        string
	nonceOverride   string
	expired         bool
	noSharing       bool
	badSignature    bool
	exchangeEntered chan struct{}
	releaseExchange chan struct{}
	modelsEntered   chan struct{}
}

func newAuthTransport(t *testing.T) *authTransport {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &authTransport{key: key, subject: "subject-1"}
}

func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func (f *authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Scheme != "https" || (r.URL.Host != "auth.openai.com" && r.URL.Host != "api.openai.com") {
		return nil, errors.New("unexpected target")
	}
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		return response(200, `{"issuer":"https://auth.openai.com","jwks_uri":"https://auth.openai.com/jwks","revocation_endpoint":"https://auth.openai.com/revoke"}`), nil
	case "/jwks":
		n := base64.RawURLEncoding.EncodeToString(f.key.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.E)).Bytes())
		return response(200, fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"key1","alg":"RS256","use":"sig","n":"%s","e":"%s"}]}`, n, e)), nil
	case "/api/accounts/oauth/token":
		_ = r.ParseForm()
		if r.Form.Get("grant_type") == "refresh_token" {
			f.refreshes++
			if f.refreshCode != "" {
				return response(400, fmt.Sprintf(`{"error":%q}`, f.refreshCode)), nil
			}
			return response(200, `{"access_token":"new-access","refresh_token":"new-refresh","token_type":"Bearer","expires_in":3600}`), nil
		}
		f.tokenCalls++
		if f.exchangeEntered != nil {
			close(f.exchangeEntered)
			<-f.releaseExchange
		}
		issuer := f.issuer
		if issuer == "" {
			issuer = authOrigin
		}
		audience := f.audience
		if audience == "" {
			audience = r.Form.Get("client_id")
		}
		nonce := f.nonce
		if f.nonceOverride != "" {
			nonce = f.nonceOverride
		}
		expires := time.Now().Add(time.Hour)
		if f.expired {
			expires = time.Now().Add(-time.Hour)
		}
		claims := fmt.Sprintf(`{"iss":%q,"aud":%q,"sub":%q,"email":"user@example.com","nonce":%q,"exp":%d}`, issuer, audience, f.subject, nonce, expires.Unix())
		jwt := f.signedJWT(claims)
		if f.badSignature {
			jwt += "corrupt"
		}
		scopes := scope
		if f.noSharing {
			scopes = "openid profile email offline_access resource.invoke"
		}
		body, _ := json.Marshal(tokenResponse{AccessToken: "access", RefreshToken: "refresh", IDToken: jwt, TokenType: "Bearer", ExpiresIn: 3600, Scope: scopes})
		return response(200, string(body)), nil
	case "/v1/models":
		if f.modelsEntered != nil {
			close(f.modelsEntered)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		if r.Header.Get("Authorization") != "Bearer access" && r.Header.Get("Authorization") != "Bearer new-access" {
			return response(401, ""), nil
		}
		return response(200, `{"models":[{"slug":"gpt-6.1-sol","display_name":"GPT-6.1 Sol","visibility":"list"},{"slug":"hidden","display_name":"Hidden","visibility":"hide"}]}`), nil
	case "/revoke":
		return response(200, ""), nil
	}
	return response(404, ""), nil
}

func (f *authTransport) signedJWT(claims string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"key1"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(claims))
	unsigned := header + "." + payload
	digest := crypto.SHA256.New()
	_, _ = digest.Write([]byte(unsigned))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest.Sum(nil))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func completeLogin(t *testing.T, c *Client, f *authTransport, profileID string) Login {
	t.Helper()
	login, err := c.StartLogin(context.Background(), profileID)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(login.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.nonce = u.Query().Get("nonce")
	f.mu.Unlock()
	callback := u.Query().Get("redirect_uri") + "?code=code&state=" + url.QueryEscape(u.Query().Get("state"))
	if profileID == "" {
		callback += fmt.Sprintf("&client_id=oaiapp_test%d", f.tokenCalls+1)
	}
	resp, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("callback %d: %s", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(body), "ChatGPT connected") {
		t.Fatalf("callback body: %q %v", body, err)
	}
	return login
}

func TestDuplicateCallbackCannotExchangeTwice(t *testing.T) {
	f := newAuthTransport(t)
	f.exchangeEntered = make(chan struct{})
	f.releaseExchange = make(chan struct{})
	c, err := New(t.TempDir(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	login, err := c.StartLogin(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(login.AuthURL)
	f.nonce = u.Query().Get("nonce")
	callback := u.Query().Get("redirect_uri") + "?code=x&state=" + u.Query().Get("state") + "&client_id=oaiapp_test"
	firstDone := make(chan error, 1)
	go func() {
		resp, err := http.Get(callback)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != 200 {
				err = fmt.Errorf("first callback %d", resp.StatusCode)
			}
		}
		firstDone <- err
	}()
	<-f.exchangeEntered
	resp, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate callback: %d", resp.StatusCode)
	}
	close(f.releaseExchange)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if f.tokenCalls != 1 {
		t.Fatalf("token exchanges: %d", f.tokenCalls)
	}
}

func TestRejectsInvalidIdentityClaims(t *testing.T) {
	for _, name := range []string{"issuer", "audience", "expired", "nonce", "subject"} {
		t.Run(name, func(t *testing.T) {
			f := newAuthTransport(t)
			switch name {
			case "issuer":
				f.issuer = "https://other.example"
			case "audience":
				f.audience = "other-client"
			case "expired":
				f.expired = true
			case "nonce":
				f.nonceOverride = "wrong"
			case "subject":
				f.subject = ""
			}
			c, err := New(t.TempDir(), f)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			login, err := c.StartLogin(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(login.AuthURL)
			f.nonce = u.Query().Get("nonce")
			resp, err := http.Get(u.Query().Get("redirect_uri") + "?code=x&state=" + u.Query().Get("state") + "&client_id=oaiapp_test")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode == 200 || len(c.Status().Profiles) != 0 {
				t.Fatal("invalid identity accepted")
			}
		})
	}
}

func TestConcurrentRefreshUsesOneRotatingToken(t *testing.T) {
	f := newAuthTransport(t)
	c, err := New(t.TempDir(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	completeLogin(t, c, f, "")
	id := c.Status().ActiveProfileID
	c.mu.Lock()
	c.state.Profiles[0].ExpiresAt = time.Now().Add(-time.Minute)
	c.mu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := c.AccessToken(context.Background(), id)
			if err != nil || token != "new-access" {
				t.Errorf("refresh: %q %v", token, err)
			}
		}()
	}
	wg.Wait()
	f.mu.Lock()
	count := f.refreshes
	f.mu.Unlock()
	if count != 1 {
		t.Fatalf("refresh calls: %d", count)
	}
}

func TestRefreshErrorsPreserveOrClearCredentials(t *testing.T) {
	for _, tc := range []struct {
		code      string
		want      error
		connected bool
	}{{"invalid_client", ErrUpstream, true}, {"invalid_grant", ErrReconnect, false}, {"refresh_token_reused", ErrReconnect, false}} {
		t.Run(tc.code, func(t *testing.T) {
			f := newAuthTransport(t)
			c, err := New(t.TempDir(), f)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			completeLogin(t, c, f, "")
			id := c.Status().ActiveProfileID
			c.mu.Lock()
			c.state.Profiles[0].ExpiresAt = time.Now().Add(-time.Minute)
			c.mu.Unlock()
			f.mu.Lock()
			f.refreshCode = tc.code
			f.mu.Unlock()
			if _, err := c.AccessToken(context.Background(), id); !errors.Is(err, tc.want) {
				t.Fatalf("refresh error: %v", err)
			}
			if c.Status().Profiles[0].Connected != tc.connected {
				t.Fatalf("credential retained: %+v", c.Status())
			}
		})
	}
}

func TestSameEmailSeparateProfilesAndSwitchCancels(t *testing.T) {
	f := newAuthTransport(t)
	c, err := New(t.TempDir(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	completeLogin(t, c, f, "")
	first := c.Status().ActiveProfileID
	ctx, release, err := c.RequestContext(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	completeLogin(t, c, f, "")
	second := c.Status().ActiveProfileID
	if first == second || len(c.Status().Profiles) != 2 || c.Status().Profiles[0].Label == c.Status().Profiles[1].Label {
		t.Fatal("profiles were merged")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("old profile work still active")
	}
	if _, _, err := c.RequestContext(context.Background(), first); !errors.Is(err, ErrInvalid) {
		t.Fatalf("inactive request: %v", err)
	}
	if err := c.Select(first); err != nil {
		t.Fatal(err)
	}
	if c.Status().ActiveProfileID != first {
		t.Fatal("selection failed")
	}
}

func TestModelsCancelledOnDisconnect(t *testing.T) {
	f := newAuthTransport(t)
	c, err := New(t.TempDir(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	completeLogin(t, c, f, "")
	id := c.Status().ActiveProfileID
	f.modelsEntered = make(chan struct{})
	done := make(chan error, 1)
	go func() { _, err := c.Models(context.Background(), id); done <- err }()
	<-f.modelsEntered
	if _, err := c.Disconnect(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("catalog completed after disconnect")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("catalog was not cancelled")
	}
}

func TestSelectWriteFailureKeepsActiveAccount(t *testing.T) {
	dir := t.TempDir()
	f := newAuthTransport(t)
	c, err := New(dir, f)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	completeLogin(t, c, f, "")
	first := c.Status().ActiveProfileID
	completeLogin(t, c, f, "")
	second := c.Status().ActiveProfileID
	ctx, release, err := c.RequestContext(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	path := filepath.Join(dir, "credentials.json")
	if err := os.Rename(path, filepath.Join(dir, "credentials.backup")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := c.Select(first); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("selection write: %v", err)
	}
	if c.Status().ActiveProfileID != second {
		t.Fatal("active account switched despite failed save")
	}
	select {
	case <-ctx.Done():
		t.Fatal("active request cancelled despite failed save")
	default:
	}
}

func TestProtectedLockAndRepositorySymlink(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir, newAuthTransport(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir, newAuthTransport(t)); !errors.Is(err, ErrBusy) {
		t.Fatalf("duplicate runtime: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "credentials.lock"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir, newAuthTransport(t)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("loose lock permissions: %v", err)
	}
	repoDir, err := os.MkdirTemp(".", "chatgpt-storage-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(repoDir)
	repoDirAbs, err := filepath.Abs(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked-storage")
	if err := os.Symlink(repoDirAbs, link); err != nil {
		t.Fatal(err)
	}
	if _, err := New(link, newAuthTransport(t)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("repository symlink: %v", err)
	}
}

func TestRefreshWriteFailureFailsClosed(t *testing.T) {
	dir := t.TempDir()
	f := newAuthTransport(t)
	c, err := New(dir, f)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	completeLogin(t, c, f, "")
	id := c.Status().ActiveProfileID
	c.mu.Lock()
	c.state.Profiles[0].ExpiresAt = time.Now().Add(-time.Minute)
	c.mu.Unlock()
	path := filepath.Join(dir, "credentials.json")
	backup := filepath.Join(dir, "credentials.backup")
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AccessToken(context.Background(), id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("refresh write: %v", err)
	}
	if _, err := c.AccessToken(context.Background(), id); !errors.Is(err, ErrReconnect) {
		t.Fatalf("reused old refresh: %v", err)
	}
	f.mu.Lock()
	count := f.refreshes
	f.mu.Unlock()
	if count != 1 {
		t.Fatalf("refresh calls after failed write: %d", count)
	}
}

func TestRegistrationStoreFailureStopsCodeExchange(t *testing.T) {
	dir := t.TempDir()
	f := newAuthTransport(t)
	c, err := New(dir, f)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	login, err := c.StartLogin(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(login.AuthURL)
	path := filepath.Join(dir, "credentials.json")
	if err := os.Rename(path, filepath.Join(dir, "credentials.backup")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(u.Query().Get("redirect_uri") + "?code=x&state=" + u.Query().Get("state") + "&client_id=oaiapp_test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 200 || f.tokenCalls != 0 || len(c.Status().Profiles) != 0 {
		t.Fatal("exchange continued after failed registration save")
	}
}

func TestCancelAndReturningIdentityMismatch(t *testing.T) {
	f := newAuthTransport(t)
	c, err := New(t.TempDir(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	login, err := c.StartLogin(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CancelLogin(login.ID); err != nil {
		t.Fatal(err)
	}
	if c.Status().Login.Status != "cancelled" {
		t.Fatal("cancel status")
	}
	if err := c.CancelLogin(login.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("second cancel: %v", err)
	}
	completeLogin(t, c, f, "")
	id := c.Status().ActiveProfileID
	f.mu.Lock()
	f.subject = "other-subject"
	f.mu.Unlock()
	login, err = c.StartLogin(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(login.AuthURL)
	f.mu.Lock()
	f.nonce = u.Query().Get("nonce")
	f.mu.Unlock()
	if u.Query().Get("client_id") == "dynamic_agent_client" || u.Query().Get("agent_name_hint") != "" {
		t.Fatal("returning registration not reused")
	}
	resp, err := http.Get(u.Query().Get("redirect_uri") + "?code=x&state=" + u.Query().Get("state"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 200 || c.Status().ActiveProfileID != id || !c.Status().Profiles[0].Connected {
		t.Fatal("returning identity replaced account")
	}
}

func TestSignInPersistenceModelsRefreshAndDisconnect(t *testing.T) {
	dir := t.TempDir()
	f := newAuthTransport(t)
	c, err := New(dir, f)
	if err != nil {
		t.Fatal(err)
	}
	login := completeLogin(t, c, f, "")
	s := c.Status()
	if s.Login == nil || s.Login.ID != login.ID || s.Login.Status != "completed" || len(s.Profiles) != 1 || !s.Profiles[0].Sharing || s.ActiveProfileID == "" {
		t.Fatalf("status: %+v", s)
	}
	id := s.ActiveProfileID
	models, err := c.Models(context.Background(), id)
	if err != nil || len(models) != 1 || models[0].Slug != "gpt-6.1-sol" {
		t.Fatalf("models: %+v, %v", models, err)
	}
	info, err := os.Stat(filepath.Join(dir, "credentials.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("credential permissions: %v, %v", info, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c, err = New(dir, f)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Status().ActiveProfileID != id {
		t.Fatal("profile did not persist")
	}
	c.mu.Lock()
	c.state.Profiles[0].ExpiresAt = time.Now().Add(-time.Minute)
	c.mu.Unlock()
	token, err := c.AccessToken(context.Background(), id)
	if err != nil || token != "new-access" || f.refreshes != 1 {
		t.Fatalf("refresh: %q %v %d", token, err, f.refreshes)
	}
	reqCtx, release, err := c.RequestContext(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	result, err := c.Disconnect(context.Background(), id)
	if err != nil || result.Profiles[0].Connected || result.ActiveProfileID != "" {
		t.Fatalf("disconnect: %+v %v", result, err)
	}
	select {
	case <-reqCtx.Done():
	default:
		t.Fatal("request not cancelled")
	}
}

func TestCallbackRejectsWrongStateAndClient(t *testing.T) {
	f := newAuthTransport(t)
	c, err := New(t.TempDir(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	login, err := c.StartLogin(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(login.AuthURL)
	callback := u.Query().Get("redirect_uri")
	resp, err := http.Get(callback + "?code=x&state=wrong&client_id=oaiapp_test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 || c.Status().Login.Status != "pending" {
		t.Fatal("wrong state accepted")
	}
	resp, err = http.Get(callback + "?code=x&state=" + u.Query().Get("state") + "&client_id=wrong")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 || c.Status().Login.Status != "error" {
		t.Fatal("wrong client accepted")
	}
}

func TestMissingSharingAndBadSignature(t *testing.T) {
	for _, name := range []string{"sharing", "signature"} {
		t.Run(name, func(t *testing.T) {
			f := newAuthTransport(t)
			f.noSharing = name == "sharing"
			f.badSignature = name == "signature"
			c, err := New(t.TempDir(), f)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			login, err := c.StartLogin(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(login.AuthURL)
			f.nonce = u.Query().Get("nonce")
			resp, err := http.Get(u.Query().Get("redirect_uri") + "?code=x&state=" + u.Query().Get("state") + "&client_id=oaiapp_test")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if name == "signature" {
				if resp.StatusCode == 200 || len(c.Status().Profiles) != 0 {
					t.Fatal("invalid signature accepted")
				}
				return
			}
			s := c.Status()
			if resp.StatusCode != 200 || len(s.Profiles) != 1 || !s.Profiles[0].Connected || s.Profiles[0].Sharing {
				t.Fatalf("identity without sharing: %+v, %d", s, resp.StatusCode)
			}
			if _, err := c.Models(context.Background(), s.ActiveProfileID); !errors.Is(err, ErrReconnect) {
				t.Fatalf("model access without sharing: %v", err)
			}
			login, err = c.StartLogin(context.Background(), s.ActiveProfileID)
			if err != nil {
				t.Fatal(err)
			}
			returning, _ := url.Parse(login.AuthURL)
			if returning.Query().Get("prompt") != "consent" {
				t.Fatal("consent not requested for missing plan permission")
			}
		})
	}
}
