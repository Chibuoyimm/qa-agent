package httpapi_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"github.com/Chibuoyimm/qa-agent/internal/planner"
	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
	"github.com/Chibuoyimm/qa-agent/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

type rejectingTransport struct{}

func (rejectingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("provider must not be called")
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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
	proposals, err := planner.New("", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	store := qa.NewStore(testDB, map[string]bool{"http://localhost:4174": true})
	server := httptest.NewServer(httpapi.New(store, proposals, repository.New(nil), "api-token", "worker-token", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer server.Close()
	baseURL := server.URL
	request := func(method, path, token, body string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, baseURL+path, strings.NewReader(body))
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
	if resp, _ := request("GET", "/api/ai/config", "", ""); resp.StatusCode != 401 {
		t.Fatalf("AI config auth: %d", resp.StatusCode)
	}
	resp, body := request("GET", "/api/ai/config", "api-token", "")
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte(`"models":[]`)) || !bytes.Contains(body, []byte(`"managed_available":false`)) {
		t.Fatalf("AI config: %d %s", resp.StatusCode, body)
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
	resp, body = request("POST", "/api/projects", "api-token", `{"name":"pilot","base_url":"http://localhost:4174"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("create project: %d %s", resp.StatusCode, body)
	}
	var project qa.Project
	if err := json.Unmarshal(body, &project); err != nil {
		t.Fatal(err)
	}
	if resp, _ := request("POST", "/api/projects/"+project.ID+"/proposals", "api-token", `{"prompt":"check","context":"context","model":"test-model","credential_mode":"byok","consent":true,"extra":1}`); resp.StatusCode != 400 {
		t.Fatalf("unknown proposal field: %d", resp.StatusCode)
	}
	if resp, _ := request("POST", "/api/projects/"+project.ID+"/proposals", "api-token", `{"prompt":"check","context":"context","model":"test-model","credential_mode":"byok","consent":true}`); resp.StatusCode != 503 {
		t.Fatalf("unconfigured model: %d", resp.StatusCode)
	}
	guardPlanner, err := planner.New("test-model", "", rejectingTransport{})
	if err != nil {
		t.Fatal(err)
	}
	guardServer := httptest.NewServer(httpapi.New(store, guardPlanner, repository.New(nil), "api-token", "worker-token", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer guardServer.Close()
	baseURL = guardServer.URL
	validProposal := `{"prompt":"check","context":"context","model":"test-model","credential_mode":"byok","consent":true}`
	if resp, _ := request("POST", "/api/projects/"+project.ID+"/proposals", "api-token", strings.Replace(validProposal, `"consent":true`, `"consent":false`, 1)); resp.StatusCode != 400 {
		t.Fatalf("missing consent: %d", resp.StatusCode)
	}
	if resp, _ := request("POST", "/api/projects/"+project.ID+"/proposals", "api-token", strings.Replace(validProposal, `"byok"`, `"managed"`, 1)); resp.StatusCode != 503 {
		t.Fatalf("managed unavailable: %d", resp.StatusCode)
	}
	if resp, _ := request("POST", "/api/projects/missing/proposals", "api-token", validProposal); resp.StatusCode != 400 {
		t.Fatalf("missing BYOK key: %d", resp.StatusCode)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/projects/missing/proposals", strings.NewReader(validProposal))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer api-token")
	req.Header.Set("X-QA-Provider-Key", "test-only-key")
	missing, err := guardServer.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	missing.Body.Close()
	if missing.StatusCode != 404 {
		t.Fatalf("missing project: %d", missing.StatusCode)
	}
	const (
		commitSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		treeSHA   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		blobSHA   = "cccccccccccccccccccccccccccccccccccccccc"
	)
	githubCalls := 0
	github := repository.New(transportFunc(func(req *http.Request) (*http.Response, error) {
		githubCalls++
		if req.URL.Host != "api.github.com" || req.Header.Get("Authorization") != "Bearer transient-github-key" {
			t.Errorf("unexpected GitHub destination or token")
		}
		var payload string
		switch req.URL.Path {
		case "/repos/owner/name/commits/main":
			payload = `{"sha":"` + commitSHA + `","commit":{"tree":{"sha":"` + treeSHA + `"}}}`
		case "/repos/owner/name/git/trees/" + treeSHA:
			payload = `{"sha":"` + treeSHA + `","tree":[{"path":"README.md","mode":"100644","type":"blob","size":5,"sha":"` + blobSHA + `"}]}`
		case "/repos/owner/name/git/blobs/" + blobSHA:
			payload = `{"sha":"` + blobSHA + `","encoding":"base64","size":5,"content":"aGVsbG8="}`
		default:
			t.Errorf("unexpected GitHub path %q", req.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}, nil
	}))
	syncServer := httptest.NewServer(httpapi.New(store, proposals, github, "api-token", "worker-token", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer syncServer.Close()
	syncRequest, err := http.NewRequest(http.MethodPost, syncServer.URL+"/api/projects/"+project.ID+"/repositories/sync",
		strings.NewReader(`{"repository":"owner/name","ref":"main","role":"backend","paths":["README.md"]}`))
	if err != nil {
		t.Fatal(err)
	}
	syncRequest.Header.Set("Authorization", "Bearer api-token")
	syncRequest.Header.Set("X-QA-GitHub-Token", "transient-github-key")
	syncResponse, err := syncServer.Client().Do(syncRequest)
	if err != nil {
		t.Fatal(err)
	}
	syncContent, err := io.ReadAll(syncResponse.Body)
	syncResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if syncResponse.StatusCode != 201 || githubCalls != 3 || bytes.Contains(syncContent, []byte("transient-github-key")) {
		t.Fatalf("repository sync: %d calls=%d body=%s", syncResponse.StatusCode, githubCalls, syncContent)
	}
	var synced qa.RepositorySnapshot
	if err := json.Unmarshal(syncContent, &synced); err != nil {
		t.Fatal(err)
	}
	storedSync, err := store.GetRepositorySnapshot(ctx, project.ID, synced.ID)
	if err != nil || len(storedSync.Files) != 1 || storedSync.Files[0].Content != "hello" {
		t.Fatalf("stored sync: %+v %v", storedSync, err)
	}
	imported := repository.Snapshot{
		Repository: "owner/name", Ref: "main", Role: "frontend", CommitSHA: strings.Repeat("a", 40),
		ContentSHA256: strings.Repeat("b", 64), Files: []repository.File{{Path: "src/app.ts", Content: "the revenue test ID is revenue"}},
		TotalBytes: len("the revenue test ID is revenue"),
	}
	snapshot, err := store.CreateRepositorySnapshot(ctx, project.ID, imported)
	if err != nil {
		t.Fatal(err)
	}
	baseURL = server.URL
	resp, body = request("GET", "/api/projects/"+project.ID+"/repositories", "api-token", "")
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte(snapshot.ID)) || bytes.Contains(body, []byte("the revenue test ID")) {
		t.Fatalf("repository summaries: %d %s", resp.StatusCode, body)
	}
	resp, body = request("GET", "/api/projects/"+project.ID+"/repositories/"+snapshot.ID, "api-token", "")
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte("the revenue test ID")) {
		t.Fatalf("repository content: %d %s", resp.StatusCode, body)
	}
	resp, _ = request("GET", "/api/projects/missing/repositories/"+snapshot.ID, "api-token", "")
	if resp.StatusCode != 404 {
		t.Fatalf("repository project isolation: %d", resp.StatusCode)
	}
	sendProposal := func(url, payload string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, url+"/api/projects/"+project.ID+"/proposals", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer api-token")
		req.Header.Set("X-QA-Provider-Key", "test-only-key")
		response, err := guardServer.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		content, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response, content
	}
	proposalJSON := func(contextText string, ids []string) string {
		t.Helper()
		encoded, err := json.Marshal(planner.Input{Prompt: "check revenue", Context: contextText, Model: "test-model", CredentialMode: "byok", Consent: true, RepositorySnapshotIDs: ids})
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	for name, ids := range map[string][]string{
		"duplicate": {snapshot.ID, snapshot.ID},
		"missing":   {"missing"},
		"too many":  {snapshot.ID, "other", "third"},
	} {
		response, content := sendProposal(guardServer.URL, proposalJSON("", ids))
		if response.StatusCode != 400 {
			t.Fatalf("%s snapshot IDs: %d %s", name, response.StatusCode, content)
		}
	}
	response, content := sendProposal(guardServer.URL, proposalJSON(strings.Repeat("x", 59_950), []string{snapshot.ID}))
	if response.StatusCode != 400 {
		t.Fatalf("oversized final context: %d %s", response.StatusCode, content)
	}
	var sentContext string
	provider := transportFunc(func(req *http.Request) (*http.Response, error) {
		var payload struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		sentContext = strings.TrimPrefix(payload.Input, "Testing request:\ncheck revenue\n\nApplication context:\n")
		output := `{"scenarios":[],"questions":["What is expected?"],"assumptions":[]}`
		encoded, err := json.Marshal(output)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":` + string(encoded) + `}]}]}`))}, nil
	})
	workingPlanner, err := planner.New("test-model", "", provider)
	if err != nil {
		t.Fatal(err)
	}
	workingServer := httptest.NewServer(httpapi.New(store, workingPlanner, repository.New(nil), "api-token", "worker-token", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer workingServer.Close()
	response, content = sendProposal(workingServer.URL, proposalJSON("", []string{snapshot.ID}))
	if response.StatusCode != 200 {
		t.Fatalf("snapshot proposal: %d %s", response.StatusCode, content)
	}
	var proposal planner.Result
	if err := json.Unmarshal(content, &proposal); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(sentContext))
	if !strings.Contains(sentContext, "the revenue test ID is revenue") || !strings.Contains(sentContext, "untrusted source data") ||
		proposal.ContextSHA256 != hex.EncodeToString(hash[:]) || len(proposal.RepositorySnapshots) != 1 || proposal.RepositorySnapshots[0].ID != snapshot.ID {
		t.Fatalf("snapshot proposal context or metadata: %+v %q", proposal, sentContext)
	}
	baseURL = server.URL
	resp, body = request("POST", "/api/projects/"+project.ID+"/discoveries", "api-token", `{"start_path":"/dashboard","max_pages":2}`)
	if resp.StatusCode != 202 {
		t.Fatalf("create discovery: %d %s", resp.StatusCode, body)
	}
	var discovery qa.Discovery
	if err := json.Unmarshal(body, &discovery); err != nil {
		t.Fatal(err)
	}
	resp, body = request("GET", "/api/projects/"+project.ID+"/discoveries", "api-token", "")
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte(discovery.ID)) {
		t.Fatalf("list discovery: %d %s", resp.StatusCode, body)
	}
	resp, body = request("POST", "/api/worker/discoveries/claim", "worker-token", `{"worker_id":"worker"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("claim discovery: %d %s", resp.StatusCode, body)
	}
	var discoveryClaim struct {
		Discovery  qa.Discovery `json:"discovery"`
		LeaseToken string       `json:"lease_token"`
	}
	if err := json.Unmarshal(body, &discoveryClaim); err != nil {
		t.Fatal(err)
	}
	if discoveryClaim.Discovery.ID != discovery.ID || discoveryClaim.LeaseToken == "" {
		t.Fatalf("discovery claim: %+v", discoveryClaim)
	}
	result := qa.DiscoveryResult{Pages: []qa.DiscoveryPage{{Path: "/dashboard", Title: "Revenue is 140000", Headings: []string{}, Elements: []qa.DiscoveryElement{}, Links: []qa.DiscoveryLink{}}}, Warnings: []string{}}
	completion, err := json.Marshal(qa.CompleteDiscoveryInput{LeaseToken: discoveryClaim.LeaseToken, Result: &result})
	if err != nil {
		t.Fatal(err)
	}
	resp, body = request("POST", "/api/worker/discoveries/"+discovery.ID+"/complete", "worker-token", string(completion))
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte(`"status":"completed"`)) {
		t.Fatalf("complete discovery: %d %s", resp.StatusCode, body)
	}
	discoveryProposal, err := json.Marshal(planner.Input{Prompt: "check revenue", Model: "test-model", CredentialMode: "byok", Consent: true, DiscoveryID: discovery.ID})
	if err != nil {
		t.Fatal(err)
	}
	response, content = sendProposal(workingServer.URL, string(discoveryProposal))
	if response.StatusCode != 200 {
		t.Fatalf("discovery proposal: %d %s", response.StatusCode, content)
	}
	if err := json.Unmarshal(content, &proposal); err != nil {
		t.Fatal(err)
	}
	hash = sha256.Sum256([]byte(sentContext))
	if proposal.DiscoveryID != discovery.ID || proposal.ContextSHA256 != hex.EncodeToString(hash[:]) ||
		!strings.Contains(sentContext, "untrusted observations, not business expectations") || !strings.Contains(sentContext, "Revenue is 140000") {
		t.Fatalf("discovery context or traceability: %+v %q", proposal, sentContext)
	}
	discoveryProposal, err = json.Marshal(planner.Input{Prompt: "check revenue", Context: strings.Repeat("x", 59_950), Model: "test-model", CredentialMode: "byok", Consent: true, DiscoveryID: discovery.ID})
	if err != nil {
		t.Fatal(err)
	}
	response, content = sendProposal(guardServer.URL, string(discoveryProposal))
	if response.StatusCode != 400 {
		t.Fatalf("oversized discovery context: %d %s", response.StatusCode, content)
	}
	baseURL = server.URL
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM releases WHERE project_id=$1`, `DELETE FROM runs WHERE project_id=$1`, `DELETE FROM scenarios WHERE project_id=$1`, `DELETE FROM repository_snapshots WHERE project_id=$1`, `DELETE FROM discoveries WHERE project_id=$1`, `DELETE FROM projects WHERE id=$1`} {
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
	releaseInput := qa.ReleaseInput{
		DeploymentKey: "deploy-123", BaseURL: "http://localhost:4174", Mode: "blocking", ScenarioIDs: []string{scenario.ID},
		Repositories: []qa.ReleaseRepositoryInput{{SnapshotID: snapshot.ID, Repository: snapshot.Repository, Role: snapshot.Role, CommitSHA: snapshot.CommitSHA}},
	}
	releasePayload, err := json.Marshal(releaseInput)
	if err != nil {
		t.Fatal(err)
	}
	releasePath := "/api/projects/" + project.ID + "/releases"
	if resp, _ := request("POST", releasePath, "", string(releasePayload)); resp.StatusCode != 401 {
		t.Fatalf("release authorization: %d", resp.StatusCode)
	}
	resp, body = request("POST", releasePath, "api-token", string(releasePayload))
	if resp.StatusCode != 202 {
		t.Fatalf("create release: %d %s", resp.StatusCode, body)
	}
	var release qa.Release
	if err := json.Unmarshal(body, &release); err != nil {
		t.Fatal(err)
	}
	if release.ID == "" || release.Run.ID == "" || release.Run.Scenarios[0].ID != scenario.ID || release.Repositories[0].ContentSHA256 != snapshot.ContentSHA256 {
		t.Fatalf("release response: %+v", release)
	}
	resp, body = request("POST", releasePath, "api-token", string(releasePayload))
	if resp.StatusCode != 202 || !bytes.Contains(body, []byte(release.Run.ID)) {
		t.Fatalf("release retry: %d %s", resp.StatusCode, body)
	}
	for _, path := range []string{releasePath + "/" + release.ID, releasePath + "/by-key/" + release.DeploymentKey} {
		resp, body = request("GET", path, "api-token", "")
		if resp.StatusCode != 200 || !bytes.Contains(body, []byte(release.Run.ID)) {
			t.Fatalf("release detail %s: %d %s", path, resp.StatusCode, body)
		}
	}
	resp, body = request("GET", releasePath, "api-token", "")
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte(release.ID)) {
		t.Fatalf("release list: %d %s", resp.StatusCode, body)
	}
	resp, _ = request("GET", "/api/projects/missing/releases/"+release.ID, "api-token", "")
	if resp.StatusCode != 404 {
		t.Fatalf("cross-project release: %d", resp.StatusCode)
	}
	releaseInput.BaseURL = "http://example.test"
	deniedPayload, err := json.Marshal(releaseInput)
	if err != nil {
		t.Fatal(err)
	}
	resp, _ = request("POST", releasePath, "api-token", string(deniedPayload))
	if resp.StatusCode != 400 {
		t.Fatalf("denied origin: %d", resp.StatusCode)
	}
	releaseInput.BaseURL = "http://localhost:4174"
	releaseInput.Mode = "advisory"
	changedPayload, err := json.Marshal(releaseInput)
	if err != nil {
		t.Fatal(err)
	}
	resp, _ = request("POST", releasePath, "api-token", string(changedPayload))
	if resp.StatusCode != 409 {
		t.Fatalf("changed release: %d", resp.StatusCode)
	}
	resp, body = request("POST", "/api/worker/claim", "worker-token", `{"worker_id":"release-worker"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("claim release run: %d %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Run.ID != release.Run.ID {
		t.Fatalf("claimed unexpected run: %+v", claim.Run)
	}
	resp, body = request("POST", "/api/worker/runs/"+release.Run.ID+"/complete", "worker-token", `{"lease_token":"`+claim.LeaseToken+`","results":[{"scenario_id":"`+scenario.ID+`","status":"failed","message":"dashboard total incorrect","duration_ms":10,"artifacts":[]}]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("complete release run: %d %s", resp.StatusCode, body)
	}
	resp, body = request("GET", releasePath+"/by-key/"+release.DeploymentKey, "api-token", "")
	if resp.StatusCode != 200 {
		t.Fatalf("completed release detail: %d %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &release); err != nil {
		t.Fatal(err)
	}
	if release.Run.Status != "failed" || release.Run.Gate != "fail" || len(release.Run.Results) != 1 || release.Run.Results[0].Message != "dashboard total incorrect" {
		t.Fatalf("completed release status: %+v", release.Run)
	}
}
