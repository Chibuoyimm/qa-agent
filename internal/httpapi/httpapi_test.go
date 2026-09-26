package httpapi_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Chibuoyimm/qa-agent/internal/httpapi"
	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestHTTPBoundary(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := "qa_test_" + hex.EncodeToString(suffix[:])
	if _, err := db.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	testDB, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testDB.Close()
		if _, err := db.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		db.Close()
	})
	if err := migrations.Apply(ctx, testDB); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(qa.NewStore(testDB, map[string]bool{"http://localhost:4174": true}), "api-token", "worker-token", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer server.Close()
	request := func(method, path, token, body string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		content, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp, content
	}
	if resp, _ := request("GET", "/healthz", "", ""); resp.StatusCode != 200 {
		t.Fatalf("health: %d", resp.StatusCode)
	}
	if resp, _ := request("GET", "/api/projects", "", ""); resp.StatusCode != 401 {
		t.Fatalf("missing auth: %d", resp.StatusCode)
	}
	if resp, _ := request("POST", "/api/worker/claim", "api-token", `{"worker_id":"worker"}`); resp.StatusCode != 401 {
		t.Fatalf("worker token separation: %d", resp.StatusCode)
	}
	if resp, _ := request("POST", "/api/projects", "api-token", `{"name":"pilot","base_url":"http://localhost:4174","unexpected":true}`); resp.StatusCode != 400 {
		t.Fatalf("unknown field: %d", resp.StatusCode)
	}
	if resp, _ := request("POST", "/api/projects", "api-token", `{"name":"pilot","base_url":"http://localhost:4174"} {}`); resp.StatusCode != 400 {
		t.Fatalf("trailing JSON: %d", resp.StatusCode)
	}
	if resp, _ := request("POST", "/api/projects", "api-token", `{"name":"pilot","base_url":"http://example.test"}`); resp.StatusCode != 400 {
		t.Fatalf("origin: %d", resp.StatusCode)
	}
	resp, body := request("POST", "/api/projects", "api-token", `{"name":"pilot","base_url":"http://localhost:4174"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("create project: %d %s", resp.StatusCode, body)
	}
	var project qa.Project
	if err := json.Unmarshal(body, &project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM runs WHERE project_id=$1`, `DELETE FROM scenarios WHERE project_id=$1`, `DELETE FROM projects WHERE id=$1`} {
			if _, err := testDB.Exec(ctx, query, project.ID); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	})
	resp, body = request("GET", "/api/projects/"+project.ID+"/scenarios", "api-token", "")
	if resp.StatusCode != 200 || !bytes.Equal(bytes.TrimSpace(body), []byte("[]")) {
		t.Fatalf("empty array: %d %s", resp.StatusCode, body)
	}
	resp, body = request("POST", "/api/projects/"+project.ID+"/scenarios", "api-token", `{"name":"check","expected_outcome":"ready","approved":true,"steps":[{"action":"assert_visible","test_id":"ready","javascript":"alert(1)"}]}`)
	if resp.StatusCode != 400 {
		t.Fatalf("unknown nested field: %d %s", resp.StatusCode, body)
	}
	resp, body = request("POST", "/api/projects/"+project.ID+"/scenarios", "api-token", `{"name":"check","expected_outcome":"ready","approved":true,"steps":[{"action":"assert_visible","test_id":"ready"}]}`)
	if resp.StatusCode != 201 {
		t.Fatalf("create scenario: %d %s", resp.StatusCode, body)
	}
	var scenario qa.Scenario
	if err := json.Unmarshal(body, &scenario); err != nil {
		t.Fatal(err)
	}
	resp, body = request("POST", "/api/projects/"+project.ID+"/runs", "api-token", `{"mode":"blocking","scenario_ids":["`+scenario.ID+`"]}`)
	if resp.StatusCode != 202 {
		t.Fatalf("create run: %d %s", resp.StatusCode, body)
	}
	var run qa.Run
	if err := json.Unmarshal(body, &run); err != nil {
		t.Fatal(err)
	}
	resp, body = request("POST", "/api/worker/claim", "worker-token", `{"worker_id":"worker"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("claim: %d %s", resp.StatusCode, body)
	}
	var claim struct {
		Run        qa.Run `json:"run"`
		LeaseToken string `json:"lease_token"`
	}
	if err := json.Unmarshal(body, &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Run.ID != run.ID || claim.LeaseToken == "" {
		t.Fatalf("claim shape: %s", body)
	}
	resp, body = request("POST", "/api/worker/runs/"+run.ID+"/complete", "worker-token", `{"lease_token":"`+claim.LeaseToken+`","results":[{"scenario_id":"`+scenario.ID+`","status":"passed","message":"ok","duration_ms":10,"artifacts":[]}]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("complete: %d %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &run); err != nil {
		t.Fatal(err)
	}
	if run.Status != "passed" || run.Gate != "pass" {
		t.Fatalf("derived status: %+v", run)
	}
}
