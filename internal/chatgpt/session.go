package chatgpt

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (c *Client) AccessToken(ctx context.Context, profileID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", ErrUnavailable
	}
	p := c.profileLocked(profileID)
	if p == nil {
		return "", ErrInvalid
	}
	if c.disconnecting[profileID] {
		return "", ErrBusy
	}
	if p.RefreshToken == "" || !hasScope(p.Scopes, "chatgpt.tokens.use.direct") {
		return "", ErrReconnect
	}
	if time.Until(p.ExpiresAt) > 2*time.Minute {
		return p.AccessToken, nil
	}
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {p.ClientID}, "refresh_token": {p.RefreshToken}, "resource": {resource}}
	var token tokenResponse
	if err := c.postForm(ctx, authOrigin+"/api/accounts/oauth/token", form, &token); err != nil {
		if errors.Is(err, ErrReconnect) {
			c.cancelRequestsLocked(profileID)
			p.AccessToken = ""
			p.RefreshToken = ""
			p.IDToken = ""
			if c.state.ActiveProfileID == profileID {
				c.state.ActiveProfileID = ""
			}
			if saveErr := c.saveLocked(); saveErr != nil {
				return "", saveErr
			}
		}
		return "", err
	}
	if token.AccessToken == "" || token.RefreshToken == "" || !strings.EqualFold(token.TokenType, "Bearer") || !validExpiresIn(token.ExpiresIn) {
		return "", ErrUpstream
	}
	if token.Scope != "" && !hasScope(strings.Fields(token.Scope), "chatgpt.tokens.use.direct") {
		return "", ErrReconnect
	}
	previous := *p
	p.AccessToken = token.AccessToken
	p.RefreshToken = token.RefreshToken
	p.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	if token.Scope != "" {
		p.Scopes = strings.Fields(token.Scope)
	}
	if err := c.saveLocked(); err != nil {
		*p = previous
		p.AccessToken = ""
		p.RefreshToken = ""
		p.IDToken = ""
		if c.state.ActiveProfileID == profileID {
			c.state.ActiveProfileID = ""
		}
		c.cancelRequestsLocked(profileID)
		return "", err
	}
	return p.AccessToken, nil
}

func (c *Client) Models(ctx context.Context, profileID string) ([]Model, error) {
	ctx, release, err := c.RequestContext(ctx, profileID)
	if err != nil {
		return nil, err
	}
	defer release()
	token, err := c.AccessToken(ctx, profileID)
	if err != nil {
		return nil, err
	}
	var catalog struct {
		Models *[]struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
		} `json:"models"`
	}
	if err := c.getJSON(ctx, apiOrigin+"/v1/models", token, &catalog); err != nil {
		return nil, err
	}
	if catalog.Models == nil {
		return nil, ErrUpstream
	}
	models := make([]Model, 0, len(*catalog.Models))
	for _, m := range *catalog.Models {
		if m.Visibility == "list" && m.Slug != "" && m.DisplayName != "" {
			models = append(models, Model{Slug: m.Slug, DisplayName: m.DisplayName})
		}
	}
	return models, nil
}

func (c *Client) Disconnect(ctx context.Context, profileID string) (Status, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return Status{}, ErrUnavailable
	}
	p := c.profileLocked(profileID)
	if p == nil {
		c.mu.Unlock()
		return Status{}, ErrInvalid
	}
	if c.disconnecting[profileID] {
		c.mu.Unlock()
		return Status{}, ErrBusy
	}
	c.operations.Add(1)
	defer c.operations.Done()
	c.disconnecting[profileID] = true
	c.cancelRequestsLocked(profileID)
	if c.pending != nil && c.pending.profileID == profileID {
		c.finishLocked(c.pending, "cancelled", "Sign-in cancelled.")
	}
	refresh := p.RefreshToken
	clientID := p.ClientID
	c.mu.Unlock()

	confirmed := refresh == ""
	if refresh != "" {
		var d discovery
		if err := c.getJSON(ctx, authOrigin+"/.well-known/openid-configuration", "", &d); err == nil && d.Issuer == authOrigin && fixedAuthURL(d.RevocationEndpoint) {
			form := url.Values{"token": {refresh}, "token_type_hint": {"refresh_token"}, "client_id": {clientID}}
		retry:
			for i := 0; i < 3; i++ {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.RevocationEndpoint, strings.NewReader(form.Encode()))
				if err != nil {
					break
				}
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				resp, err := c.http.Do(req)
				if err == nil {
					_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
					_ = resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						confirmed = true
						break
					}
					if resp.StatusCode < 500 {
						break
					}
				}
				if i < 2 {
					select {
					case <-ctx.Done():
						break retry
					case <-time.After(time.Duration(i+1) * 200 * time.Millisecond):
					}
				}
			}
		}
	}
	c.mu.Lock()
	delete(c.disconnecting, profileID)
	if p = c.profileLocked(profileID); p == nil {
		c.mu.Unlock()
		return Status{}, ErrInvalid
	}
	p.AccessToken = ""
	p.RefreshToken = ""
	p.IDToken = ""
	p.ExpiresAt = time.Time{}
	p.Scopes = nil
	if c.state.ActiveProfileID == profileID {
		c.state.ActiveProfileID = ""
	}
	if !confirmed {
		c.message = "Remote ChatGPT sign-out could not be confirmed. Disconnect this app in ChatGPT Settings."
	} else {
		c.message = ""
	}
	if err := c.saveLocked(); err != nil {
		c.message = "Local sign-out could not be saved. Disconnect this app in ChatGPT Settings."
		c.mu.Unlock()
		return Status{}, err
	}
	c.mu.Unlock()
	return c.Status(), nil
}
