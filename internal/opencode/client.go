// Package opencode connects QA proposals to the OpenCode Go subscription provider.
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
)

const ProviderID = "opencode-go"

var (
	ErrInvalid     = errors.New("invalid OpenCode Go connection or model selection")
	ErrUnavailable = errors.New("OpenCode Go connection is unavailable")
	ErrBusy        = errors.New("OpenCode Go connection is busy")
	ErrAuth        = errors.New("connect your OpenCode Go subscription key")
	ErrQuota       = errors.New("OpenCode Go usage or rate limit reached; check usage in the OpenCode console")
	ErrRequest     = errors.New("OpenCode Go rejected this model or structured proposal request")
	ErrUpstream    = errors.New("OpenCode Go failed or returned an incomplete proposal")
	ErrCleanup     = errors.New("OpenCode proposal session cleanup failed")
)

type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Connected means OpenCode has a stored provider key, not that plan or quota was verified.
type Status struct {
	Enabled   bool    `json:"enabled"`
	Connected bool    `json:"connected"`
	Models    []Model `json:"models"`
}

type Client struct {
	baseURL  string
	password string
	http     *http.Client
	gate     chan struct{}
	runtime  *runtimeProcess
}

func ValidModelID(id string) bool {
	return id != "" && len(id) <= 100 && strings.IndexFunc(id, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-')
	}) == -1
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	status := Status{Enabled: true, Models: []Model{}}
	var catalog struct {
		All []struct {
			ID     string `json:"id"`
			Key    string `json:"key"`
			Models map[string]struct {
				ID           string `json:"id"`
				ProviderID   string `json:"providerID"`
				Name         string `json:"name"`
				Capabilities struct {
					Toolcall bool `json:"toolcall"`
					Input    struct {
						Text bool `json:"text"`
					} `json:"input"`
				} `json:"capabilities"`
			} `json:"models"`
		} `json:"all"`
		Connected []string `json:"connected"`
	}
	if err := c.call(ctx, http.MethodGet, "/provider", nil, &catalog); err != nil {
		return status, err
	}
	connected := false
	for _, id := range catalog.Connected {
		if id == ProviderID {
			connected = true
		}
	}
	if !connected {
		return status, nil
	}
	for _, provider := range catalog.All {
		if provider.ID != ProviderID {
			continue
		}
		if provider.Key == "" {
			status.Connected = false
			return status, nil
		}
		status.Connected = true
		for id, model := range provider.Models {
			if !ValidModelID(id) || model.ID != id || model.ProviderID != ProviderID || !model.Capabilities.Toolcall || !model.Capabilities.Input.Text {
				continue
			}
			name := model.Name
			if name == "" || len(name) > 200 {
				name = id
			}
			status.Models = append(status.Models, Model{ID: id, Name: name})
		}
	}
	sort.Slice(status.Models, func(i, j int) bool { return status.Models[i].ID < status.Models[j].ID })
	return status, nil
}

func (c *Client) acquire() error {
	select {
	case c.gate <- struct{}{}:
		return nil
	default:
		return ErrBusy
	}
}

// OpenCode owns persistence of this key in its isolated local data directory.
func (c *Client) Connect(ctx context.Context, key string) (Status, error) {
	if key == "" || len(key) > 4096 || strings.IndexFunc(key, func(r rune) bool { return r <= 32 || r >= 127 }) != -1 {
		return Status{}, ErrInvalid
	}
	if err := c.acquire(); err != nil {
		return Status{}, err
	}
	defer func() { <-c.gate }()
	var accepted bool
	if err := c.call(ctx, http.MethodPut, "/auth/"+ProviderID, struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}{"api", key}, &accepted); err != nil {
		return Status{}, err
	}
	if !accepted {
		return Status{}, ErrUpstream
	}
	if err := c.reload(ctx); err != nil {
		return Status{}, err
	}
	return c.Status(ctx)
}

func (c *Client) Disconnect(ctx context.Context) (Status, error) {
	if err := c.acquire(); err != nil {
		return Status{}, err
	}
	defer func() { <-c.gate }()
	var accepted bool
	if err := c.call(ctx, http.MethodDelete, "/auth/"+ProviderID, nil, &accepted); err != nil {
		return Status{}, err
	}
	if !accepted {
		return Status{}, ErrUpstream
	}
	if err := c.reload(ctx); err != nil {
		return Status{}, err
	}
	return c.Status(ctx)
}

// Auth persistence does not invalidate the pinned runtime's provider cache.
// Dispose only this app's isolated instance so removed keys cannot keep working.
func (c *Client) reload(ctx context.Context) error {
	var accepted bool
	if err := c.call(ctx, http.MethodPost, "/global/dispose", nil, &accepted); err != nil || !accepted {
		c.Close()
		return ErrUnavailable
	}
	return nil
}

func (c *Client) call(ctx context.Context, method, path string, body any, result any) error {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return ErrInvalid
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return ErrInvalid
	}
	req.SetBasicAuth("opencode", c.password)
	req.Header.Set("User-Agent", "qa-agent/0.1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return statusError(response.StatusCode)
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return ErrUpstream
	}
	if result != nil && json.Unmarshal(data, result) != nil {
		return ErrUpstream
	}
	return nil
}

func statusError(status int) error {
	switch status {
	case 401, 403:
		return ErrAuth
	case 429:
		return ErrQuota
	case 400, 422:
		return ErrRequest
	default:
		return ErrUpstream
	}
}
