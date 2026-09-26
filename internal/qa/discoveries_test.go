package qa_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
)

func discoveryResult() *qa.DiscoveryResult {
	return &qa.DiscoveryResult{
		Pages: []qa.DiscoveryPage{{
			Path: "/dashboard", Title: "Dashboard", Headings: []string{"Revenue"},
			Elements: []qa.DiscoveryElement{{TestID: "revenue", Tag: "span", Text: "140000"}},
			Links:    []qa.DiscoveryLink{{Path: "/details", Text: "Details"}},
		}},
		Warnings: []string{},
	}
}

func TestDiscoveryObservationBudget(t *testing.T) {
	result := discoveryResult()
	result.Pages[0].Title = strings.Repeat("é", 200)
	if err := qa.ValidateDiscoveryResult(*result, 1); err != nil {
		t.Fatalf("valid Unicode title: %v", err)
	}
	result.Pages[0].Elements = []qa.DiscoveryElement{}
	result.Pages[0].Links = []qa.DiscoveryLink{}
	for range 80 {
		result.Pages[0].Elements = append(result.Pages[0].Elements, qa.DiscoveryElement{Tag: "span", Text: strings.Repeat("x", 300)})
		result.Pages[0].Links = append(result.Pages[0].Links, qa.DiscoveryLink{Path: "/details", Text: strings.Repeat("y", 300)})
	}
	if err := qa.ValidateDiscoveryResult(*result, 1); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("aggregate observation budget: %v", err)
	}
}

