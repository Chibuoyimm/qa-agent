package planner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/Chibuoyimm/qa-agent/internal/chatgpt"
)

func (p *Planner) subscriptionResponse(ctx context.Context, in Input) (providerResponse, error) {
	ctx, release, err := p.ChatGPT.RequestContext(ctx, in.ChatGPTProfileID)
	if err != nil {
		return providerResponse{}, err
	}
	defer release()
	models, err := p.ChatGPT.Models(ctx, in.ChatGPTProfileID)
	if err != nil {
		return providerResponse{}, err
	}
	allowed := false
	for _, model := range models {
		if model.Slug == in.Model {
			allowed = true
			break
		}
	}
	if !allowed {
		return providerResponse{}, chatgpt.ErrInvalid
	}
	key, err := p.ChatGPT.AccessToken(ctx, in.ChatGPTProfileID)
	if err != nil {
		return providerResponse{}, err
	}
	return p.streamSubscription(ctx, in, key)
}

func (p *Planner) streamSubscription(ctx context.Context, in Input, key string) (providerResponse, error) {
	request := struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
		Input        []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"input"`
		Store  bool         `json:"store"`
		Stream bool         `json:"stream"`
		Text   responseText `json:"text"`
	}{Model: in.Model, Instructions: instructions, Store: false, Stream: true}
	request.Input = append(request.Input, struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{"user", "Testing request:\n" + in.Prompt + "\n\nApplication context:\n" + in.Context})
	format := providerRequest{}
	format.Text.Format.Type = "json_schema"
	format.Text.Format.Name = "qa_scenario_proposals"
	format.Text.Format.Strict = true
	format.Text.Format.Schema = p.schema
	request.Text = format.Text
	body, err := json.Marshal(request)
	if err != nil {
		return providerResponse{}, ErrUpstream
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return providerResponse{}, ErrUpstream
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	response, err := p.client.Do(req)
	if err != nil {
		return providerResponse{}, ErrUpstream
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		return providerResponse{}, subscriptionFailure(failure.Error.Code, response.StatusCode)
	}
	if strings.Split(response.Header.Get("Content-Type"), ";")[0] != "text/event-stream" {
		return providerResponse{}, ErrUpstream
	}
	return readSubscriptionStream(response.Body)
}

func subscriptionFailure(code string, status int) error {
	switch code {
	case "subscription_sharing_usage_limit_exceeded":
		return chatgpt.ErrUsageLimit
	case "subscription_sharing_usage_unavailable", "subscription_sharing_user_unavailable":
		return ErrSubscriptionUnavailable
	case "subscription_sharing_user_not_eligible", "subscription_sharing_route_not_supported", "chatpass_v2_scope_not_authorized", "chatpass_v2_invalid_authorization_context":
		return ErrSubscriptionDenied
	case "subscription_sharing_unsupported_capability":
		return ErrSubscriptionCapability
	case "subscription_sharing_invalid_user":
		return chatgpt.ErrReconnect
	}
	switch status {
	case http.StatusUnauthorized:
		return chatgpt.ErrReconnect
	case http.StatusForbidden:
		return ErrSubscriptionDenied
	case http.StatusTooManyRequests:
		return chatgpt.ErrUsageLimit
	case http.StatusServiceUnavailable:
		return ErrSubscriptionUnavailable
	default:
		return ErrUpstream
	}
}

func readSubscriptionStream(body io.Reader) (providerResponse, error) {
	limited := &io.LimitedReader{R: body, N: (2 << 20) + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	var data strings.Builder
	eventName := ""
	consume := func() (providerResponse, bool, error) {
		if data.Len() == 0 {
			eventName = ""
			return providerResponse{}, false, nil
		}
		var event struct {
			Type  string `json:"type"`
			Code  string `json:"code"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
			Response struct {
				providerResponse
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			} `json:"response"`
		}
		if json.Unmarshal([]byte(data.String()), &event) != nil || event.Type == "" || (eventName != "" && eventName != event.Type) {
			return providerResponse{}, false, ErrUpstream
		}
		data.Reset()
		eventName = ""
		switch event.Type {
		case "response.completed":
			if event.Response.Status != "completed" {
				return providerResponse{}, false, ErrUpstream
			}
			return event.Response.providerResponse, true, nil
		case "response.failed", "response.incomplete", "error":
			code := event.Code
			if code == "" {
				code = event.Error.Code
			}
			if code == "" {
				code = event.Response.Error.Code
			}
			return providerResponse{}, false, subscriptionFailure(code, 0)
		}
		return providerResponse{}, false, nil
	}
	for scanner.Scan() {
		if limited.N <= 0 {
			return providerResponse{}, ErrUpstream
		}
		line := scanner.Text()
		if line == "" {
			provider, completed, err := consume()
			if err != nil {
				return providerResponse{}, err
			}
			if completed {
				return provider, nil
			}
		} else if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		} else if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
	}
	// A successful response requires a complete terminal event, including its frame delimiter.
	return providerResponse{}, ErrUpstream
}
