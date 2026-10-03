package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
)

const maxReleaseManifest = 64 << 10

type releaseManifest struct {
	ProjectID     string                  `json:"project_id"`
	DeploymentKey string                  `json:"deployment_key"`
	BaseURL       string                  `json:"base_url"`
	ReadinessPath string                  `json:"readiness_path"`
	Mode          string                  `json:"mode"`
	ScenarioIDs   []string                `json:"scenario_ids"`
	Repositories  []releaseManifestSource `json:"repositories"`
}

type releaseManifestSource struct {
	Provider   string   `json:"provider,omitempty"`
	Repository string   `json:"repository"`
	Role       string   `json:"role"`
	CommitSHA  string   `json:"commit_sha"`
	Paths      []string `json:"paths"`
}

func executeRelease(ctx context.Context, args []string, getenv func(string) string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("qa release", flag.ContinueOnError)
	flags.SetOutput(errOut)
	manifestPath := flags.String("manifest", "", "Strict release JSON manifest")
	timeout := flags.Duration("timeout", 10*time.Minute, "Overall deadline for readiness, imports, creation, and polling")
	poll := flags.Duration("poll", time.Second, "Polling interval")
	jsonOutput := flags.Bool("json", false, "Print the terminal release JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *manifestPath == "" || *timeout <= 0 || *poll <= 0 {
		fmt.Fprintln(errOut, "Provide --manifest and positive --timeout and --poll durations.")
		return 2
	}
	manifest, err := readReleaseManifest(*manifestPath)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	apiBase := getenv("QA_API_BASE_URL")
	if apiBase == "" {
		apiBase = "http://127.0.0.1:8080"
	}
	if _, err := releaseOrigin(apiBase); err != nil {
		fmt.Fprintln(errOut, "QA_API_BASE_URL must be an HTTP(S) origin without credentials, query, or path.")
		return 2
	}
	apiToken, githubToken, azurePAT := getenv("QA_API_TOKEN"), getenv("QA_GITHUB_TOKEN"), getenv("QA_AZURE_PAT")
	if !safeReleaseToken(apiToken) || (githubToken != "" && !safeReleaseToken(githubToken)) || (azurePAT != "" && !safeReleaseToken(azurePAT)) {
		fmt.Fprintln(errOut, "Set valid QA_API_TOKEN and optional QA_GITHUB_TOKEN / QA_AZURE_PAT environment values.")
		return 2
	}

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	apiClient := &http.Client{Timeout: 60 * time.Second, CheckRedirect: noRedirect}
	readyClient := &http.Client{Timeout: 10 * time.Second, CheckRedirect: noRedirect}
	endpoint := strings.TrimRight(apiBase, "/") + "/api/projects/" + url.PathEscape(manifest.ProjectID) + "/releases"
	lookup := endpoint + "/by-key/" + url.PathEscape(manifest.DeploymentKey)

	var release qa.Release
	status, body, err := releaseAPIRequest(ctx, apiClient, http.MethodGet, lookup, apiToken, "", "", nil)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	switch status {
	case http.StatusOK:
		if err := json.Unmarshal(body, &release); err != nil || !releaseMatches(release, manifest, nil) {
			fmt.Fprintln(errOut, "Deployment key already has a different or inconsistent release.")
			return 2
		}
		fmt.Fprintf(errOut, "Resuming release %s, run %s.\n", release.ID, release.Run.ID)
	case http.StatusNotFound:
		fmt.Fprintln(errOut, "Waiting for deployment readiness.")
		if err := waitForReadiness(ctx, readyClient, manifest.BaseURL+manifest.ReadinessPath, *poll); err != nil {
			fmt.Fprintln(errOut, "Deployment did not become ready before the deadline; no remote run was created by this attempt.")
			return 2
		}
		imports := make(map[string]qa.RepositorySnapshot, len(manifest.Repositories))
		for _, source := range manifest.Repositories {
			payload, err := json.Marshal(repository.Input{Provider: source.Provider, Repository: source.Repository, Ref: source.CommitSHA, Role: source.Role, Paths: source.Paths})
			if err != nil {
				fmt.Fprintln(errOut, "Could not encode repository import.")
				return 2
			}
			fmt.Fprintf(errOut, "Importing %s at %s.\n", source.Role, source.CommitSHA)
			importEndpoint := strings.TrimRight(apiBase, "/") + "/api/projects/" + url.PathEscape(manifest.ProjectID) + "/repositories/sync"
			repositoryToken := githubToken
			if source.Provider == "azure" {
				repositoryToken = azurePAT
			}
			status, body, err = releaseAPIRequest(ctx, apiClient, http.MethodPost, importEndpoint, apiToken, source.Provider, repositoryToken, payload)
			if err != nil {
				fmt.Fprintln(errOut, err)
				return 2
			}
			if status != http.StatusCreated {
				fmt.Fprintf(errOut, "Repository import returned HTTP %d.\n", status)
				return 2
			}
			var imported qa.RepositorySnapshot
			if err := json.Unmarshal(body, &imported); err != nil || !importMatches(imported, manifest.ProjectID, source) {
				fmt.Fprintln(errOut, "Repository import returned an inconsistent snapshot.")
				return 2
			}
			imports[source.Role] = imported
		}
		repositories := make([]qa.ReleaseRepositoryInput, 0, len(manifest.Repositories))
		for _, source := range manifest.Repositories {
			repositories = append(repositories, qa.ReleaseRepositoryInput{
				SnapshotID: imports[source.Role].ID,
				Provider:   source.Provider,
				Repository: source.Repository,
				Role:       source.Role,
				CommitSHA:  source.CommitSHA,
			})
		}
		payload, err := json.Marshal(qa.ReleaseInput{
			DeploymentKey: manifest.DeploymentKey,
			BaseURL:       manifest.BaseURL,
			Mode:          manifest.Mode,
			ScenarioIDs:   manifest.ScenarioIDs,
			Repositories:  repositories,
		})
		if err != nil {
			fmt.Fprintln(errOut, "Could not encode release request.")
			return 2
		}
		status, body, err = releaseAPIRequest(ctx, apiClient, http.MethodPost, endpoint, apiToken, "", "", payload)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		if status != http.StatusAccepted {
			fmt.Fprintf(errOut, "Release creation returned HTTP %d.\n", status)
			return 2
		}
		if err := json.Unmarshal(body, &release); err != nil || !releaseMatches(release, manifest, imports) {
			fmt.Fprintln(errOut, "Release creation returned an inconsistent release.")
			return 2
		}
		fmt.Fprintf(errOut, "Release %s created, run %s.\n", release.ID, release.Run.ID)
	default:
		fmt.Fprintf(errOut, "Release lookup returned HTTP %d.\n", status)
		return 2
	}

	id, runID := release.ID, release.Run.ID
	observedSources := make(map[string]struct{ id, hash string }, len(release.Repositories))
	for _, source := range release.Repositories {
		observedSources[source.Role] = struct{ id, hash string }{source.SnapshotID, source.ContentSHA256}
	}
	lastStatus := ""
	for {
		if release.ID != id || release.Run.ID != runID || !releaseMatches(release, manifest, nil) || !sameReleaseSources(release.Repositories, observedSources) {
			fmt.Fprintln(errOut, "API returned a different or inconsistent release while polling.")
			return 2
		}
		code, done := releaseExitCode(release.Run, manifest.ScenarioIDs)
		if done && code == 2 {
			fmt.Fprintln(errOut, "API returned an incomplete or inconsistent release decision.")
			return 2
		}
		if release.Run.Status != lastStatus {
			fmt.Fprintf(errOut, "Release %s: %s.\n", id, release.Run.Status)
			lastStatus = release.Run.Status
		}
		if done {
			if *jsonOutput {
				if err := json.NewEncoder(out).Encode(release); err != nil {
					fmt.Fprintln(errOut, "Could not write release JSON.")
					return 2
				}
			} else {
				fmt.Fprintf(out, "Release %s, run %s: %s; gate: %s.\n", id, runID, release.Run.Status, release.Run.Gate)
			}
			return code
		}
		if err := waitReleasePoll(ctx, *poll); err != nil {
			fmt.Fprintln(errOut, "Release wait timed out or was interrupted. The remote run may still be active; retry with the same manifest.")
			return 2
		}
		status, body, err = releaseAPIRequest(ctx, apiClient, http.MethodGet, endpoint+"/"+url.PathEscape(id), apiToken, "", "", nil)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		var next qa.Release
		if status != http.StatusOK || json.Unmarshal(body, &next) != nil {
			fmt.Fprintln(errOut, "Release detail request failed or returned invalid JSON.")
			return 2
		}
		release = next
	}
}

