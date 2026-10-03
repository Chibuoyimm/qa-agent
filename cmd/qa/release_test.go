package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
)

const (
	releaseCommit = "0123456789012345678901234567890123456789"
	releaseHash   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func testReleaseManifest(base string) releaseManifest {
	return releaseManifest{
		ProjectID:     "p1",
		DeploymentKey: "_production-123",
		BaseURL:       base,
		ReadinessPath: "/healthz",
		Mode:          "blocking",
		ScenarioIDs:   []string{"s1"},
		Repositories: []releaseManifestSource{{
			Repository: "owner/frontend", Role: "frontend", CommitSHA: releaseCommit,
			Paths: []string{"src/z.ts", "src/a.ts"},
		}},
	}
}

func testRelease(manifest releaseManifest, status, gate string) qa.Release {
	return qa.Release{
		ID: "rel1", ProjectID: manifest.ProjectID, DeploymentKey: manifest.DeploymentKey,
		BaseURL: manifest.BaseURL, Mode: manifest.Mode, CreatedAt: time.Now(),
		Repositories: []qa.ReleaseRepository{{
			SnapshotID: "snap1", Provider: manifest.Repositories[0].Provider, Repository: manifest.Repositories[0].Repository, Role: "frontend", CommitSHA: releaseCommit,
			ContentSHA256: releaseHash, Paths: []string{"src/a.ts", "src/z.ts"},
		}},
		Run: qa.Run{
			ID: "run1", ProjectID: manifest.ProjectID, BaseURL: manifest.BaseURL,
			Mode: manifest.Mode, Status: status, Gate: gate, CreatedAt: time.Now(),
			Scenarios: []qa.Scenario{{ID: "s1", ProjectID: manifest.ProjectID, ScenarioInput: qa.ScenarioInput{Approved: true}}},
		},
	}
}

func writeReleaseManifest(t *testing.T, manifest releaseManifest) string {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "release.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func releaseEnv(apiBase string) func(string) string {
	return func(key string) string {
		switch key {
		case "QA_API_BASE_URL":
			return apiBase
		case "QA_API_TOKEN":
			return "api-secret"
		case "QA_GITHUB_TOKEN":
			return "github-secret"
		case "QA_AZURE_PAT":
			return "azure-secret"
		default:
			return ""
		}
	}
}

func TestReleaseCLIImportsExactSnapshotAndPasses(t *testing.T) {
	for _, provider := range []string{"github", "azure"} {
		t.Run(provider, func(t *testing.T) {
			readinessCalls := 0
			redirected := false
			redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
			defer redirectTarget.Close()
			readyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				readinessCalls++
				if r.Method != http.MethodGet || r.URL.Path != "/healthz" || r.Header.Get("Authorization") != "" || (r.Header.Get("X-QA-GitHub-Token") != "" || r.Header.Get("X-QA-Azure-PAT") != "") {
					t.Errorf("readiness request carried credentials or used the wrong target: %s %s", r.Method, r.URL.Path)
				}
				if readinessCalls == 1 {
					http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
					return
				}
				if readinessCalls == 2 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer readyServer.Close()
			manifest := testReleaseManifest(readyServer.URL)
			manifest.Repositories[0].Provider = provider
			if provider == "azure" {
				manifest.Repositories[0].Repository = "https://dev.azure.com/team/project/_git/app"
			}
			release := testRelease(manifest, "queued", "pending")
			imports, creates, polls := 0, 0, 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer api-secret" {
					t.Error("API request missing authorization")
				}
				if r.URL.Path != "/api/projects/p1/repositories/sync" && (r.Header.Get("X-QA-GitHub-Token") != "" || r.Header.Get("X-QA-Azure-PAT") != "") {
					t.Error("GitHub credential sent outside repository import")
				}
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/projects/p1/releases/by-key/_production-123":
					w.WriteHeader(http.StatusNotFound)
				case r.Method == http.MethodPost && r.URL.Path == "/api/projects/p1/repositories/sync":
					imports++
					expectedGithub, expectedAzure := "github-secret", ""
					if provider == "azure" {
						expectedGithub, expectedAzure = "", "azure-secret"
					}
					if r.Header.Get("X-QA-GitHub-Token") != expectedGithub || r.Header.Get("X-QA-Azure-PAT") != expectedAzure {
						t.Error("missing import token")
					}
					var in repository.Input
					if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
						t.Fatal(err)
					}
					if in.Ref != releaseCommit || in.Repository != manifest.Repositories[0].Repository || in.Provider != provider || in.Role != "frontend" || strings.Join(in.Paths, ",") != "src/a.ts,src/z.ts" {
						t.Errorf("wrong immutable import: %+v", in)
					}
					w.WriteHeader(http.StatusCreated)
					json.NewEncoder(w).Encode(qa.RepositorySnapshot{
						RepositorySummary: qa.RepositorySummary{ID: "snap1", ProjectID: "p1", Provider: in.Provider, Repository: in.Repository, Role: in.Role,
							CommitSHA: in.Ref, ContentSHA256: releaseHash},
						Files: []repository.File{{Path: "src/a.ts"}, {Path: "src/z.ts"}},
					})
				case r.Method == http.MethodPost && r.URL.Path == "/api/projects/p1/releases":
					creates++
					var in qa.ReleaseInput
					if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
						t.Fatal(err)
					}
					if in.BaseURL != manifest.BaseURL || in.DeploymentKey != manifest.DeploymentKey || len(in.Repositories) != 1 || in.Repositories[0].Provider != provider || in.Repositories[0].SnapshotID != "snap1" || in.Repositories[0].CommitSHA != releaseCommit {
						t.Errorf("wrong release input: %+v", in)
					}
					w.WriteHeader(http.StatusAccepted)
					json.NewEncoder(w).Encode(release)
				case r.Method == http.MethodGet && r.URL.Path == "/api/projects/p1/releases/rel1":
					polls++
					completed := release
					completed.Run.Status, completed.Run.Gate = "passed", "pass"
					completed.Run.Results = []qa.ScenarioResult{{ScenarioID: "s1", Status: "passed"}}
					json.NewEncoder(w).Encode(completed)
				default:
					t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer api.Close()
			var out, errOut bytes.Buffer
			code := execute(context.Background(), []string{"release", "--manifest", writeReleaseManifest(t, manifest), "--poll", "1ms", "--json"}, releaseEnv(api.URL), &out, &errOut)
			if code != 0 || imports != 1 || creates != 1 || polls != 1 || readinessCalls != 3 || redirected {
				t.Fatalf("code=%d imports=%d creates=%d polls=%d readiness=%d redirected=%v stderr=%s", code, imports, creates, polls, readinessCalls, redirected, errOut.String())
			}
			var output qa.Release
			if err := json.Unmarshal(out.Bytes(), &output); err != nil || output.Run.Status != "passed" || output.Run.Gate != "pass" || strings.Count(strings.TrimSpace(out.String()), "\n") != 0 {
				t.Fatalf("stdout should be one terminal release object: %s", out.String())
			}
			if strings.Contains(errOut.String(), "api-secret") || strings.Contains(errOut.String(), "github-secret") || strings.Contains(errOut.String(), "azure-secret") {
				t.Fatal("credential appeared in progress output")
			}
		})
	}
}

