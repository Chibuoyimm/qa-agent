package qa

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type DiscoveryInput struct {
	StartPath       string `json:"start_path"`
	MaxPages        int    `json:"max_pages"`
	SetupScenarioID string `json:"setup_scenario_id,omitempty"`
}

type DiscoveryElement struct {
	TestID    string `json:"test_id"`
	Tag       string `json:"tag"`
	Role      string `json:"role"`
	Label     string `json:"label"`
	Text      string `json:"text"`
	InputType string `json:"input_type"`
}

type DiscoveryLink struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

type DiscoveryPage struct {
	Path      string             `json:"path"`
	Title     string             `json:"title"`
	Headings  []string           `json:"headings"`
	Elements  []DiscoveryElement `json:"elements"`
	Links     []DiscoveryLink    `json:"links"`
	Truncated bool               `json:"truncated"`
}

type DiscoveryResult struct {
	Pages    []DiscoveryPage `json:"pages"`
	Limited  bool            `json:"limited"`
	Warnings []string        `json:"warnings"`
}

type Discovery struct {
	ID            string           `json:"id"`
	ProjectID     string           `json:"project_id"`
	BaseURL       string           `json:"base_url"`
	StartPath     string           `json:"start_path"`
	MaxPages      int              `json:"max_pages"`
	SetupScenario *Scenario        `json:"setup_scenario,omitempty"`
	Status        string           `json:"status"`
	Result        *DiscoveryResult `json:"result,omitempty"`
	Error         string           `json:"error"`
	CreatedAt     time.Time        `json:"created_at"`
	StartedAt     *time.Time       `json:"started_at,omitempty"`
	FinishedAt    *time.Time       `json:"finished_at,omitempty"`
}

type CompleteDiscoveryInput struct {
	LeaseToken string           `json:"lease_token"`
	Result     *DiscoveryResult `json:"result,omitempty"`
	Error      string           `json:"error,omitempty"`
}

func SafeDiscoveryPath(path string) bool {
	if len(path) == 0 || utf8.RuneCountInString(path) > 2048 || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "..") || strings.ContainsAny(path, "?#\\") || !utf8.ValidString(path) {
		return false
	}
	for _, r := range path {
		if r <= 0x20 {
			return false
		}
	}
	return true
}

func validObservationText(value string, limit int) bool {
	if utf8.RuneCountInString(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 && r != '\t' {
			return false
		}
	}
	return true
}

func ValidateDiscoveryResult(result DiscoveryResult, maxPages int) error {
	if len(result.Pages) == 0 || len(result.Pages) > maxPages || len(result.Pages) > 5 || result.Warnings == nil || len(result.Warnings) > 20 {
		return fmt.Errorf("%w: discovery page or warning count exceeds limits", ErrInvalid)
	}
	for _, warning := range result.Warnings {
		if !validObservationText(warning, 500) {
			return fmt.Errorf("%w: invalid discovery warning", ErrInvalid)
		}
	}
	seen := make(map[string]bool, len(result.Pages))
	for _, page := range result.Pages {
		if !SafeDiscoveryPath(page.Path) || seen[page.Path] || !validObservationText(page.Title, 200) ||
			page.Headings == nil || page.Elements == nil || page.Links == nil ||
			len(page.Headings) > 20 || len(page.Elements) > 80 || len(page.Links) > 80 {
			return fmt.Errorf("%w: invalid discovery page", ErrInvalid)
		}
		seen[page.Path] = true
		for _, heading := range page.Headings {
			if !validObservationText(heading, 300) {
				return fmt.Errorf("%w: invalid discovery heading", ErrInvalid)
			}
		}
		for _, element := range page.Elements {
			if !validObservationText(element.TestID, 200) || element.Tag == "" || !validObservationText(element.Tag, 100) ||
				!validObservationText(element.Role, 100) || !validObservationText(element.Label, 200) ||
				!validObservationText(element.Text, 300) || !validObservationText(element.InputType, 50) {
				return fmt.Errorf("%w: invalid discovery element", ErrInvalid)
			}
		}
		for _, link := range page.Links {
			if !SafeDiscoveryPath(link.Path) || !validObservationText(link.Text, 300) {
				return fmt.Errorf("%w: invalid discovery link", ErrInvalid)
			}
		}
	}
	var pages bytes.Buffer
	encoder := json.NewEncoder(&pages)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result.Pages); err != nil || pages.Len()-1 > 40000 {
		return fmt.Errorf("%w: discovery observations exceed 40000 bytes", ErrInvalid)
	}
	return nil
}

