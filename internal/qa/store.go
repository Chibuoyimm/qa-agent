package qa

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	db      *pgxpool.Pool
	allowed map[string]bool
}

func NewStore(db *pgxpool.Pool, allowed map[string]bool) *Store {
	return &Store{db: db, allowed: allowed}
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Store) CreateProject(ctx context.Context, in ProjectInput) (Project, error) {
	in, err := ValidateProject(in, s.allowed)
	if err != nil {
		return Project{}, err
	}
	id, err := newID()
	if err != nil {
		return Project{}, err
	}
	var p Project
	err = s.db.QueryRow(ctx, `INSERT INTO projects(id,name,base_url) VALUES($1,$2,$3) RETURNING id,name,base_url,created_at`, id, in.Name, in.BaseURL).
		Scan(&p.ID, &p.Name, &p.BaseURL, &p.CreatedAt)
	return p, err
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.Query(ctx, `SELECT id,name,base_url,created_at FROM projects ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.BaseURL, &p.CreatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (s *Store) GetProject(ctx context.Context, id string) (Project, error) {
	var p Project
	err := s.db.QueryRow(ctx, `SELECT id,name,base_url,created_at FROM projects WHERE id=$1`, id).
		Scan(&p.ID, &p.Name, &p.BaseURL, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return p, err
}

func (s *Store) CreateScenario(ctx context.Context, projectID string, in ScenarioInput) (Scenario, error) {
	in, err := ValidateScenario(in)
	if err != nil {
		return Scenario{}, err
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1)`, projectID).Scan(&exists); err != nil {
		return Scenario{}, err
	}
	if !exists {
		return Scenario{}, ErrNotFound
	}
	id, err := newID()
	if err != nil {
		return Scenario{}, err
	}
	steps, err := json.Marshal(in.Steps)
	if err != nil {
		return Scenario{}, err
	}
	var sc Scenario
	var raw []byte
	err = s.db.QueryRow(ctx, `INSERT INTO scenarios(id,project_id,name,description,expected_outcome,approved,steps)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id,project_id,name,description,expected_outcome,approved,steps,created_at`,
		id, projectID, in.Name, in.Description, in.ExpectedOutcome, in.Approved, steps).
		Scan(&sc.ID, &sc.ProjectID, &sc.Name, &sc.Description, &sc.ExpectedOutcome, &sc.Approved, &raw, &sc.CreatedAt)
	if err != nil {
		return Scenario{}, err
	}
	return sc, json.Unmarshal(raw, &sc.Steps)
}

func (s *Store) ListScenarios(ctx context.Context, projectID string) ([]Scenario, error) {
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1)`, projectID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.db.Query(ctx, `SELECT id,project_id,name,description,expected_outcome,approved,steps,created_at
		FROM scenarios WHERE project_id=$1 ORDER BY created_at,id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scenarios := []Scenario{}
	for rows.Next() {
		var sc Scenario
		var raw []byte
		if err := rows.Scan(&sc.ID, &sc.ProjectID, &sc.Name, &sc.Description, &sc.ExpectedOutcome, &sc.Approved, &raw, &sc.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &sc.Steps); err != nil {
			return nil, err
		}
		scenarios = append(scenarios, sc)
	}
	return scenarios, rows.Err()
}

func (s *Store) CreateRun(ctx context.Context, projectID string, in RunInput) (Run, error) {
	if err := validateRunInput(in); err != nil {
		return Run{}, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx)
	var baseURL string
	if err := tx.QueryRow(ctx, `SELECT base_url FROM projects WHERE id=$1 FOR SHARE`, projectID).Scan(&baseURL); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Run{}, ErrNotFound
		}
		return Run{}, err
	}
	id, err := newID()
	if err != nil {
		return Run{}, err
	}
	run, err := createRunTx(ctx, tx, id, projectID, baseURL, in)
	if err != nil {
		return Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, err
	}
	return run, nil
}

func validateRunInput(in RunInput) error {
	if in.Mode != "advisory" && in.Mode != "blocking" {
		return fmt.Errorf("%w: mode must be advisory or blocking", ErrInvalid)
	}
	if len(in.ScenarioIDs) == 0 || len(in.ScenarioIDs) > 50 {
		return fmt.Errorf("%w: select 1–50 scenarios", ErrInvalid)
	}
	selected := make(map[string]bool, len(in.ScenarioIDs))
	for _, id := range in.ScenarioIDs {
		if id == "" || selected[id] {
			return fmt.Errorf("%w: scenario_ids must be nonempty and unique", ErrInvalid)
		}
		selected[id] = true
	}
	return nil
}