func sameReleaseSources(sources []qa.ReleaseRepository, observed map[string]struct{ id, hash string }) bool {
	if len(sources) != len(observed) {
		return false
	}
	for _, source := range sources {
		if observed[source.Role] != (struct{ id, hash string }{source.SnapshotID, source.ContentSHA256}) {
			return false
		}
	}
	return true
}

func readReleaseManifest(path string) (releaseManifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return releaseManifest{}, errors.New("could not open release manifest")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxReleaseManifest+1))
	if err != nil || len(data) > maxReleaseManifest {
		return releaseManifest{}, errors.New("release manifest is unreadable or exceeds 64 KiB")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var manifest releaseManifest
	if dec.Decode(&manifest) != nil || dec.Decode(new(struct{})) != io.EOF {
		return releaseManifest{}, errors.New("release manifest must contain one strict JSON object")
	}
	if !safeCLIIdentifier(manifest.ProjectID) || !safeDeploymentKey(manifest.DeploymentKey) ||
		(manifest.Mode != "advisory" && manifest.Mode != "blocking") ||
		!safeCLIPath(manifest.ReadinessPath) || strings.Contains(manifest.ReadinessPath, "%") {
		return releaseManifest{}, errors.New("release manifest has an invalid project, deployment key, mode, or readiness path")
	}
	origin, err := releaseOrigin(manifest.BaseURL)
	if err != nil {
		return releaseManifest{}, errors.New("release base_url must be an HTTP(S) origin without credentials, query, or path")
	}
	manifest.BaseURL = origin
	if len(manifest.ScenarioIDs) < 1 || len(manifest.ScenarioIDs) > 50 {
		return releaseManifest{}, errors.New("release must select 1–50 approved scenarios")
	}
	seenScenarios := make(map[string]bool, len(manifest.ScenarioIDs))
	for _, id := range manifest.ScenarioIDs {
		if !safeCLIIdentifier(id) || seenScenarios[id] {
			return releaseManifest{}, errors.New("release scenario IDs must be safe and unique")
		}
		seenScenarios[id] = true
	}
	if len(manifest.Repositories) < 1 || len(manifest.Repositories) > 2 {
		return releaseManifest{}, errors.New("release must select 1–2 repositories")
	}
	seenRoles := make(map[string]bool, len(manifest.Repositories))
	for i := range manifest.Repositories {
		source := &manifest.Repositories[i]
		if (source.Role != "frontend" && source.Role != "backend") || seenRoles[source.Role] ||
			!hexString(source.CommitSHA, 40) || repository.Validate(repository.Input{
			Provider:   source.Provider,
			Repository: source.Repository,
			Ref:        source.CommitSHA,
			Role:       source.Role,
			Paths:      source.Paths,
		}, "") != nil {
			return releaseManifest{}, errors.New("release repositories need distinct roles, valid provider/repository, full commit SHAs, and 1–20 paths")
		}
		seenRoles[source.Role] = true
		source.CommitSHA = strings.ToLower(source.CommitSHA)
		sort.Strings(source.Paths)
	}
	return manifest, nil
}

func releaseOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || u.Opaque != "" ||
		(u.Path != "" && u.Path != "/") || strings.ContainsAny(raw, "\r\n") {
		return "", errors.New("invalid origin")
	}
	return u.Scheme + "://" + u.Host, nil
}

func safeReleaseToken(token string) bool {
	if token == "" || len(token) > 4096 {
		return false
	}
	for _, b := range []byte(token) {
		if b < '!' || b > '~' {
			return false
		}
	}
	return true
}

func safeDeploymentKey(key string) bool {
	if len(key) < 1 || len(key) > 200 {
		return false
	}
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func hexString(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

func releaseAPIRequest(ctx context.Context, client *http.Client, method, endpoint, apiToken, provider, repositoryToken string, payload []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, errors.New("could not construct release API request")
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	if repositoryToken != "" {
		header := "X-QA-GitHub-Token"
		if provider == "azure" {
			header = "X-QA-Azure-PAT"
		}
		req.Header.Set(header, repositoryToken)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, errors.New("release API request failed; check connectivity, authentication, and the timeout")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return 0, nil, errors.New("release API response unreadable or too large")
	}
	return resp.StatusCode, data, nil
}

func waitForReadiness(ctx context.Context, client *http.Client, endpoint string, poll time.Duration) error {
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return errors.New("could not construct readiness request")
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if err := waitReleasePoll(ctx, poll); err != nil {
			return err
		}
	}
}

func waitReleasePoll(ctx context.Context, poll time.Duration) error {
	timer := time.NewTimer(poll)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func importMatches(imported qa.RepositorySnapshot, projectID string, source releaseManifestSource) bool {
	if !safeCLIIdentifier(imported.ID) || imported.ProjectID != projectID || repository.EffectiveProvider(imported.Provider) != repository.EffectiveProvider(source.Provider) || imported.Repository != source.Repository ||
		imported.Role != source.Role || !strings.EqualFold(imported.CommitSHA, source.CommitSHA) ||
		!hexString(imported.ContentSHA256, 64) || len(imported.Files) != len(source.Paths) {
		return false
	}
	paths := make([]string, 0, len(imported.Files))
	for _, file := range imported.Files {
		paths = append(paths, file.Path)
	}
	sort.Strings(paths)
	for i, path := range paths {
		if path != source.Paths[i] {
			return false
		}
	}
	return true
}

func releaseMatches(release qa.Release, manifest releaseManifest, imports map[string]qa.RepositorySnapshot) bool {
	if !safeCLIIdentifier(release.ID) || release.CreatedAt.IsZero() || release.ProjectID != manifest.ProjectID || release.DeploymentKey != manifest.DeploymentKey ||
		release.BaseURL != manifest.BaseURL || release.Mode != manifest.Mode || len(release.Repositories) != len(manifest.Repositories) ||
		!safeCLIIdentifier(release.Run.ID) || release.Run.CreatedAt.IsZero() || release.Run.ProjectID != manifest.ProjectID || release.Run.BaseURL != manifest.BaseURL ||
		release.Run.Mode != manifest.Mode || len(release.Run.Scenarios) != len(manifest.ScenarioIDs) {
		return false
	}
	for i, scenario := range release.Run.Scenarios {
		if scenario.ID != manifest.ScenarioIDs[i] || scenario.ProjectID != manifest.ProjectID || !scenario.Approved {
			return false
		}
	}
	byRole := make(map[string]qa.ReleaseRepository, len(release.Repositories))
	for _, source := range release.Repositories {
		if !safeCLIIdentifier(source.SnapshotID) || !hexString(source.ContentSHA256, 64) || byRole[source.Role].Role != "" {
			return false
		}
		byRole[source.Role] = source
	}
	for _, expected := range manifest.Repositories {
		actual := byRole[expected.Role]
		if actual.Role != expected.Role || repository.EffectiveProvider(actual.Provider) != repository.EffectiveProvider(expected.Provider) || actual.Repository != expected.Repository || !strings.EqualFold(actual.CommitSHA, expected.CommitSHA) || len(actual.Paths) != len(expected.Paths) {
			return false
		}
		for i, path := range actual.Paths {
			if path != expected.Paths[i] {
				return false
			}
		}
		if imports != nil {
			imported := imports[expected.Role]
			if actual.ContentSHA256 != imported.ContentSHA256 {
				return false
			}
		}
	}
	return true
}

func releaseExitCode(run qa.Run, requested []string) (int, bool) {
	if (run.Status == "queued" || run.Status == "running") && len(run.Results) != 0 {
		return 2, true
	}
	response := runResponse{ID: run.ID, Status: run.Status, Mode: run.Mode, Gate: run.Gate}
	for _, result := range run.Results {
		response.Results = append(response.Results, struct {
			ScenarioID string `json:"scenario_id"`
			Status     string `json:"status"`
			Message    string `json:"message"`
		}{result.ScenarioID, result.Status, result.Message})
	}
	code, done := exitCode(response, requested)
	if !done || code == 2 {
		return code, done
	}
	if run.Status == "passed" {
		return code, true
	}
	if run.Status == "failed" || run.Status == "blocked" {
		status, err := qa.ValidateResults(run.Scenarios, run.Results)
		if err != nil || status != run.Status {
			return 2, true
		}
	}
	if run.Status == "error" && len(run.Results) > 0 {
		status, err := qa.ValidateResults(run.Scenarios, run.Results)
		if err != nil || status != "error" {
			return 2, true
		}
	}
	if run.Status == "cancelled" && len(run.Results) != 0 {
		return 2, true
	}
	return code, true
}
