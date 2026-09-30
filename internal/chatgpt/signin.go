package chatgpt

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}

type discovery struct {
	Issuer             string `json:"issuer"`
	JWKSURI            string `json:"jwks_uri"`
	RevocationEndpoint string `json:"revocation_endpoint"`
}

func (c *Client) StartLogin(ctx context.Context, profileID string) (Login, error) {
	if err := ctx.Err(); err != nil {
		return Login{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return Login{}, ErrUnavailable
	}
	if c.pending != nil {
		return Login{}, ErrBusy
	}
	clientID := c.state.PendingClientID
	var selected *credential
	if profileID != "" {
		selected = c.profileLocked(profileID)
		if selected == nil {
			return Login{}, ErrInvalid
		}
		clientID = selected.ClientID
	}
	if clientID == "" {
		clientID = "dynamic_agent_client"
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return Login{}, ErrUnavailable
	}
	id, err := randomUUID()
	if err != nil {
		listener.Close()
		return Login{}, ErrUnavailable
	}
	state, err := randomString(32)
	if err != nil {
		listener.Close()
		return Login{}, ErrUnavailable
	}
	nonce, err := randomString(32)
	if err != nil {
		listener.Close()
		return Login{}, ErrUnavailable
	}
	verifier, err := randomString(32)
	if err != nil {
		listener.Close()
		return Login{}, ErrUnavailable
	}
	callback := "http://127.0.0.1:" + strings.TrimPrefix(listener.Addr().String(), "127.0.0.1:") + "/auth/callback"
	attemptCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(10*time.Minute))
	a := &attempt{id: id, profileID: profileID, clientID: clientID, redirectURI: callback, state: state, nonce: nonce, verifier: verifier, ctx: attemptCtx, cancel: cancel, expiresAt: time.Now().Add(10 * time.Minute)}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { c.callback(w, r, a) }), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 45 * time.Second, MaxHeaderBytes: 16 << 10}
	a.listener = server
	c.pending = a
	c.login = &LoginStatus{ID: id, Status: "pending"}
	go func() { _ = server.Serve(listener) }()
	go func() {
		<-attemptCtx.Done()
		c.mu.Lock()
		if c.pending == a {
			c.finishLocked(a, "expired", "Sign-in expired. Try again.")
		}
		c.mu.Unlock()
	}()
	q := url.Values{
		"client_id": {clientID}, "ext_agent_host_id": {c.state.HostID}, "response_type": {"code"},
		"redirect_uri": {callback}, "scope": {scope}, "resource": {resource}, "state": {state}, "nonce": {nonce},
		"code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sha256Sum(verifier))},
	}
	if selected == nil && clientID == "dynamic_agent_client" {
		q.Set("agent_name_hint", "QA Agent")
	}
	if selected != nil {
		if !hasScope(selected.Scopes, "chatgpt.tokens.use.direct") {
			q.Set("prompt", "consent")
		}
		if selected.Email != "" {
			q.Set("login_hint", selected.Email)
		}
	}
	return Login{ID: id, AuthURL: authOrigin + "/api/accounts/authorize?" + q.Encode(), ExpiresAt: a.expiresAt}, nil
}

func sha256Sum(value string) []byte { sum := sha256.Sum256([]byte(value)); return sum[:] }

func (c *Client) CancelLogin(loginID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil || c.pending.id != loginID {
		return ErrInvalid
	}
	c.finishLocked(c.pending, "cancelled", "Sign-in cancelled.")
	return nil
}

func (c *Client) finishLocked(a *attempt, status, message string) {
	a.cancel()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.listener.Shutdown(ctx)
	}()
	c.pending = nil
	c.login = &LoginStatus{ID: a.id, Status: status, Message: message}
}

