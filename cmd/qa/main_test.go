package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReleaseDecision(t *testing.T) {
	for _, tc := range []struct {
		status, mode, gate string
		want               int
	}{
		{"failed", "blocking", "fail", 1}, {"blocked", "blocking", "fail", 1},
		{"error", "advisory", "warn", 0}, {"cancelled", "blocking", "fail", 1},
		{"failed", "blocking", "pass", 2}, {"passed", "blocking", "pass", 2},
		{"unknown", "advisory", "warn", 2},
	} {
		t.Run(tc.status+tc.mode+tc.gate, func(t *testing.T) {
			code, done := exitCode(runResponse{Status: tc.status, Mode: tc.mode, Gate: tc.gate}, []string{"s1"})
			if !done || code != tc.want {
				t.Fatalf("got (%d,%v), want (%d,true)", code, done, tc.want)
			}
		})
	}
}

func TestCLIWaitsForCompleteResults(t *testing.T) {
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing auth")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			if r.URL.Path != "/api/projects/p1/runs" {
				t.Error(r.URL.Path)
			}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"id":"r1","status":"queued","mode":"blocking","gate":"pending"}`)
			return
		}
		gets++
		fmt.Fprint(w, `{"id":"r1","status":"passed","mode":"blocking","gate":"pass","results":[{"scenario_id":"s1","status":"passed","message":"Revenue is 140000"}]}`)
	}))
	defer server.Close()
	var out, errOut bytes.Buffer
	getenv := func(key string) string {
		if key == "QA_API_TOKEN" {
			return "test-token"
		}
		return server.URL
	}
	code := execute(context.Background(), []string{"run", "--project", "p1", "--scenarios", "s1", "--mode", "blocking", "--poll", "1ms"}, getenv, &out, &errOut)
	if code != 0 || gets != 1 {
		t.Fatalf("code=%d gets=%d stderr=%s", code, gets, errOut.String())
	}
}

func TestCLIRejectsRedirects(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	var out bytes.Buffer
	getenv := func(key string) string {
		if key == "QA_API_TOKEN" {
			return "test-token"
		}
		return server.URL
	}
	if got := execute(context.Background(), []string{"run", "--project", "p1", "--scenarios", "s1"}, getenv, &out, &out); got != 2 || forwarded {
		t.Fatalf("code=%d forwarded=%v", got, forwarded)
	}
}
