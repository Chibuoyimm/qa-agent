// qa launches an approved suite and waits for its release decision.
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
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type runResponse struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Mode    string `json:"mode"`
	Gate    string `json:"gate"`
	Results []struct {
		ScenarioID string `json:"scenario_id"`
		Status     string `json:"status"`
		Message    string `json:"message"`
	} `json:"results"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(execute(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func execute(ctx context.Context, args []string, getenv func(string) string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] != "run" {
		fmt.Fprintln(errOut, "Usage: qa run --project ID --scenarios ID,ID [--mode advisory|blocking] [--timeout 5m]")
		return 2
	}
	flags := flag.NewFlagSet("qa run", flag.ContinueOnError)
	flags.SetOutput(errOut)
	project := flags.String("project", "", "Project ID")
	scenarios := flags.String("scenarios", "", "Comma-separated approved scenario IDs")
	mode := flags.String("mode", "advisory", "Release mode: advisory or blocking")
	timeout := flags.Duration("timeout", 5*time.Minute, "Maximum wait for the run")
	poll := flags.Duration("poll", time.Second, "Polling interval")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*project) == "" || *scenarios == "" || (*mode != "advisory" && *mode != "blocking") || *timeout <= 0 || *poll <= 0 {
		fmt.Fprintln(errOut, "Provide a project, scenario IDs, valid mode, and positive timeout/poll durations.")
		return 2
	}
	ids := strings.Split(*scenarios, ",")
	seen := make(map[string]bool, len(ids))
	for i := range ids {
		ids[i] = strings.TrimSpace(ids[i])
		if ids[i] == "" || seen[ids[i]] {
			fmt.Fprintln(errOut, "Scenario IDs must be nonempty and unique.")
			return 2
		}
		seen[ids[i]] = true
	}
	base := getenv("QA_API_BASE_URL")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		fmt.Fprintln(errOut, "QA_API_BASE_URL must be an HTTP(S) origin without credentials, query, or path.")
		return 2
	}
	token := getenv("QA_API_TOKEN")
	if token == "" {
		fmt.Fprintln(errOut, "Set QA_API_TOKEN in the environment.")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	payload, _ := json.Marshal(struct {
		ScenarioIDs []string `json:"scenario_ids"`
		Mode        string   `json:"mode"`
	}{ids, *mode})
	run, err := request(ctx, client, strings.TrimRight(base, "/")+"/api/projects/"+url.PathEscape(*project)+"/runs", token, payload)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	if run.ID == "" {
		fmt.Fprintln(errOut, "API response omitted the run ID.")
		return 2
	}
	id := run.ID
	fmt.Fprintf(out, "Run %s started (%s).\n", id, *mode)
	for {
		if run.ID != id || run.Mode != *mode {
			fmt.Fprintln(errOut, "API returned a different run or release mode.")
			return 2
		}
		code, done := exitCode(run, ids)
		if done {
			for _, result := range run.Results {
				fmt.Fprintf(out, "%s: %s — %s\n", result.ScenarioID, result.Status, result.Message)
			}
			fmt.Fprintf(out, "Run %s: %s; release decision: %s\n", id, run.Status, run.Gate)
			if code == 2 {
				fmt.Fprintln(errOut, "API returned an incomplete or inconsistent release decision.")
			}
			return code
		}
		timer := time.NewTimer(*poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			fmt.Fprintf(errOut, "Stopped waiting for run %s: %v. The remote run may still be active.\n", id, ctx.Err())
			return 2
		case <-timer.C:
		}
		run, err = request(ctx, client, strings.TrimRight(base, "/")+"/api/runs/"+url.PathEscape(id), token, nil)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
	}
}

func request(ctx context.Context, client *http.Client, endpoint, token string, payload []byte) (runResponse, error) {
	method, expected := http.MethodGet, http.StatusOK
	if payload != nil {
		method, expected = http.MethodPost, http.StatusAccepted
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return runResponse{}, errors.New("could not construct API request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return runResponse{}, errors.New("API request failed; check connectivity, authentication, and the run timeout")
	}
	defer resp.Body.Close()
	if resp.StatusCode != expected {
		return runResponse{}, fmt.Errorf("API returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(body) > 4<<20 {
		return runResponse{}, errors.New("API response unreadable or too large")
	}
	var run runResponse
	if err := json.Unmarshal(body, &run); err != nil {
		return runResponse{}, errors.New("API returned invalid run JSON")
	}
	return run, nil
}

func exitCode(run runResponse, requested []string) (int, bool) {
	switch run.Status {
	case "queued", "running":
		if run.Gate != "pending" {
			return 2, true
		}
		return 0, false
	case "passed":
		if run.Gate != "pass" || len(run.Results) != len(requested) {
			return 2, true
		}
		seen := make(map[string]bool, len(requested))
		for _, id := range requested {
			seen[id] = false
		}
		for _, result := range run.Results {
			done, exists := seen[result.ScenarioID]
			if !exists || done || result.Status != "passed" {
				return 2, true
			}
			seen[result.ScenarioID] = true
		}
		return 0, true
	case "failed", "blocked", "error", "cancelled":
		if run.Mode == "advisory" && run.Gate == "warn" {
			return 0, true
		}
		if run.Mode == "blocking" && run.Gate == "fail" {
			return 1, true
		}
		return 2, true
	default:
		return 2, true
	}
}