func TestReleaseCLIResumesFailedBlockingRunWithoutSideEffects(t *testing.T) {
	readyCalls := 0
	readyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { readyCalls++ }))
	defer readyServer.Close()
	manifest := testReleaseManifest(readyServer.URL)
	release := testRelease(manifest, "failed", "fail")
	release.Run.Results = []qa.ScenarioResult{{ScenarioID: "s1", Status: "failed", Message: "expected 140000, saw 230000"}}
	apiCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls++
		if r.Method != http.MethodGet || r.URL.Path != "/api/projects/p1/releases/by-key/_production-123" {
			t.Errorf("unexpected retry side effect: %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(release)
	}))
	defer api.Close()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"release", "--manifest", writeReleaseManifest(t, manifest), "--json"}, releaseEnv(api.URL), &out, &errOut)
	if code != 1 || apiCalls != 1 || readyCalls != 0 {
		t.Fatalf("code=%d apiCalls=%d readyCalls=%d stderr=%s", code, apiCalls, readyCalls, errOut.String())
	}
	var output qa.Release
	if err := json.Unmarshal(out.Bytes(), &output); err != nil || output.Run.ID != "run1" || output.Run.Gate != "fail" {
		t.Fatalf("wrong terminal release JSON: %s", out.String())
	}
}