func (c *Client) callback(w http.ResponseWriter, r *http.Request, a *attempt) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	callbackURL, _ := url.Parse(a.redirectURI)
	if r.Method != http.MethodGet || r.URL.Path != "/auth/callback" || r.Host != callbackURL.Host || len(r.URL.RawQuery) > 8192 {
		http.Error(w, "Invalid callback", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	if len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(a.state)) != 1 {
		http.Error(w, "Invalid callback", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	if c.pending != a || a.ctx.Err() != nil {
		c.mu.Unlock()
		http.Error(w, "Sign-in no longer active", http.StatusGone)
		return
	}
	if a.exchanging {
		c.mu.Unlock()
		http.Error(w, "Sign-in already processing", http.StatusConflict)
		return
	}
	if q.Get("error") != "" {
		c.finishLocked(a, "error", "ChatGPT sign-in was declined.")
		c.mu.Unlock()
		http.Error(w, "Sign-in was declined", http.StatusBadRequest)
		return
	}
	if len(q["code"]) != 1 || q.Get("code") == "" || len(q["client_id"]) > 1 {
		c.finishLocked(a, "error", "Invalid sign-in callback.")
		c.mu.Unlock()
		http.Error(w, "Invalid callback", http.StatusBadRequest)
		return
	}
	issued := q.Get("client_id")
	if a.clientID == "dynamic_agent_client" {
		if !strings.HasPrefix(issued, "oaiapp_") {
			c.finishLocked(a, "error", "Registration was incomplete.")
			c.mu.Unlock()
			http.Error(w, "Incomplete registration", http.StatusBadRequest)
			return
		}
		a.clientID = issued
		c.state.PendingClientID = issued
		if err := c.saveLocked(); err != nil {
			c.finishLocked(a, "error", "Could not save registration.")
			c.mu.Unlock()
			http.Error(w, "Sign-in unavailable", http.StatusServiceUnavailable)
			return
		}
	} else if issued != "" && issued != a.clientID {
		c.finishLocked(a, "error", "Account registration did not match.")
		c.mu.Unlock()
		http.Error(w, "Account mismatch", http.StatusBadRequest)
		return
	}
	a.exchanging = true
	c.mu.Unlock()

	cred, err := c.exchange(a, q.Get("code"))
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending != a {
		http.Error(w, "Sign-in no longer active", http.StatusGone)
		return
	}
	if err != nil {
		c.finishLocked(a, "error", err.Error())
		http.Error(w, "Sign-in could not be completed", http.StatusBadGateway)
		return
	}
	if a.profileID != "" {
		old := c.profileLocked(a.profileID)
		if old == nil || old.Subject != cred.Subject || old.ClientID != cred.ClientID {
			c.finishLocked(a, "error", "ChatGPT account did not match.")
			http.Error(w, "Account mismatch", http.StatusBadRequest)
			return
		}
		cred.ID = old.ID
	} else {
		id, err := randomUUID()
		if err != nil {
			c.finishLocked(a, "error", "Could not save account.")
			http.Error(w, "Sign-in unavailable", http.StatusServiceUnavailable)
			return
		}
		cred.ID = id
	}
	previous := c.state
	previous.Profiles = append([]credential(nil), c.state.Profiles...)
	if a.profileID == "" {
		c.state.Profiles = append(c.state.Profiles, cred)
		c.state.PendingClientID = ""
	} else {
		*c.profileLocked(a.profileID) = cred
	}
	c.state.ActiveProfileID = cred.ID
	if err := c.saveLocked(); err != nil {
		c.state = previous
		c.finishLocked(a, "error", "Could not save account.")
		http.Error(w, "Sign-in unavailable", http.StatusServiceUnavailable)
		return
	}
	if previous.ActiveProfileID != "" {
		c.cancelRequestsLocked(previous.ActiveProfileID)
	}
	if previous.ActiveProfileID != cred.ID {
		c.cancelRequestsLocked(cred.ID)
	}
	message := "Connected to ChatGPT."
	if !hasScope(cred.Scopes, "chatgpt.tokens.use.direct") {
		message = "ChatGPT connected. Plan usage is not enabled."
	}
	c.finishLocked(a, "completed", message)
	_, _ = io.WriteString(w, "ChatGPT connected. You can return to QA Agent.")
}

func (c *Client) exchange(a *attempt, code string) (credential, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {a.clientID}, "code": {code}, "code_verifier": {a.verifier}, "redirect_uri": {a.redirectURI}, "resource": {resource}}
	var token tokenResponse
	if err := c.postForm(a.ctx, authOrigin+"/api/accounts/oauth/token", form, &token); err != nil {
		return credential{}, err
	}
	if token.IDToken == "" {
		return credential{}, ErrUpstream
	}
	sharing := hasScope(strings.Fields(token.Scope), "chatgpt.tokens.use.direct")
	if sharing && (token.AccessToken == "" || token.RefreshToken == "" || !strings.EqualFold(token.TokenType, "Bearer") || !validExpiresIn(token.ExpiresIn)) {
		return credential{}, ErrUpstream
	}
	var d discovery
	if err := c.getJSON(a.ctx, authOrigin+"/.well-known/openid-configuration", "", &d); err != nil {
		return credential{}, err
	}
	if d.Issuer != authOrigin || !fixedAuthURL(d.JWKSURI) {
		return credential{}, ErrUpstream
	}
	verifier := oidc.NewVerifier(authOrigin, oidc.NewRemoteKeySet(oidc.ClientContext(a.ctx, c.http), d.JWKSURI), &oidc.Config{ClientID: a.clientID})
	id, err := verifier.Verify(oidc.ClientContext(a.ctx, c.http), token.IDToken)
	if err != nil {
		return credential{}, ErrUpstream
	}
	var claims struct {
		Subject string `json:"sub"`
		Email   string `json:"email"`
		Nonce   string `json:"nonce"`
	}
	if err := id.Claims(&claims); err != nil || claims.Subject == "" || claims.Nonce != a.nonce {
		return credential{}, ErrUpstream
	}
	cred := credential{Email: claims.Email, Subject: claims.Subject, ClientID: a.clientID, IDToken: token.IDToken, Scopes: strings.Fields(token.Scope)}
	if sharing {
		cred.AccessToken = token.AccessToken
		cred.RefreshToken = token.RefreshToken
		cred.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return cred, nil
}

func validExpiresIn(seconds int) bool {
	return seconds > 0 && int64(seconds) <= int64((1<<63-1)/time.Second)
}

func fixedAuthURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "auth.openai.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && strings.HasPrefix(u.Path, "/")
}

func (c *Client) postForm(ctx context.Context, target string, form url.Values, result *tokenResponse) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return ErrUpstream
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return ErrUpstream
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return ErrUsageLimit
	}
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
		var oauth struct {
			Error json.RawMessage `json:"error"`
			Code  string          `json:"code"`
		}
		if readJSON(resp.Body, &oauth) == nil {
			code := oauth.Code
			if len(oauth.Error) > 0 {
				var simple string
				if json.Unmarshal(oauth.Error, &simple) == nil {
					code = simple
				} else {
					var nested struct {
						Code string `json:"code"`
					}
					if json.Unmarshal(oauth.Error, &nested) == nil {
						code = nested.Code
					}
				}
			}
			switch code {
			case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused":
				return ErrReconnect
			}
		}
		return ErrUpstream
	}
	if resp.StatusCode != http.StatusOK {
		return ErrUpstream
	}
	if err := readJSON(resp.Body, result); err != nil {
		return ErrUpstream
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, target, bearer string, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return ErrUpstream
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ErrUpstream
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return ErrUsageLimit
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrReconnect
	}
	if resp.StatusCode != http.StatusOK {
		return ErrUpstream
	}
	if err := readJSON(resp.Body, result); err != nil {
		return ErrUpstream
	}
	return nil
}

func readJSON(body io.Reader, result interface{}) error {
	data, err := io.ReadAll(io.LimitReader(body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return ErrUpstream
	}
	if err := json.Unmarshal(data, result); err != nil {
		return ErrUpstream
	}
	return nil
}