const discoveryColumns = `id,project_id,base_url,start_path,max_pages,setup_scenario,status,result,error,created_at,started_at,finished_at`

func scanDiscovery(row rowScanner) (Discovery, error) {
	var discovery Discovery
	var setup, result []byte
	err := row.Scan(&discovery.ID, &discovery.ProjectID, &discovery.BaseURL, &discovery.StartPath,
		&discovery.MaxPages, &setup, &discovery.Status, &result, &discovery.Error, &discovery.CreatedAt,
		&discovery.StartedAt, &discovery.FinishedAt)
	if err != nil {
		return Discovery{}, err
	}
	if len(setup) > 0 {
		discovery.SetupScenario = new(Scenario)
		if err := json.Unmarshal(setup, discovery.SetupScenario); err != nil {
			return Discovery{}, err
		}
	}
	if len(result) > 0 {
		discovery.Result = new(DiscoveryResult)
		if err := json.Unmarshal(result, discovery.Result); err != nil {
			return Discovery{}, err
		}
	}
	return discovery, nil
}

func (s *Store) CreateDiscovery(ctx context.Context, projectID string, in DiscoveryInput) (Discovery, error) {
	if !SafeDiscoveryPath(in.StartPath) || in.MaxPages < 1 || in.MaxPages > 5 {
		return Discovery{}, fmt.Errorf("%w: start_path and max_pages must be within discovery limits", ErrInvalid)
	}
	project, err := s.GetProject(ctx, projectID)
	if err != nil {
		return Discovery{}, err
	}
	var setup []byte
	if in.SetupScenarioID != "" {
		var scenario Scenario
		var steps []byte
		err := s.db.QueryRow(ctx, `SELECT id,project_id,name,description,expected_outcome,approved,steps,created_at
			FROM scenarios WHERE project_id=$1 AND id=$2`, projectID, in.SetupScenarioID).
			Scan(&scenario.ID, &scenario.ProjectID, &scenario.Name, &scenario.Description, &scenario.ExpectedOutcome,
				&scenario.Approved, &steps, &scenario.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return Discovery{}, fmt.Errorf("%w: setup scenario must belong to this project", ErrInvalid)
		}
		if err != nil {
			return Discovery{}, err
		}
		if !scenario.Approved {
			return Discovery{}, fmt.Errorf("%w: setup scenario must be approved", ErrInvalid)
		}
		if err := json.Unmarshal(steps, &scenario.Steps); err != nil {
			return Discovery{}, err
		}
		setup, err = json.Marshal(scenario)
		if err != nil {
			return Discovery{}, err
		}
	}
	id, err := newID()
	if err != nil {
		return Discovery{}, err
	}
	row := s.db.QueryRow(ctx, `INSERT INTO discoveries(id,project_id,base_url,start_path,max_pages,setup_scenario,status)
		VALUES($1,$2,$3,$4,$5,$6,'queued') RETURNING `+discoveryColumns,
		id, projectID, project.BaseURL, in.StartPath, in.MaxPages, setup)
	return scanDiscovery(row)
}

func (s *Store) expireDiscoveries(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `UPDATE discoveries SET status='error',error='Discovery lease expired',
		finished_at=now(),lease_token=NULL,lease_expires_at=NULL
		WHERE status='running' AND lease_expires_at<=clock_timestamp()`)
	return err
}