func TestReleaseCLIRejectsChangedDeploymentKeyContents(t *testing.T) {
	manifest := testReleaseManifest("https://deployed.example")
	existing := testRelease(manifest, "passed", "pass")
	existing.Repositories[0].Paths = []string{"different.ts"}
	apiCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls++
		json.NewEncoder(w).Encode(existing)
	}))
	defer api.Close()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"release", "--manifest", writeReleaseManifest(t, manifest), "--json"}, releaseEnv(api.URL), &out, &errOut)
	if code != 2 || apiCalls != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), "different or inconsistent") {
		t.Fatalf("code=%d calls=%d stdout=%s stderr=%s", code, apiCalls, out.String(), errOut.String())
	}
}

func TestReleaseCLIRejectsChangedSnapshotDuringPolling(t *testing.T) {
	manifest := testReleaseManifest("https://deployed.example")
	release := testRelease(manifest, "queued", "pending")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/by-key/") {
			json.NewEncoder(w).Encode(release)
			return
		}
		changed := release
		changed.Repositories = append([]qa.ReleaseRepository(nil), release.Repositories...)
		changed.Repositories[0].SnapshotID = "snap2"
		changed.Run.Status, changed.Run.Gate = "passed", "pass"
		changed.Run.Results = []qa.ScenarioResult{{ScenarioID: "s1", Status: "passed"}}
		json.NewEncoder(w).Encode(changed)
	}))
	defer api.Close()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"release", "--manifest", writeReleaseManifest(t, manifest), "--poll", "1ms", "--json"}, releaseEnv(api.URL), &out, &errOut)
	if code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "inconsistent") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}

func TestReleaseCLIRejectsPartialPollResponse(t *testing.T) {
	manifest := testReleaseManifest("https://deployed.example")
	release := testRelease(manifest, "queued", "pending")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/by-key/") {
			json.NewEncoder(w).Encode(release)
			return
		}
		fmt.Fprint(w, `{"run":{"status":"passed","gate":"pass","results":[{"scenario_id":"s1","status":"passed"}]}}`)
	}))
	defer api.Close()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"release", "--manifest", writeReleaseManifest(t, manifest), "--poll", "1ms", "--json"}, releaseEnv(api.URL), &out, &errOut)
	if code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "inconsistent") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}

func TestReleaseCLIReadinessTimesOutWithoutCredentials(t *testing.T) {
	var readyCalls atomic.Int32
	readyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readyCalls.Add(1)
		if r.Header.Get("Authorization") != "" || (r.Header.Get("X-QA-GitHub-Token") != "" || r.Header.Get("X-QA-Azure-PAT") != "") {
			t.Error("credential sent to deployment")
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer readyServer.Close()
	manifest := testReleaseManifest(readyServer.URL)
	var apiCalls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer api.Close()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"release", "--manifest", writeReleaseManifest(t, manifest), "--timeout", "30ms", "--poll", "1ms", "--json"}, releaseEnv(api.URL), &out, &errOut)
	if code != 2 || readyCalls.Load() < 2 || apiCalls.Load() != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), "did not become ready") {
		t.Fatalf("code=%d ready=%d api=%d stdout=%s stderr=%s", code, readyCalls.Load(), apiCalls.Load(), out.String(), errOut.String())
	}
}

