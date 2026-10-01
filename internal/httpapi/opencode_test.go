package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/httpapi"
	"github.com/Chibuoyimm/qa-agent/internal/planner"
)

func TestOpenCodeEndpointsRequireOperator(t *testing.T) {
	p, err := planner.New("", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(nil, p, nil, "operator-token", "worker-token", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer server.Close()
	for _, endpoint := range []struct{ method, path string }{{"GET", "/api/ai/opencode"}, {"POST", "/api/ai/opencode/connect"}, {"POST", "/api/ai/opencode/disconnect"}} {
		for _, token := range []string{"", "worker-token", "operator-token"} {
			r, _ := http.NewRequest(endpoint.method, server.URL+endpoint.path, strings.NewReader(`{"key":"secret-must-not-echo"}`))
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			response, err := server.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			want := http.StatusUnauthorized
			if token == "operator-token" {
				want = http.StatusServiceUnavailable
			}
			if response.StatusCode != want || strings.Contains(string(body), "secret-must-not-echo") {
				t.Fatalf("%s token %q status %d body %s", endpoint.path, token, response.StatusCode, body)
			}
		}
	}
}
