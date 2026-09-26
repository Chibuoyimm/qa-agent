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
	"strings"
	"time"
	"unicode/utf8"
)

type discoveryResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	StartPath string `json:"start_path"`
	MaxPages  int    `json:"max_pages"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	Result    *struct {
		Pages []struct {
			Path     string   `json:"path"`
			Title    string   `json:"title"`
			Headings []string `json:"headings"`
			Elements []struct {
				TestID string `json:"test_id"`
			} `json:"elements"`
			Links []struct {
				Path string `json:"path"`
			} `json:"links"`
			Truncated bool `json:"truncated"`
		} `json:"pages"`
		Limited  bool     `json:"limited"`
		Warnings []string `json:"warnings"`
	} `json:"result"`
	raw []byte
}

func executeDiscovery(ctx context.Context, args []string, getenv func(string) string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("qa discover", flag.ContinueOnError)
	flags.SetOutput(errOut)
	project := flags.String("project", "", "Project ID")
	startPath := flags.String("start-path", "/", "First page path")
	maxPages := flags.Int("max-pages", 3, "Maximum pages, 1–5")
	setupScenario := flags.String("setup-scenario", "", "Approved setup scenario ID")
	timeout := flags.Duration("timeout", 2*time.Minute, "Maximum wait for discovery")
	poll := flags.Duration("poll", time.Second, "Polling interval")
	jsonOutput := flags.Bool("json", false, "Print the full terminal discovery JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || !safeCLIIdentifier(*project) || (*setupScenario != "" && !safeCLIIdentifier(*setupScenario)) || !safeCLIPath(*startPath) || *maxPages < 1 || *maxPages > 5 || *timeout <= 0 || *poll <= 0 {
		fmt.Fprintln(errOut, "Provide a valid project, page path, optional setup scenario, 1–5 pages, and positive timeout/poll durations.")
		return 2
	}
	base := getenv("QA_API_BASE_URL")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		fmt.Fprintln(errOut, "QA_API_BASE_URL must be an HTTP(S) origin without credentials, query, or path.")
		return 2
	}
	token := getenv("QA_API_TOKEN")
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, "\r\n") {
		fmt.Fprintln(errOut, "Set a valid QA_API_TOKEN in the environment.")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	payload, _ := json.Marshal(struct {
		StartPath       string `json:"start_path"`
		MaxPages        int    `json:"max_pages"`
		SetupScenarioID string `json:"setup_scenario_id,omitempty"`
	}{*startPath, *maxPages, *setupScenario})
	endpoint := strings.TrimRight(base, "/") + "/api/projects/" + url.PathEscape(*project) + "/discoveries"
	job, err := discoveryRequest(ctx, client, endpoint, token, payload)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	if !safeCLIIdentifier(job.ID) || job.ProjectID != *project || job.StartPath != *startPath || job.MaxPages != *maxPages {
		fmt.Fprintln(errOut, "API returned an inconsistent discovery job.")
		return 2
	}
	id := job.ID
	lastStatus := ""
	for {
		if job.ID != id || job.ProjectID != *project || job.StartPath != *startPath || job.MaxPages != *maxPages || len(job.Error) > 500 {
			fmt.Fprintln(errOut, "API returned a different discovery job.")
			return 2
		}
		if job.Status != lastStatus {
			progress := out
			if *jsonOutput {
				progress = errOut
			}
			fmt.Fprintf(progress, "Discovery %s: %s\n", id, job.Status)
			lastStatus = job.Status
		}
		switch job.Status {
		case "completed":
			if job.Result == nil || len(job.Result.Pages) == 0 || len(job.Result.Pages) > *maxPages || job.Error != "" {
				fmt.Fprintln(errOut, "API returned an incomplete discovery result.")
				return 2
			}
			seen := make(map[string]bool, len(job.Result.Pages))
			for _, page := range job.Result.Pages {
				if !safeCLIPath(page.Path) || seen[page.Path] {
					fmt.Fprintln(errOut, "API returned an invalid discovery page.")
					return 2
				}
				seen[page.Path] = true
			}
			printDiscovery(out, job, *jsonOutput)
			return 0
		case "error", "cancelled":
			if job.Result != nil {
				fmt.Fprintln(errOut, "API returned an inconsistent terminal discovery job.")
				return 2
			}
			printDiscovery(out, job, *jsonOutput)
			return 1
		case "queued", "running":
			if job.Result != nil || job.Error != "" {
				fmt.Fprintln(errOut, "API returned an inconsistent active discovery job.")
				return 2
			}
		default:
			fmt.Fprintln(errOut, "API returned an unknown discovery status.")
			return 2
		}
		timer := time.NewTimer(*poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			fmt.Fprintf(errOut, "Stopped waiting for discovery %s: %v. The remote job may still be active.\n", id, ctx.Err())
			return 2
		case <-timer.C:
		}
		job, err = discoveryRequest(ctx, client, strings.TrimRight(base, "/")+"/api/discoveries/"+url.PathEscape(id), token, nil)
		if err != nil {
			if ctx.Err() != nil {
				fmt.Fprintf(errOut, "Stopped waiting for discovery %s: %v. The remote job may still be active.\n", id, ctx.Err())
				return 2
			}
			fmt.Fprintln(errOut, err)
			return 2
		}
	}
}

func safeCLIIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 200 || !(value[0] >= 'a' && value[0] <= 'z' || value[0] >= 'A' && value[0] <= 'Z' || value[0] >= '0' && value[0] <= '9') {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func safeCLIPath(value string) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 2048 || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.Contains(value, "..") || strings.ContainsAny(value, "?#\\") {
		return false
	}
	for _, r := range value {
		if r <= 0x20 {
			return false
		}
	}
	return true
}

func discoveryRequest(ctx context.Context, client *http.Client, endpoint, token string, payload []byte) (discoveryResponse, error) {
	method, expected := http.MethodGet, http.StatusOK
	if payload != nil {
		method, expected = http.MethodPost, http.StatusAccepted
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return discoveryResponse{}, errors.New("could not construct discovery API request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return discoveryResponse{}, errors.New("discovery API request failed; check connectivity, authentication, and the timeout")
	}
	defer resp.Body.Close()
	if resp.StatusCode != expected {
		return discoveryResponse{}, fmt.Errorf("discovery API returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return discoveryResponse{}, errors.New("discovery API response unreadable or too large")
	}
	var job discoveryResponse
	if err := json.Unmarshal(body, &job); err != nil {
		return discoveryResponse{}, errors.New("discovery API returned invalid JSON")
	}
	job.raw = body
	return job, nil
}

func printDiscovery(out io.Writer, job discoveryResponse, jsonOutput bool) {
	if jsonOutput {
		out.Write(job.raw)
		fmt.Fprintln(out)
		return
	}
	if job.Status != "completed" {
		fmt.Fprintf(out, "Discovery %s %s", job.ID, job.Status)
		if job.Error != "" {
			fmt.Fprintf(out, ": %s", job.Error)
		}
		fmt.Fprintln(out)
		return
	}
	fmt.Fprintf(out, "Discovery %s completed: %d page(s).\n", job.ID, len(job.Result.Pages))
	for _, page := range job.Result.Pages {
		fmt.Fprintf(out, "%s — %s (%d controls, %d links)\n", page.Path, page.Title, len(page.Elements), len(page.Links))
		for i, heading := range page.Headings {
			if i == 3 {
				break
			}
			fmt.Fprintf(out, "  heading: %s\n", heading)
		}
		shown := 0
		for _, element := range page.Elements {
			if element.TestID == "" {
				continue
			}
			fmt.Fprintf(out, "  test id: %s\n", element.TestID)
			shown++
			if shown == 5 {
				break
			}
		}
	}
	if job.Result.Limited {
		fmt.Fprintln(out, "Observation was limited.")
	}
	for _, warning := range job.Result.Warnings {
		fmt.Fprintf(out, "Warning: %s\n", warning)
	}
}
