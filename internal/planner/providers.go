package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

type openAIProvider struct{ planner *Planner }
type anthropicProvider struct{ planner *Planner }
type geminiProvider struct{ planner *Planner }

func (a openAIProvider) draft(ctx context.Context, in Input, key, workspace string, spec draftSpec) (string, error) {
	request := providerRequest{Model: in.Model, Instructions: spec.instructions,
		Input: "Testing request:\n" + in.Prompt + "\n\nApplication context:\n" + in.Context,
		Store: false, MaxOutputTokens: 6000}
	request.Text.Format.Type = "json_schema"
	request.Text.Format.Name = spec.name
	request.Text.Format.Strict = true
	request.Text.Format.Schema = spec.schema
	body, err := json.Marshal(request)
	if err != nil {
		return "", ErrUpstream
	}
	data, err := a.planner.postJSON(ctx, endpoint, body, http.Header{"Authorization": {"Bearer " + key}})
	if err != nil {
		return "", err
	}
	var response providerResponse
	if err := json.Unmarshal(data, &response); err != nil || response.Status != "completed" {
		return "", ErrUpstream
	}
	return openAIOutput(response)
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type jsonFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

type anthropicRequest struct {
	Model        string             `json:"model"`
	MaxTokens    int                `json:"max_tokens"`
	System       string             `json:"system"`
	Messages     []anthropicMessage `json:"messages"`
	OutputConfig struct {
		Format jsonFormat `json:"format"`
	} `json:"output_config"`
}

type anthropicResponse struct {
	Type       string `json:"type"`
	Role       string `json:"role"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (a anthropicProvider) draft(ctx context.Context, in Input, key, workspace string, spec draftSpec) (string, error) {
	request := anthropicRequest{Model: in.Model, MaxTokens: 6000, System: spec.instructions,
		Messages: []anthropicMessage{{Role: "user", Content: "Testing request:\n" + in.Prompt + "\n\nApplication context:\n" + in.Context}}}
	request.OutputConfig.Format = jsonFormat{Type: "json_schema", Schema: spec.schema}
	body, err := json.Marshal(request)
	if err != nil {
		return "", ErrUpstream
	}
	headers := http.Header{"Authorization": {"Bearer " + key}, "Anthropic-Version": {"2023-06-01"}}
	if workspace != "" {
		headers.Set("anthropic-workspace-id", workspace)
	}
	data, err := a.planner.postJSON(ctx, "https://api.anthropic.com/v1/messages", body, headers)
	if err != nil {
		return "", err
	}
	var response anthropicResponse
	if err := json.Unmarshal(data, &response); err != nil || response.Type != "message" || response.Role != "assistant" || response.StopReason != "end_turn" || len(response.Content) != 1 {
		return "", ErrUpstream
	}
	content := response.Content[0]
	if content.Type != "text" || content.Text == "" {
		return "", ErrUpstream
	}
	return content.Text, nil
}

type geminiText struct {
	Text string `json:"text"`
}
type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiText `json:"parts"`
}

type geminiRequest struct {
	SystemInstruction geminiContent   `json:"systemInstruction"`
	Contents          []geminiContent `json:"contents"`
	GenerationConfig  struct {
		CandidateCount  int `json:"candidateCount"`
		MaxOutputTokens int `json:"maxOutputTokens"`
		ResponseFormat  struct {
			Text struct {
				MIMEType string          `json:"mimeType"`
				Schema   json.RawMessage `json:"schema"`
			} `json:"text"`
		} `json:"responseFormat"`
	} `json:"generationConfig"`
}

type geminiSafetyRating struct {
	Blocked bool `json:"blocked"`
}
type geminiResponse struct {
	Error          json.RawMessage `json:"error"`
	PromptFeedback struct {
		BlockReason   string               `json:"blockReason"`
		SafetyRatings []geminiSafetyRating `json:"safetyRatings"`
	} `json:"promptFeedback"`
	Candidates []struct {
		Index        int    `json:"index"`
		FinishReason string `json:"finishReason"`
		Content      struct {
			Role  string            `json:"role"`
			Parts []json.RawMessage `json:"parts"`
		} `json:"content"`
		SafetyRatings []geminiSafetyRating `json:"safetyRatings"`
	} `json:"candidates"`
}

type geminiResponseText struct {
	Text             string `json:"text"`
	Thought          bool   `json:"thought"`
	ThoughtSignature string `json:"thoughtSignature"`
}

func (a geminiProvider) draft(ctx context.Context, in Input, key, workspace string, spec draftSpec) (string, error) {
	request := geminiRequest{
		SystemInstruction: geminiContent{Parts: []geminiText{{Text: spec.instructions}}},
		Contents:          []geminiContent{{Role: "user", Parts: []geminiText{{Text: "Testing request:\n" + in.Prompt + "\n\nApplication context:\n" + in.Context}}}},
	}
	request.GenerationConfig.CandidateCount = 1
	request.GenerationConfig.MaxOutputTokens = 6000
	request.GenerationConfig.ResponseFormat.Text.MIMEType = "application/json"
	request.GenerationConfig.ResponseFormat.Text.Schema = spec.schema
	body, err := json.Marshal(request)
	if err != nil {
		return "", ErrUpstream
	}
	// Model IDs are validated before they reach this URL path.
	data, err := a.planner.postJSON(ctx, "https://generativelanguage.googleapis.com/v1beta/models/"+in.Model+":generateContent", body, http.Header{"X-Goog-Api-Key": {key}})
	if err != nil {
		return "", err
	}
	var response geminiResponse
	if err := json.Unmarshal(data, &response); err != nil || (len(response.Error) > 0 && string(response.Error) != "null") || len(response.Candidates) != 1 {
		return "", ErrUpstream
	}
	if response.PromptFeedback.BlockReason != "" && response.PromptFeedback.BlockReason != "BLOCK_REASON_UNSPECIFIED" {
		return "", ErrUpstream
	}
	for _, rating := range response.PromptFeedback.SafetyRatings {
		if rating.Blocked {
			return "", ErrUpstream
		}
	}
	candidate := response.Candidates[0]
	if candidate.Index != 0 || candidate.FinishReason != "STOP" || candidate.Content.Role != "model" || len(candidate.Content.Parts) != 1 {
		return "", ErrUpstream
	}
	for _, rating := range candidate.SafetyRatings {
		if rating.Blocked {
			return "", ErrUpstream
		}
	}
	// Parts are a union: accept text only, never function calls, media, or reasoning.
	var part geminiResponseText
	dec := json.NewDecoder(bytes.NewReader(candidate.Content.Parts[0]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&part); err != nil || part.Thought || part.Text == "" {
		return "", ErrUpstream
	}
	return part.Text, nil
}

func (p *Planner) postJSON(ctx context.Context, url string, body []byte, headers http.Header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, ErrUpstream
	}
	req.Header = headers
	req.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(req)
	if err != nil {
		return nil, ErrUpstream
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return nil, ErrProviderAuth
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return nil, ErrProviderRequest
	case http.StatusTooManyRequests:
		return nil, ErrProviderQuota
	default:
		if response.StatusCode >= 500 {
			return nil, ErrProviderUnavailable
		}
		return nil, ErrUpstream
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return nil, ErrUpstream
	}
	return data, nil
}