func createRunTx(ctx context.Context, tx pgx.Tx, id, projectID, baseURL string, in RunInput) (Run, error) {
	rows, err := tx.Query(ctx, `SELECT id,project_id,name,description,expected_outcome,approved,steps,created_at
		FROM scenarios WHERE project_id=$1 AND id=ANY($2)`, projectID, in.ScenarioIDs)
	if err != nil {
		return Run{}, err
	}
	byID := make(map[string]Scenario, len(in.ScenarioIDs))
	for rows.Next() {
		var sc Scenario
		var raw []byte
		if err := rows.Scan(&sc.ID, &sc.ProjectID, &sc.Name, &sc.Description, &sc.ExpectedOutcome, &sc.Approved, &raw, &sc.CreatedAt); err != nil {
			rows.Close()
			return Run{}, err
		}
		if err := json.Unmarshal(raw, &sc.Steps); err != nil {
			rows.Close()
			return Run{}, err
		}
		byID[sc.ID] = sc
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Run{}, err
	}
	rows.Close()
	if len(byID) != len(in.ScenarioIDs) {
		return Run{}, fmt.Errorf("%w: a selected scenario is not in this project", ErrInvalid)
	}
	scenarios := make([]Scenario, 0, len(in.ScenarioIDs))
	for _, id := range in.ScenarioIDs {
		sc := byID[id]
		if !sc.Approved {
			return Run{}, fmt.Errorf("%w: all selected scenarios must be approved", ErrConflict)
		}
		scenarios = append(scenarios, sc)
	}
	snapshot, err := json.Marshal(scenarios)
	if err != nil {
		return Run{}, err
	}
	var created time.Time
	err = tx.QueryRow(ctx, `INSERT INTO runs(id,project_id,base_url,mode,status,gate,scenarios,results)
		VALUES($1,$2,$3,$4,'queued','pending',$5,'[]'::jsonb) RETURNING created_at`,
		id, projectID, baseURL, in.Mode, snapshot).Scan(&created)
	if err != nil {
		return Run{}, err
	}
	return Run{ID: id, ProjectID: projectID, BaseURL: baseURL, Mode: in.Mode, Status: "queued", Gate: "pending", Scenarios: scenarios, Results: []ScenarioResult{}, CreatedAt: created}, nil
}

func (s *Store) expireLeases(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `UPDATE runs SET status='error', gate=CASE WHEN mode='blocking' THEN 'fail' ELSE 'warn' END,
		finished_at=now(), lease_token=NULL, lease_expires_at=NULL
		WHERE status='running' AND lease_expires_at <= clock_timestamp()`)
	return err
}

type rowScanner interface{ Scan(...any) error }

func scanRun(row rowScanner) (Run, error) {
	var run Run
	var scenarios, results []byte
	err := row.Scan(&run.ID, &run.ProjectID, &run.BaseURL, &run.Mode, &run.Status, &run.Gate,
		&scenarios, &results, &run.CreatedAt, &run.StartedAt, &run.FinishedAt)
	if err != nil {
		return Run{}, err
	}
	if err := json.Unmarshal(scenarios, &run.Scenarios); err != nil {
		return Run{}, err
	}
	if err := json.Unmarshal(results, &run.Results); err != nil {
		return Run{}, err
	}
	return run, nil
}

const runColumns = `id,project_id,base_url,mode,status,gate,scenarios,results,created_at,started_at,finished_at`