func (s *Store) ListDiscoveries(ctx context.Context, projectID string) ([]Discovery, error) {
	if err := s.expireDiscoveries(ctx); err != nil {
		return nil, err
	}
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT `+discoveryColumns+` FROM discoveries
		WHERE project_id=$1 ORDER BY created_at DESC,id DESC LIMIT 100`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	discoveries := []Discovery{}
	for rows.Next() {
		discovery, err := scanDiscovery(rows)
		if err != nil {
			return nil, err
		}
		discoveries = append(discoveries, discovery)
	}
	return discoveries, rows.Err()
}

func (s *Store) GetDiscovery(ctx context.Context, id string) (Discovery, error) {
	if err := s.expireDiscoveries(ctx); err != nil {
		return Discovery{}, err
	}
	discovery, err := scanDiscovery(s.db.QueryRow(ctx, `SELECT `+discoveryColumns+` FROM discoveries WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Discovery{}, ErrNotFound
	}
	return discovery, err
}

func (s *Store) SelectDiscovery(ctx context.Context, projectID, id string) (Discovery, error) {
	discovery, err := s.GetDiscovery(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Discovery{}, fmt.Errorf("%w: discovery is not in this project", ErrInvalid)
	}
	if err != nil {
		return Discovery{}, err
	}
	if discovery.ProjectID != projectID || discovery.Status != "completed" || discovery.Result == nil {
		return Discovery{}, fmt.Errorf("%w: discovery must be completed for this project", ErrInvalid)
	}
	return discovery, nil
}

func (s *Store) CancelDiscovery(ctx context.Context, id string) (Discovery, error) {
	if err := s.expireDiscoveries(ctx); err != nil {
		return Discovery{}, err
	}
	row := s.db.QueryRow(ctx, `UPDATE discoveries SET status='cancelled',finished_at=now(),
		lease_token=NULL,lease_expires_at=NULL WHERE id=$1 AND status IN ('queued','running')
		RETURNING `+discoveryColumns, id)
	discovery, err := scanDiscovery(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Discovery{}, s.discoveryMissingOrConflict(ctx, id)
	}
	return discovery, err
}

func (s *Store) discoveryMissingOrConflict(ctx context.Context, id string) error {
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM discoveries WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return ErrConflict
}

