package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/chatgpt"
	"github.com/Chibuoyimm/qa-agent/internal/planner"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
)

func TestChatGPTEndpointsAuthorizationAndLoginLifecycle(t *testing.T) {
	client, err := chatgpt.New(filepath.Join(t.TempDir(), "credentials"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	p, err := planner.New("", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p.ChatGPT = client
	server := httptest.NewServer(New(nil, p, repository.New(nil), "operator", "worker", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer server.Close()
	send := func(method, path, token, body string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, data
	}
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "/api/ai/chatgpt"}, {"GET", "/api/ai/chatgpt/models?profile_id=missing"},
		{"POST", "/api/ai/chatgpt/login"}, {"POST", "/api/ai/chatgpt/login/cancel"},
		{"POST", "/api/ai/chatgpt/select"}, {"POST", "/api/ai/chatgpt/disconnect"},
	} {
		for _, token := range []string{"", "worker"} {
			if status, _ := send(endpoint.method, endpoint.path, token, `{}`); status != 401 {
				t.Fatalf("%s denied token got %d", endpoint.path, status)
			}
		}
	}
	code, data := send("GET", "/api/ai/config", "operator", "")
	var config planner.Config
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if code != 200 || config.ChatGPT == nil || !config.ChatGPT.Enabled || len(config.Models) != 0 || config.ManagedAvailable || config.BYOKAvailable {
		t.Fatal("subscription must be available independently of API keys")
	}
	if code, _ := send("POST", "/api/ai/chatgpt/login", "operator", `{"unexpected":true}`); code != 400 {
		t.Fatalf("unknown login input accepted: %d", code)
	}
	code, data = send("POST", "/api/ai/chatgpt/login", "operator", `{}`)
	var login chatgpt.Login
	if err := json.Unmarshal(data, &login); err != nil {
		t.Fatal(err)
	}
	if code != 202 || login.ID == "" {
		t.Fatalf("login not accepted: %d", code)
	}
	auth, err := url.Parse(login.AuthURL)
	if err != nil || auth.Scheme != "https" || auth.Host != "auth.openai.com" || auth.Query().Get("client_id") != "dynamic_agent_client" {
		t.Fatal("unexpected sign-in target")
	}
	if code, _ := send("POST", "/api/ai/chatgpt/login", "operator", `{}`); code != 409 {
		t.Fatalf("overlapping sign-in accepted: %d", code)
	}
	code, data = send("GET", "/api/ai/chatgpt", "operator", "")
	var status chatgpt.Status
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	if code != 200 || status.Login == nil || status.Login.Status != "pending" {
		t.Fatal("pending login missing")
	}
	if strings.Contains(string(data), "auth_url") || strings.Contains(string(data), "code_verifier") || strings.Contains(string(data), "access_token") || strings.Contains(string(data), "refresh_token") {
		t.Fatal("status leaked credentials")
	}
	payload, _ := json.Marshal(struct {
		LoginID string `json:"login_id"`
	}{login.ID})
	code, data = send("POST", "/api/ai/chatgpt/login/cancel", "operator", string(payload))
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	if code != 200 || status.Login == nil || status.Login.Status != "cancelled" {
		t.Fatal("login cancellation missing")
	}
	for _, path := range []string{"/api/ai/chatgpt/select", "/api/ai/chatgpt/disconnect"} {
		if code, _ := send("POST", path, "operator", `{"profile_id":"missing"}`); code != 400 {
			t.Fatalf("unknown account %s got %d", path, code)
		}
	}
	if code, _ := send("GET", "/api/ai/chatgpt/models?profile_id=missing", "operator", ""); code != 400 {
		t.Fatalf("unknown model account got %d", code)
	}
}

func TestChatGPTDisabledEndpoints(t *testing.T) {
	p, err := planner.New("", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(nil, p, repository.New(nil), "operator", "worker", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "/api/ai/chatgpt"}, {"GET", "/api/ai/chatgpt/models?profile_id=missing"},
		{"POST", "/api/ai/chatgpt/login"}, {"POST", "/api/ai/chatgpt/login/cancel"},
		{"POST", "/api/ai/chatgpt/select"}, {"POST", "/api/ai/chatgpt/disconnect"},
	} {
		req := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer operator")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != 503 {
			t.Fatalf("disabled %s got%d", endpoint.path, response.Code)
		}
	}
}