func TestDiscoveryLifecycleAndIsolation(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	first, err := store.CreateProject(ctx, qa.ProjectInput{Name: "first", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateProject(ctx, qa.ProjectInput{Name: "second", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	approved := approvedScenario(t, store, first.ID)
	unapproved, err := store.CreateScenario(ctx, first.ID, qa.ScenarioInput{
		Name: "draft", ExpectedOutcome: "visible", Steps: []qa.Step{{Action: "assert_visible", TestID: "draft"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []qa.DiscoveryInput{
		{StartPath: "//host", MaxPages: 1},
		{StartPath: "/dashboard?token=x", MaxPages: 1},
		{StartPath: "/", MaxPages: 6},
		{StartPath: "/", MaxPages: 1, SetupScenarioID: unapproved.ID},
	} {
		if _, err := store.CreateDiscovery(ctx, first.ID, input); !errors.Is(err, qa.ErrInvalid) {
			t.Fatalf("invalid discovery %+v: %v", input, err)
		}
	}
	if _, err := store.CreateDiscovery(ctx, second.ID, qa.DiscoveryInput{StartPath: "/", MaxPages: 1, SetupScenarioID: approved.ID}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("cross-project setup: %v", err)
	}
	queued, err := store.CreateDiscovery(ctx, first.ID, qa.DiscoveryInput{StartPath: "/dashboard", MaxPages: 2, SetupScenarioID: approved.ID})
	if err != nil || queued.Status != "queued" || queued.SetupScenario == nil || queued.SetupScenario.ID != approved.ID || queued.Result != nil {
		t.Fatalf("queued discovery: %+v %v", queued, err)
	}
	if _, err := store.SelectDiscovery(ctx, first.ID, queued.ID); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("queued proposal selection: %v", err)
	}
	claimed, token, expiry, found, err := store.ClaimDiscovery(ctx, "worker")
	if err != nil || !found || claimed.ID != queued.ID || claimed.Status != "running" || token == "" || expiry.Before(time.Now()) {
		t.Fatalf("claim: %+v %q %v %v %v", claimed, token, expiry, found, err)
	}
	if _, err := store.HeartbeatDiscovery(ctx, queued.ID, "wrong"); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("wrong heartbeat: %v", err)
	}
	if _, err := store.HeartbeatDiscovery(ctx, queued.ID, token); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	invalid := discoveryResult()
	invalid.Pages[0].Elements = make([]qa.DiscoveryElement, 81)
	if _, err := store.CompleteDiscovery(ctx, queued.ID, qa.CompleteDiscoveryInput{LeaseToken: token, Result: invalid}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("oversized result: %v", err)
	}
	if _, err := store.CompleteDiscovery(ctx, queued.ID, qa.CompleteDiscoveryInput{LeaseToken: token, Result: discoveryResult(), Error: "also error"}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("ambiguous result: %v", err)
	}
	if _, err := store.CompleteDiscovery(ctx, queued.ID, qa.CompleteDiscoveryInput{LeaseToken: "wrong", Result: discoveryResult()}); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("wrong completion token: %v", err)
	}
	completed, err := store.CompleteDiscovery(ctx, queued.ID, qa.CompleteDiscoveryInput{LeaseToken: token, Result: discoveryResult()})
	if err != nil || completed.Status != "completed" || completed.Result == nil || completed.Result.Pages[0].Path != "/dashboard" || completed.FinishedAt == nil {
		t.Fatalf("completed: %+v %v", completed, err)
	}
	if _, err := store.SelectDiscovery(ctx, second.ID, queued.ID); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("cross-project proposal selection: %v", err)
	}
	if _, err := store.SelectDiscovery(ctx, first.ID, queued.ID); err != nil {
		t.Fatalf("completed proposal selection: %v", err)
	}
	if _, err := store.CancelDiscovery(ctx, queued.ID); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("terminal cancellation: %v", err)
	}
	listed, err := store.ListDiscoveries(ctx, first.ID)
	if err != nil || len(listed) != 1 || listed[0].Status != "completed" {
		t.Fatalf("list: %+v %v", listed, err)
	}
}

func TestDiscoveryCancellationExpiryAndError(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "pilot", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := store.CreateDiscovery(ctx, project.ID, qa.DiscoveryInput{StartPath: "/", MaxPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := store.CancelDiscovery(ctx, queued.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("queued cancellation: %+v %v", cancelled, err)
	}
	expiring, err := store.CreateDiscovery(ctx, project.ID, qa.DiscoveryInput{StartPath: "/", MaxPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, token, _, found, err := store.ClaimDiscovery(ctx, "worker")
	if err != nil || !found {
		t.Fatalf("claim expiring: %v %v", found, err)
	}
	if _, err := db.Exec(ctx, `UPDATE discoveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, expiring.ID); err != nil {
		t.Fatal(err)
	}
	expired, err := store.GetDiscovery(ctx, expiring.ID)
	if err != nil || expired.Status != "error" || expired.Error == "" || expired.Result != nil {
		t.Fatalf("expired discovery: %+v %v", expired, err)
	}
	if _, err := store.CompleteDiscovery(ctx, expiring.ID, qa.CompleteDiscoveryInput{LeaseToken: token, Result: discoveryResult()}); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("expired completion: %v", err)
	}
	invalid := discoveryResult()
	invalid.Pages[0].Path = "https://other.test/"
	if _, err := store.CompleteDiscovery(ctx, expiring.ID, qa.CompleteDiscoveryInput{LeaseToken: token, Result: invalid}); !errors.Is(err, qa.ErrConflict) {
		t.Fatalf("expired lease should take precedence over result shape: %v", err)
	}
	if _, _, _, found, err := store.ClaimDiscovery(ctx, "worker"); err != nil || found {
		t.Fatalf("expired work was replayed: %v %v", found, err)
	}
	failing, err := store.CreateDiscovery(ctx, project.ID, qa.DiscoveryInput{StartPath: "/", MaxPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, failureToken, _, _, err := store.ClaimDiscovery(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	failed, err := store.CompleteDiscovery(ctx, failing.ID, qa.CompleteDiscoveryInput{LeaseToken: failureToken, Error: "Browser navigation failed"})
	if err != nil || failed.Status != "error" || failed.Result != nil || failed.Error != "Browser navigation failed" {
		t.Fatalf("error completion: %+v %v", failed, err)
	}
	if _, err := store.SelectDiscovery(ctx, project.ID, failing.ID); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("failed proposal selection: %v", err)
	}
	if _, err := store.CreateDiscovery(ctx, project.ID, qa.DiscoveryInput{StartPath: strings.Repeat("/", 2049), MaxPages: 1}); !errors.Is(err, qa.ErrInvalid) {
		t.Fatalf("overlong path: %v", err)
	}
}

func TestConcurrentDiscoveryClaimsAreUnique(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	project, err := store.CreateProject(ctx, qa.ProjectInput{Name: "pilot", BaseURL: "http://localhost:4174"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := store.CreateDiscovery(ctx, project.ID, qa.DiscoveryInput{StartPath: "/", MaxPages: 1}); err != nil {
			t.Fatal(err)
		}
	}
	type claim struct {
		id    string
		found bool
		err   error
	}
	claims := make(chan claim, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			discovery, _, _, found, err := store.ClaimDiscovery(ctx, "worker")
			claims <- claim{id: discovery.ID, found: found, err: err}
		}()
	}
	wg.Wait()
	close(claims)
	seen := map[string]bool{}
	for claimed := range claims {
		if claimed.err != nil || !claimed.found || claimed.id == "" || seen[claimed.id] {
			t.Fatalf("duplicate or missing claim: %+v", claimed)
		}
		seen[claimed.id] = true
	}
	if len(seen) != 2 {
		t.Fatalf("claimed %d discoveries", len(seen))
	}
}