func (s *Store) ClaimDiscovery(ctx context.Context, workerID string) (Discovery, string, time.Time, bool, error) {
	if workerID == "" || len(workerID) > 200 {
		return Discovery{}, "", time.Time{}, false, fmt.Errorf("%w: worker_id must contain 1–200 characters", ErrInvalid)
	}
	if err := s.expireDiscoveries(ctx); err != nil {
		return Discovery{}, "", time.Time{}, false, err
	}
	for {
		tx, err := s.db.Begin(ctx)
		if err != nil {
			return Discovery{}, "", time.Time{}, false, err
		}
		var id, baseURL string
		err = tx.QueryRow(ctx, `SELECT id,base_url FROM discoveries WHERE status='queued'
			ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &baseURL)
		if errors.Is(err, pgx.ErrNoRows) {
			tx.Rollback(ctx)
			return Discovery{}, "", time.Time{}, false, nil
		}
		if err != nil {
			tx.Rollback(ctx)
			return Discovery{}, "", time.Time{}, false, err
		}
		if !s.allowed[baseURL] {
			_, err = tx.Exec(ctx, `UPDATE discoveries SET status='error',error='Target origin is no longer allowed',finished_at=now() WHERE id=$1`, id)
			if err == nil {
				err = tx.Commit(ctx)
			} else {
				tx.Rollback(ctx)
			}
			if err != nil {
				return Discovery{}, "", time.Time{}, false, err
			}
			continue
		}
		token, err := newID()
		if err != nil {
			tx.Rollback(ctx)
			return Discovery{}, "", time.Time{}, false, err
		}
		hash := sha256.Sum256([]byte(token))
		var expiry time.Time
		row := tx.QueryRow(ctx, `UPDATE discoveries SET status='running',worker_id=$2,lease_token=$3,
			lease_expires_at=clock_timestamp()+interval '60 seconds',started_at=now()
			WHERE id=$1 RETURNING `+discoveryColumns+`,lease_expires_at`, id, workerID, hex.EncodeToString(hash[:]))
		var discovery Discovery
		var setup, result []byte
		err = row.Scan(&discovery.ID, &discovery.ProjectID, &discovery.BaseURL, &discovery.StartPath,
			&discovery.MaxPages, &setup, &discovery.Status, &result, &discovery.Error, &discovery.CreatedAt,
			&discovery.StartedAt, &discovery.FinishedAt, &expiry)
		if err == nil && len(setup) > 0 {
			discovery.SetupScenario = new(Scenario)
			err = json.Unmarshal(setup, discovery.SetupScenario)
		}
		if err == nil {
			err = tx.Commit(ctx)
		} else {
			tx.Rollback(ctx)
		}
		return discovery, token, expiry, err == nil, err
	}
}

func (s *Store) HeartbeatDiscovery(ctx context.Context, id, token string) (time.Time, error) {
	if token == "" {
		return time.Time{}, fmt.Errorf("%w: lease_token required", ErrInvalid)
	}
	hash := sha256.Sum256([]byte(token))
	var expiry time.Time
	err := s.db.QueryRow(ctx, `UPDATE discoveries SET lease_expires_at=clock_timestamp()+interval '60 seconds'
		WHERE id=$1 AND status='running' AND lease_token=$2 AND lease_expires_at>clock_timestamp()
		RETURNING lease_expires_at`, id, hex.EncodeToString(hash[:])).Scan(&expiry)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, s.discoveryMissingOrConflict(ctx, id)
	}
	return expiry, err
}

func (s *Store) CompleteDiscovery(ctx context.Context, id string, in CompleteDiscoveryInput) (Discovery, error) {
	if in.LeaseToken == "" || (in.Result == nil) == (strings.TrimSpace(in.Error) == "") || len(in.Error) > 500 || !validObservationText(in.Error, 500) {
		return Discovery{}, fmt.Errorf("%w: completion requires a lease and exactly one valid result or error", ErrInvalid)
	}
	var maxPages int
	var status, storedToken string
	var leaseLive bool
	if err := s.db.QueryRow(ctx, `SELECT max_pages,status,COALESCE(lease_token,''),
		COALESCE(lease_expires_at>clock_timestamp(),false) FROM discoveries WHERE id=$1`, id).
		Scan(&maxPages, &status, &storedToken, &leaseLive); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Discovery{}, ErrNotFound
		}
		return Discovery{}, err
	}
	hash := sha256.Sum256([]byte(in.LeaseToken))
	if status != "running" || storedToken != hex.EncodeToString(hash[:]) || !leaseLive {
		return Discovery{}, ErrConflict
	}
	var encoded []byte
	if in.Result != nil {
		if err := ValidateDiscoveryResult(*in.Result, maxPages); err != nil {
			return Discovery{}, err
		}
		var err error
		encoded, err = json.Marshal(in.Result)
		if err != nil {
			return Discovery{}, err
		}
	}
	status = "error"
	if in.Result != nil {
		status = "completed"
	}
	row := s.db.QueryRow(ctx, `UPDATE discoveries SET status=$2,result=$3,error=$4,finished_at=now(),
		lease_token=NULL,lease_expires_at=NULL WHERE id=$1 AND status='running' AND lease_token=$5
		AND lease_expires_at>clock_timestamp() RETURNING `+discoveryColumns,
		id, status, encoded, in.Error, hex.EncodeToString(hash[:]))
	discovery, err := scanDiscovery(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Discovery{}, ErrConflict
	}
	return discovery, err
}