func TestReleaseManifestStrictValidation(t *testing.T) {
	base := testReleaseManifest("https://deployed.example")
	for _, tc := range []struct {
		name string
		edit func(*releaseManifest)
	}{
		{"short commit", func(m *releaseManifest) { m.Repositories[0].CommitSHA = "main" }},
		{"unsafe source", func(m *releaseManifest) { m.Repositories[0].Paths = []string{".env"} }},
		{"duplicate scenario", func(m *releaseManifest) { m.ScenarioIDs = []string{"s1", "s1"} }},
		{"duplicate role", func(m *releaseManifest) { m.Repositories = append(m.Repositories, m.Repositories[0]) }},
		{"readiness query", func(m *releaseManifest) { m.ReadinessPath = "/healthz?token=abc" }},
		{"origin credential", func(m *releaseManifest) { m.BaseURL = "https://user:pass@deployed.example" }},
		{"invalid key", func(m *releaseManifest) { m.DeploymentKey = "retry/1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := base
			manifest.Repositories = append([]releaseManifestSource(nil), base.Repositories...)
			tc.edit(&manifest)
			if _, err := readReleaseManifest(writeReleaseManifest(t, manifest)); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(path, []byte(`{"project_id":"p1","unexpected":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readReleaseManifest(path); err == nil {
		t.Fatal("accepted unknown manifest field")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", maxReleaseManifest+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readReleaseManifest(path); err == nil {
		t.Fatal("accepted oversized manifest")
	}
}

func TestReleaseCLIRejectsInvalidImportAndDoesNotCreate(t *testing.T) {
	readyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer readyServer.Close()
	manifest := testReleaseManifest(readyServer.URL)
	created := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path == "/api/projects/p1/repositories/sync" {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":"snap1","project_id":"p1","repository":"owner/frontend","role":"frontend","commit_sha":"%s","content_sha256":"%s","files":[{"path":"different.ts"}]}`, releaseCommit, releaseHash)
			return
		}
		created = true
	}))
	defer api.Close()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"release", "--manifest", writeReleaseManifest(t, manifest)}, releaseEnv(api.URL), &out, &errOut)
	if code != 2 || created || out.Len() != 0 || !strings.Contains(errOut.String(), "inconsistent snapshot") {
		t.Fatalf("code=%d created=%v stdout=%s stderr=%s", code, created, out.String(), errOut.String())
	}
}

func TestReleaseCLIRejectsHeaderInjection(t *testing.T) {
	manifest := testReleaseManifest("https://deployed.example")
	getenv := releaseEnv("http://127.0.0.1:1")
	getenv = func(key string) string {
		if key == "QA_API_TOKEN" {
			return "secret\r\nX-Injected: yes"
		}
		return releaseEnv("http://127.0.0.1:1")(key)
	}
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"release", "--manifest", writeReleaseManifest(t, manifest)}, getenv, &out, &errOut)
	if code != 2 || out.Len() != 0 || strings.Contains(errOut.String(), "secret") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}

func TestReleaseCLIAdvisoryFailureWarnsWithSuccess(t *testing.T) {
	manifest := testReleaseManifest("https://deployed.example")
	manifest.Mode = "advisory"
	release := testRelease(manifest, "failed", "warn")
	release.Run.Results = []qa.ScenarioResult{{ScenarioID: "s1", Status: "failed"}}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(release) }))
	defer api.Close()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"release", "--manifest", writeReleaseManifest(t, manifest), "--json"}, releaseEnv(api.URL), &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), `"gate":"warn"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}

func TestReleaseCLIRejectsMalformedTerminalPass(t *testing.T) {
	manifest := testReleaseManifest("https://deployed.example")
	release := testRelease(manifest, "passed", "pass")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(release) }))
	defer api.Close()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"release", "--manifest", writeReleaseManifest(t, manifest), "--json", "--timeout", (time.Second).String()}, releaseEnv(api.URL), &out, &errOut)
	if code != 2 || out.Len() != 0 {
		t.Fatalf("malformed pass returned code=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}
