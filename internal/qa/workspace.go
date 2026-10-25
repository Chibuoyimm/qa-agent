package qa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Journey struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}
type TaskApproval struct {
	TurnID      string   `json:"turn_id"`
	ScenarioIDs []string `json:"scenario_ids"`
}
type TestingTask struct {
	ID          string         `json:"id"`
	ProjectID   string         `json:"project_id"`
	JourneyID   string         `json:"journey_id"`
	Title       string         `json:"title"`
	Request     string         `json:"request"`
	DiscoveryID string         `json:"discovery_id,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Approvals   []TaskApproval `json:"approvals"`
}
type TaskInput struct {
	RequestID   string `json:"request_id"`
	Prompt      string `json:"prompt"`
	Title       string `json:"title"`
	JourneyID   string `json:"journey_id,omitempty"`
	JourneyName string `json:"journey_name,omitempty"`
}

const taskColumns = `id,project_id,journey_id,title,request,COALESCE(discovery_id,''),created_at`

func scanTask(row rowScanner) (TestingTask, error) {
	t := TestingTask{Approvals: []TaskApproval{}}
	err := row.Scan(&t.ID, &t.ProjectID, &t.JourneyID, &t.Title, &t.Request, &t.DiscoveryID, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}
func (s *Store) CreateJourney(ctx context.Context, projectID, name string) (Journey, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return Journey{}, fmt.Errorf("%w: journey name must be 1–200 bytes", ErrInvalid)
	}
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return Journey{}, err
	}
	id, err := newID()
	if err != nil {
		return Journey{}, err
	}
	var j Journey
	err = s.db.QueryRow(ctx, `INSERT INTO journeys(id,project_id,name) VALUES($1,$2,$3) ON CONFLICT(project_id,lower(name)) DO UPDATE SET name=journeys.name RETURNING id,project_id,name,created_at`, id, projectID, name).Scan(&j.ID, &j.ProjectID, &j.Name, &j.CreatedAt)
	return j, err
}
func (s *Store) ListJourneys(ctx context.Context, projectID string) ([]Journey, error) {
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT id,project_id,name,created_at FROM journeys WHERE project_id=$1 ORDER BY created_at,id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Journey{}
	for rows.Next() {
		var j Journey
		if err := rows.Scan(&j.ID, &j.ProjectID, &j.Name, &j.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, j)
	}
	return list, rows.Err()
}
func (s *Store) CreateTask(ctx context.Context, projectID string, in TaskInput) (TestingTask, error) {
	if err := ValidateChatRequest(in.RequestID, in.Prompt); err != nil {
		return TestingTask{}, err
	}
	in.Title = strings.TrimSpace(in.Title)
	in.JourneyName = strings.TrimSpace(in.JourneyName)
	if in.Title == "" || len(in.Title) > 200 || len(in.JourneyName) > 200 || (in.JourneyID == "" && in.JourneyName == "") {
		return TestingTask{}, fmt.Errorf("%w: provide a task title and journey", ErrInvalid)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return TestingTask{}, err
	}
	sum := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(sum[:])
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return TestingTask{}, err
	}
	defer tx.Rollback(ctx)
	var exists string
	if err := tx.QueryRow(ctx, `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, projectID).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TestingTask{}, ErrNotFound
		}
		return TestingTask{}, err
	}
	var previous string
	err = tx.QueryRow(ctx, `SELECT request_sha256 FROM testing_tasks WHERE id=$1 AND project_id=$2`, in.RequestID, projectID).Scan(&previous)
	if err == nil {
		if previous != fingerprint {
			return TestingTask{}, ErrConflict
		}
		return scanTask(tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM testing_tasks WHERE id=$1`, in.RequestID))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return TestingTask{}, err
	}
	journeyID := in.JourneyID
	if journeyID != "" {
		if err := tx.QueryRow(ctx, `SELECT id FROM journeys WHERE id=$1 AND project_id=$2`, journeyID, projectID).Scan(&exists); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return TestingTask{}, ErrInvalid
			}
			return TestingTask{}, err
		}
	} else {
		id, err := newID()
		if err != nil {
			return TestingTask{}, err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO journeys(id,project_id,name) VALUES($1,$2,$3) ON CONFLICT(project_id,lower(name)) DO UPDATE SET name=journeys.name RETURNING id`, id, projectID, in.JourneyName).Scan(&journeyID); err != nil {
			return TestingTask{}, err
		}
	}
	task, err := scanTask(tx.QueryRow(ctx, `INSERT INTO testing_tasks(id,project_id,journey_id,title,request,request_sha256) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+taskColumns, in.RequestID, projectID, journeyID, in.Title, in.Prompt, fingerprint))
	if err != nil {
		return TestingTask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TestingTask{}, err
	}
	return task, nil
}
func (s *Store) ListTasks(ctx context.Context, projectID string) ([]TestingTask, error) {
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT `+taskColumns+` FROM testing_tasks WHERE project_id=$1 ORDER BY created_at DESC,id DESC LIMIT 100`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []TestingTask{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}
func (s *Store) GetTask(ctx context.Context, projectID, id string) (TestingTask, error) {
	t, err := scanTask(s.db.QueryRow(ctx, `SELECT `+taskColumns+` FROM testing_tasks WHERE id=$1 AND project_id=$2`, id, projectID))
	if err != nil {
		return t, err
	}
	rows, err := s.db.Query(ctx, `SELECT turn_id,scenario_ids FROM task_approvals WHERE task_id=$1 ORDER BY created_at`, id)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		var a TaskApproval
		var raw []byte
		if err := rows.Scan(&a.TurnID, &raw); err != nil {
			return t, err
		}
		if err := json.Unmarshal(raw, &a.ScenarioIDs); err != nil {
			return t, err
		}
		t.Approvals = append(t.Approvals, a)
	}
	return t, rows.Err()
}

type TaskDiscoveryInput struct {
	DiscoveryInput
	Refresh bool `json:"refresh"`
}

func (s *Store) DiscoverTask(ctx context.Context, projectID, id string, in TaskDiscoveryInput) (Discovery, error) {
	if !SafeDiscoveryPath(in.StartPath) || in.MaxPages < 1 || in.MaxPages > 5 {
		return Discovery{}, fmt.Errorf("%w: choose a start path and 1–5 pages", ErrInvalid)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Discovery{}, err
	}
	defer tx.Rollback(ctx)
	task, err := scanTask(tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM testing_tasks WHERE id=$1 AND project_id=$2 FOR UPDATE`, id, projectID))
	if err != nil {
		return Discovery{}, err
	}
	if task.DiscoveryID != "" {
		previous, err := scanDiscovery(tx.QueryRow(ctx, `SELECT `+discoveryColumns+` FROM discoveries WHERE id=$1`, task.DiscoveryID))
		if err != nil {
			return Discovery{}, err
		}
		if !in.Refresh || previous.Status == "queued" || previous.Status == "running" {
			return previous, nil
		}
	}
	var baseURL string
	if err := tx.QueryRow(ctx, `SELECT base_url FROM projects WHERE id=$1`, projectID).Scan(&baseURL); err != nil {
		return Discovery{}, err
	}
	var setup []byte
	if in.SetupScenarioID != "" {
		var scenario Scenario
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT id,project_id,name,description,expected_outcome,approved,steps,created_at FROM scenarios WHERE id=$1 AND project_id=$2`, in.SetupScenarioID, projectID).Scan(&scenario.ID, &scenario.ProjectID, &scenario.Name, &scenario.Description, &scenario.ExpectedOutcome, &scenario.Approved, &raw, &scenario.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return Discovery{}, ErrInvalid
		}
		if err != nil {
			return Discovery{}, err
		}
		if !scenario.Approved {
			return Discovery{}, fmt.Errorf("%w: test access setup must be approved", ErrInvalid)
		}
		if err := json.Unmarshal(raw, &scenario.Steps); err != nil {
			return Discovery{}, err
		}
		setup, err = json.Marshal(scenario)
		if err != nil {
			return Discovery{}, err
		}
	}
	discoveryID, err := newID()
	if err != nil {
		return Discovery{}, err
	}
	discovery, err := scanDiscovery(tx.QueryRow(ctx, `INSERT INTO discoveries(id,project_id,base_url,start_path,max_pages,setup_scenario,status) VALUES($1,$2,$3,$4,$5,$6,'queued') RETURNING `+discoveryColumns, discoveryID, projectID, baseURL, in.StartPath, in.MaxPages, setup))
	if err != nil {
		return Discovery{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE testing_tasks SET discovery_id=$2 WHERE id=$1`, id, discoveryID); err != nil {
		return Discovery{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Discovery{}, err
	}
	return discovery, nil
}

type assertionKey struct {
	action, target, value string
	locator               ElementLocator
}

func assertionKeys(steps []Step) []assertionKey {
	keys := []assertionKey{}
	for _, step := range steps {
		if step.Action != "assert_text" && step.Action != "assert_visible" {
			continue
		}
		key := assertionKey{action: step.Action, target: step.TestID}
		if step.Locator != nil {
			key.locator = *step.Locator
		}
		if step.Value != nil {
			key.value = *step.Value
		}
		keys = append(keys, key)
	}
	return keys
}

type ReviewedDraft struct {
	Index int `json:"index"`
	ScenarioInput
}
type TaskApprovalInput struct {
	TurnID string          `json:"turn_id"`
	Drafts []ReviewedDraft `json:"drafts"`
}

func (s *Store) ApproveTask(ctx context.Context, projectID, taskID string, in TaskApprovalInput) (TaskApproval, error) {
	if len(in.Drafts) == 0 || len(in.Drafts) > 50 {
		return TaskApproval{}, fmt.Errorf("%w: review 1–50 checks", ErrInvalid)
	}
	seen := map[int]bool{}
	for i := range in.Drafts {
		d := &in.Drafts[i]
		if d.Index < 0 || seen[d.Index] {
			return TaskApproval{}, ErrInvalid
		}
		seen[d.Index] = true
		d.Approved = true
		var err error
		d.ScenarioInput, err = ValidateScenario(d.ScenarioInput)
		if err != nil {
			return TaskApproval{}, err
		}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return TaskApproval{}, err
	}
	sum := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(sum[:])
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return TaskApproval{}, err
	}
	defer tx.Rollback(ctx)
	task, err := scanTask(tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM testing_tasks WHERE id=$1 AND project_id=$2 FOR UPDATE`, taskID, projectID))
	if err != nil {
		return TaskApproval{}, err
	}
	var previous string
	var idsRaw []byte
	err = tx.QueryRow(ctx, `SELECT input_sha256,scenario_ids FROM task_approvals WHERE task_id=$1 AND turn_id=$2`, taskID, in.TurnID).Scan(&previous, &idsRaw)
	if err == nil {
		if previous != fingerprint {
			return TaskApproval{}, ErrConflict
		}
		approval := TaskApproval{TurnID: in.TurnID}
		err = json.Unmarshal(idsRaw, &approval.ScenarioIDs)
		return approval, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return TaskApproval{}, err
	}
	turn, err := scanChatTurn(tx.QueryRow(ctx, `SELECT `+chatColumns+` FROM chat_turns WHERE id=$1 AND project_id=$2 AND task_id=$3`, in.TurnID, projectID, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskApproval{}, ErrNotFound
	}
	if err != nil {
		return TaskApproval{}, err
	}
	if turn.Status != "completed" || turn.Proposal == nil {
		return TaskApproval{}, ErrConflict
	}
	approval := TaskApproval{TurnID: in.TurnID, ScenarioIDs: []string{}}
	for _, draft := range in.Drafts {
		if draft.Index >= len(turn.Proposal.Scenarios) {
			return TaskApproval{}, ErrInvalid
		}
		original := turn.Proposal.Scenarios[draft.Index]
		if draft.ExpectedOutcome != original.ExpectedOutcome && slices.Equal(assertionKeys(draft.Steps), assertionKeys(original.Steps)) {
			return TaskApproval{}, fmt.Errorf("%w: the expected outcome changed but the assertions did not; revise the test plan before approving", ErrInvalid)
		}
		id, err := newID()
		if err != nil {
			return TaskApproval{}, err
		}
		steps, err := json.Marshal(draft.Steps)
		if err != nil {
			return TaskApproval{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO scenarios(id,project_id,name,description,expected_outcome,approved,steps,journey_id,task_id) VALUES($1,$2,$3,$4,$5,true,$6,$7,$8)`, id, projectID, draft.Name, draft.Description, draft.ExpectedOutcome, steps, task.JourneyID, taskID)
		if err != nil {
			return TaskApproval{}, err
		}
		approval.ScenarioIDs = append(approval.ScenarioIDs, id)
	}
	idsRaw, err = json.Marshal(approval.ScenarioIDs)
	if err != nil {
		return TaskApproval{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO task_approvals(task_id,turn_id,input_sha256,scenario_ids) VALUES($1,$2,$3,$4)`, taskID, in.TurnID, fingerprint, idsRaw); err != nil {
		return TaskApproval{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TaskApproval{}, err
	}
	return approval, nil
}

type CoverageCheck struct {
	Scenario   Scenario       `json:"scenario"`
	Result     ScenarioResult `json:"result"`
	RunID      string         `json:"run_id"`
	BaseURL    string         `json:"base_url"`
	FinishedAt time.Time      `json:"finished_at"`
}
type Coverage struct {
	Journeys  []Journey       `json:"journeys"`
	Checks    []CoverageCheck `json:"checks"`
	Scenarios []Scenario      `json:"scenarios"`
}

func (s *Store) Coverage(ctx context.Context, projectID string) (Coverage, error) {
	journeys, err := s.ListJourneys(ctx, projectID)
	if err != nil {
		return Coverage{}, err
	}
	scenarios, err := s.ListScenarios(ctx, projectID)
	if err != nil {
		return Coverage{}, err
	}
	if err := s.expireLeases(ctx); err != nil {
		return Coverage{}, err
	}
	rows, err := s.db.Query(ctx, `WITH latest AS (
 SELECT DISTINCT ON (result->>'scenario_id') r.id,r.base_url,r.created_at,r.finished_at,r.scenarios,result
 FROM runs r CROSS JOIN LATERAL jsonb_array_elements(r.results) result
 WHERE r.project_id=$1 AND r.status NOT IN ('queued','running')
 ORDER BY result->>'scenario_id',r.created_at DESC,r.id DESC
 ) SELECT snapshot,result,id,base_url,COALESCE(finished_at,created_at)
 FROM latest CROSS JOIN LATERAL jsonb_array_elements(scenarios) snapshot
 WHERE snapshot->>'id'=result->>'scenario_id'`, projectID)
	if err != nil {
		return Coverage{}, err
	}
	defer rows.Close()
	coverage := Coverage{Journeys: journeys, Scenarios: scenarios, Checks: []CoverageCheck{}}
	for rows.Next() {
		var c CoverageCheck
		var scenario, result []byte
		if err := rows.Scan(&scenario, &result, &c.RunID, &c.BaseURL, &c.FinishedAt); err != nil {
			return Coverage{}, err
		}
		if err := json.Unmarshal(scenario, &c.Scenario); err != nil {
			return Coverage{}, err
		}
		if err := json.Unmarshal(result, &c.Result); err != nil {
			return Coverage{}, err
		}
		coverage.Checks = append(coverage.Checks, c)
	}
	return coverage, rows.Err()
}

// Assigning a journey changes organization, never a frozen run definition.
func (s *Store) AssignJourney(ctx context.Context, projectID, scenarioID, journeyID string) error {
	tag, err := s.db.Exec(ctx, `UPDATE scenarios SET journey_id=$3 WHERE id=$1 AND project_id=$2 AND EXISTS(SELECT 1 FROM journeys WHERE id=$3 AND project_id=$2)`, scenarioID, projectID, journeyID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