func (s *Store) GetRun(ctx context.Context, id string) (Run, error) {
	if err := s.expireLeases(ctx); err != nil {
		return Run{}, err
	}
	run, err := scanRun(s.db.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return run, err
}

func (s *Store) ListRuns(ctx context.Context, projectID string) ([]Run, error) {
	if err := s.expireLeases(ctx); err != nil {
		return nil, err
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1)`, projectID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.db.Query(ctx, `SELECT `+runColumns+` FROM runs WHERE project_id=$1 ORDER BY created_at DESC,id DESC LIMIT 100`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Store) CancelRun(ctx context.Context, id string) (Run, error) {
	if err := s.expireLeases(ctx); err != nil {
		return Run{}, err
	}
	row := s.db.QueryRow(ctx, `UPDATE runs SET status='cancelled',gate=CASE WHEN mode='blocking' THEN 'fail' ELSE 'warn' END,
		finished_at=now(),lease_token=NULL,lease_expires_at=NULL
		WHERE id=$1 AND status IN ('queued','running') RETURNING `+runColumns, id)
	run, err := scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, s.missingOrConflict(ctx, id)
	}
	return run, err
}

func (s *Store) missingOrConflict(ctx context.Context, id string) error {
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return ErrConflict
}

func (s *Store) ClaimRun(ctx context.Context, workerID string) (Run, string, time.Time, bool, error) {
	if workerID == "" || len(workerID) > 200 {
		return Run{}, "", time.Time{}, false, fmt.Errorf("%w: worker_id must contain 1–200 characters", ErrInvalid)
	}
	if err := s.expireLeases(ctx); err != nil {
		return Run{}, "", time.Time{}, false, err
	}
	for {
		tx, err := s.db.Begin(ctx)
		if err != nil {
			return Run{}, "", time.Time{}, false, err
		}
		var id, baseURL string
		err = tx.QueryRow(ctx, `SELECT id,base_url FROM runs WHERE status='queued' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &baseURL)
		if errors.Is(err, pgx.ErrNoRows) {
			tx.Rollback(ctx)
			return Run{}, "", time.Time{}, false, nil
		}
		if err != nil {
			tx.Rollback(ctx)
			return Run{}, "", time.Time{}, false, err
		}
		if !s.allowed[baseURL] {
			_, err = tx.Exec(ctx, `UPDATE runs SET status='error',gate=CASE WHEN mode='blocking' THEN 'fail' ELSE 'warn' END,
				finished_at=now() WHERE id=$1`, id)
			if err == nil {
				err = tx.Commit(ctx)
			} else {
				tx.Rollback(ctx)
			}
			if err != nil {
				return Run{}, "", time.Time{}, false, err
			}
			continue
		}
		token, err := newID()
		if err != nil {
			tx.Rollback(ctx)
			return Run{}, "", time.Time{}, false, err
		}
		hash := sha256.Sum256([]byte(token))
		var expiry time.Time
		var run Run
		row := tx.QueryRow(ctx, `UPDATE runs SET status='running',worker_id=$2,lease_token=$3,
			lease_expires_at=clock_timestamp()+interval '60 seconds',started_at=now()
			WHERE id=$1 RETURNING `+runColumns+`,lease_expires_at`, id, workerID, hex.EncodeToString(hash[:]))
		var scenarios, results []byte
		err = row.Scan(&run.ID, &run.ProjectID, &run.BaseURL, &run.Mode, &run.Status, &run.Gate,
			&scenarios, &results, &run.CreatedAt, &run.StartedAt, &run.FinishedAt, &expiry)
		if err == nil {
			err = json.Unmarshal(scenarios, &run.Scenarios)
		}
		if err == nil {
			err = json.Unmarshal(results, &run.Results)
		}
		if err == nil {
			err = tx.Commit(ctx)
		} else {
			tx.Rollback(ctx)
		}
		return run, token, expiry, err == nil, err
	}
}

func (s *Store) Heartbeat(ctx context.Context, id, token string) (time.Time, error) {
	if token == "" {
		return time.Time{}, fmt.Errorf("%w: lease_token required", ErrInvalid)
	}
	hash := sha256.Sum256([]byte(token))
	var expiry time.Time
	err := s.db.QueryRow(ctx, `UPDATE runs SET lease_expires_at=clock_timestamp()+interval '60 seconds'
		WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_expires_at>clock_timestamp()
		RETURNING lease_expires_at`, id, hex.EncodeToString(hash[:])).Scan(&expiry)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, s.missingOrConflict(ctx, id)
	}
	return expiry, err
}

func (s *Store) CompleteRun(ctx context.Context, id string, in CompleteInput) (Run, error) {
	if in.LeaseToken == "" {
		return Run{}, fmt.Errorf("%w: lease_token required", ErrInvalid)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx)
	var run Run
	var scenarios, results []byte
	var storedToken string
	var leaseLive bool
	err = tx.QueryRow(ctx, `SELECT `+runColumns+`,COALESCE(lease_token,''),COALESCE(lease_expires_at>clock_timestamp(),false)
		FROM runs WHERE id=$1 FOR UPDATE`, id).Scan(&run.ID, &run.ProjectID, &run.BaseURL, &run.Mode, &run.Status,
		&run.Gate, &scenarios, &results, &run.CreatedAt, &run.StartedAt, &run.FinishedAt, &storedToken, &leaseLive)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	hash := sha256.Sum256([]byte(in.LeaseToken))
	if run.Status != "running" || storedToken != hex.EncodeToString(hash[:]) || !leaseLive {
		return Run{}, ErrConflict
	}
	if err := json.Unmarshal(scenarios, &run.Scenarios); err != nil {
		return Run{}, err
	}
	status, err := ValidateResults(run.Scenarios, in.Results)
	if err != nil {
		return Run{}, err
	}
	gate := "pass"
	if status != "passed" {
		gate = "warn"
		if run.Mode == "blocking" {
			gate = "fail"
		}
	}
	encoded, err := json.Marshal(in.Results)
	if err != nil {
		return Run{}, err
	}
	row := tx.QueryRow(ctx, `UPDATE runs SET status=$2,gate=$3,results=$4,finished_at=now(),lease_token=NULL,lease_expires_at=NULL
		WHERE id=$1 AND status='running' AND lease_token=$5 AND lease_expires_at>clock_timestamp() RETURNING `+runColumns,
		id, status, gate, encoded, storedToken)
	run, err = scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrConflict
	}
	if err != nil {
		return Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, err
	}
	return run, nil
}
