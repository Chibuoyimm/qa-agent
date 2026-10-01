package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type permissionRule struct {
	Permission string `json:"permission"`
	Pattern    string `json:"pattern"`
	Action     string `json:"action"`
}

// Draft creates one owned session. Files, shell, browsing, MCP and subagents are
// denied; only OpenCode's StructuredOutput return tool is permitted.
func (c *Client) Draft(ctx context.Context, model, instructions, prompt string, schema json.RawMessage) (output string, err error) {
	if !ValidModelID(model) || !json.Valid(schema) {
		return "", ErrInvalid
	}
	if err := c.acquire(); err != nil {
		return "", err
	}
	defer func() { <-c.gate }()
	status, err := c.Status(ctx)
	if err != nil {
		return "", err
	}
	if !status.Connected {
		return "", ErrAuth
	}
	allowed := false
	for _, item := range status.Models {
		if item.ID == model {
			allowed = true
		}
	}
	if !allowed {
		return "", ErrInvalid
	}
	var session struct {
		ID string `json:"id"`
	}
	body := struct {
		Title      string           `json:"title"`
		Permission []permissionRule `json:"permission"`
	}{"QA Agent proposal", []permissionRule{{"*", "*", "deny"}, {"StructuredOutput", "*", "allow"}}}
	if err := c.call(ctx, http.MethodPost, "/session", body, &session); err != nil {
		return "", err
	}
	if !strings.HasPrefix(session.ID, "ses") || !ValidModelID(session.ID) {
		return "", ErrUpstream
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if c.cleanupSession(cleanup, session.ID) != nil {
			output = ""
			err = ErrCleanup
		}
	}()
	request := struct {
		Model struct {
			ProviderID string `json:"providerID"`
			ModelID    string `json:"modelID"`
		} `json:"model"`
		Agent  string `json:"agent"`
		System string `json:"system"`
		Format struct {
			Type       string          `json:"type"`
			Schema     json.RawMessage `json:"schema"`
			RetryCount int             `json:"retryCount"`
		} `json:"format"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}{Agent: "qa-proposal", System: instructions}
	request.Model.ProviderID = ProviderID
	request.Model.ModelID = model
	request.Format.Type = "json_schema"
	request.Format.Schema = schema
	request.Parts = append(request.Parts, struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{"text", prompt})
	var response struct {
		Info struct {
			SessionID  string `json:"sessionID"`
			Role       string `json:"role"`
			ProviderID string `json:"providerID"`
			ModelID    string `json:"modelID"`
			Agent      string `json:"agent"`
			Finish     string `json:"finish"`
			Time       struct {
				Completed int64 `json:"completed"`
			} `json:"time"`
			Structured json.RawMessage `json:"structured"`
			Error      *struct {
				Name string `json:"name"`
				Data struct {
					StatusCode int `json:"statusCode"`
				} `json:"data"`
			} `json:"error"`
		} `json:"info"`
		Parts []struct {
			Type  string `json:"type"`
			Tool  string `json:"tool"`
			State struct {
				Status string `json:"status"`
			} `json:"state"`
		} `json:"parts"`
	}
	if err := c.prompt(ctx, session.ID, request, &response); err != nil {
		return "", err
	}
	info := response.Info
	if info.Error != nil {
		if info.Error.Name == "ProviderAuthError" {
			return "", ErrAuth
		}
		if info.Error.Name == "APIError" && info.Error.Data.StatusCode != 0 {
			return "", statusError(info.Error.Data.StatusCode)
		}
		return "", ErrUpstream
	}
	if info.SessionID != session.ID || info.Role != "assistant" || info.ProviderID != ProviderID || info.ModelID != model || info.Agent != "qa-proposal" || info.Time.Completed <= 0 || (info.Finish != "stop" && info.Finish != "tool-calls") || len(info.Structured) == 0 || string(info.Structured) == "null" {
		return "", ErrUpstream
	}
	returns := 0
	for _, part := range response.Parts {
		switch part.Type {
		case "text", "reasoning", "step-start", "step-finish":
		case "tool":
			if part.Tool != "StructuredOutput" || part.State.Status != "completed" {
				return "", ErrUpstream
			}
			returns++
		default:
			return "", ErrUpstream
		}
	}
	if returns != 1 {
		return "", ErrUpstream
	}
	return string(info.Structured), nil
}

func (c *Client) cleanupSession(ctx context.Context, id string) error {
	var aborted, deleted bool
	abortErr := c.call(ctx, http.MethodPost, "/session/"+id+"/abort", nil, &aborted)
	deleteErr := c.call(ctx, http.MethodDelete, "/session/"+id, nil, &deleted)
	if abortErr != nil || !aborted || deleteErr != nil || !deleted {
		return ErrCleanup
	}
	return nil
}

// A crash may interrupt normal cleanup. Recover only this app's owned sessions
// in its dedicated workspace before making the connection available again.
func (c *Client) recoverSessions(ctx context.Context) error {
	var sessions []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := c.call(ctx, http.MethodGet, "/session?limit=100", nil, &sessions); err != nil {
		return ErrCleanup
	}
	for _, session := range sessions {
		if session.Title != "QA Agent proposal" {
			continue
		}
		if !strings.HasPrefix(session.ID, "ses") || !ValidModelID(session.ID) {
			return ErrCleanup
		}
		if err := c.cleanupSession(ctx, session.ID); err != nil {
			return err
		}
	}
	if len(sessions) >= 100 {
		return ErrCleanup
	}
	return nil
}

// The native runtime schedules transient retries after at least two seconds.
// Observe its state while the prompt runs and abort on the first reported retry
// rather than consuming more plan usage or leaving a quota request waiting.
func (c *Client) prompt(ctx context.Context, sessionID string, request, response any) error {
	promptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	completed := make(chan error, 1)
	go func() {
		completed <- c.call(promptCtx, http.MethodPost, "/session/"+sessionID+"/message", request, response)
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	stop := func(err error) error { cancel(); <-completed; return err }
	for {
		select {
		case err := <-completed:
			return err
		case <-ctx.Done():
			return stop(ctx.Err())
		case <-ticker.C:
			select {
			case err := <-completed:
				return err
			default:
			}
			var states map[string]struct {
				Type    string `json:"type"`
				Message string `json:"message"`
				Action  *struct {
					Reason string `json:"reason"`
				} `json:"action"`
			}
			probe, probeCancel := context.WithTimeout(ctx, 500*time.Millisecond)
			err := c.call(probe, http.MethodGet, "/session/status", nil, &states)
			probeCancel()
			if ctx.Err() != nil {
				return stop(ctx.Err())
			}
			if err != nil {
				// Initial SDK loading can briefly block the native status handler.
				if errors.Is(err, context.DeadlineExceeded) {
					continue
				}
				return stop(ErrUnavailable)
			}
			state := states[sessionID]
			if state.Type != "retry" {
				continue
			}
			message := strings.ToLower(state.Message)
			if state.Action != nil && (state.Action.Reason == "account_rate_limit" || state.Action.Reason == "free_tier_limit") || strings.Contains(message, "429") || strings.Contains(message, "rate limit") || strings.Contains(message, "usage limit") {
				return stop(ErrQuota)
			}
			return stop(ErrUnavailable)
		}
	}
}
