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

	"github.com/Chibuoyimm/qa-agent/internal/repository"
	"github.com/jackc/pgx/v5"
)

type ReleaseRepositoryInput struct {
	SnapshotID string `json:"snapshot_id"`
	Repository string `json:"repository"`
	Role       string `json:"role"`
	CommitSHA  string `json:"commit_sha"`
}

type ReleaseRepository struct {
	SnapshotID    string   `json:"snapshot_id"`
	Repository    string   `json:"repository"`
	Role          string   `json:"role"`
	CommitSHA     string   `json:"commit_sha"`
	ContentSHA256 string   `json:"content_sha256"`
	Paths         []string `json:"paths"`
}

type ReleaseInput struct {
	DeploymentKey string                   `json:"deployment_key"`
	BaseURL       string                   `json:"base_url"`
	Mode          string                   `json:"mode"`
	ScenarioIDs   []string                 `json:"scenario_ids"`
	Repositories  []ReleaseRepositoryInput `json:"repositories"`
}

type Release struct {
	ID            string              `json:"id"`
	ProjectID     string              `json:"project_id"`
	DeploymentKey string              `json:"deployment_key"`
	BaseURL       string              `json:"base_url"`
	Mode          string              `json:"mode"`
	Repositories  []ReleaseRepository `json:"repositories"`
	Run           Run                 `json:"run"`
	CreatedAt     time.Time           `json:"created_at"`
}

func (s *Store) CreateRelease(ctx context.Context, projectID string, in ReleaseInput) (Release, error) {
	if err := validateReleaseInput(in, s.allowed); err != nil {
		return Release{}, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Release{}, err
	}
	defer tx.Rollback(ctx)
	var exists string
	if err := tx.QueryRow(ctx, `SELECT id FROM projects WHERE id=$1 FOR SHARE`, projectID).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Release{}, ErrNotFound
		}
		return Release{}, err
	}
	repositories, err := selectReleaseRepositories(ctx, tx, projectID, in.Repositories)
	if err != nil {
		return Release{}, err
	}
	requestHash, err := releaseRequestHash(in, repositories)
	if err != nil {
		return Release{}, err
	}
	repositoriesJSON, err := json.Marshal(repositories)
	if err != nil {
		return Release{}, err
	}
	scenariosJSON, err := json.Marshal(in.ScenarioIDs)
	if err != nil {
		return Release{}, err
	}
	id, err := newID()
	if err != nil {
		return Release{}, err
	}
	runID, err := newID()
	if err != nil {
		return Release{}, err
	}
	var insertedID string
	err = tx.QueryRow(ctx, `INSERT INTO releases
		(id,project_id,deployment_key,base_url,mode,scenario_ids,repositories,request_sha256,run_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (project_id,deployment_key) DO NOTHING RETURNING id`,
		id, projectID, in.DeploymentKey, in.BaseURL, in.Mode, scenariosJSON, repositoriesJSON, requestHash, runID).
		Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingHash string
		if err := tx.QueryRow(ctx, `SELECT request_sha256 FROM releases WHERE project_id=$1 AND deployment_key=$2`,
			projectID, in.DeploymentKey).Scan(&existingHash); err != nil {
			return Release{}, err
		}
		if existingHash != requestHash {
			return Release{}, fmt.Errorf("%w: deployment_key already has different release inputs", ErrConflict)
		}
		if err := tx.Rollback(ctx); err != nil {
			return Release{}, err
		}
		return s.GetReleaseByKey(ctx, projectID, in.DeploymentKey)
	}
	if err != nil {
		return Release{}, err
	}
	_, err = createRunTx(ctx, tx, runID, projectID, in.BaseURL, RunInput{ScenarioIDs: in.ScenarioIDs, Mode: in.Mode})
	if err != nil {
		return Release{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Release{}, err
	}
	return s.GetRelease(ctx, projectID, insertedID)
}

func validateReleaseInput(in ReleaseInput, allowed map[string]bool) error {
	if len(in.DeploymentKey) == 0 || len(in.DeploymentKey) > 200 {
		return fmt.Errorf("%w: deployment_key must contain 1–200 characters", ErrInvalid)
	}
	for _, c := range in.DeploymentKey {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return fmt.Errorf("%w: deployment_key allows only letters, digits, underscores, and hyphens", ErrInvalid)
		}
	}
	if !allowed[in.BaseURL] {
		return fmt.Errorf("%w: base_url must exactly match an allowed origin", ErrInvalid)
	}
	validated, err := ValidateProject(ProjectInput{Name: "release", BaseURL: in.BaseURL}, allowed)
	if err != nil || validated.BaseURL != in.BaseURL {
		return fmt.Errorf("%w: base_url must exactly match an allowed origin", ErrInvalid)
	}
	if err := validateRunInput(RunInput{ScenarioIDs: in.ScenarioIDs, Mode: in.Mode}); err != nil {
		return err
	}
	if len(in.Repositories) < 1 || len(in.Repositories) > 2 {
		return fmt.Errorf("%w: select 1–2 repositories", ErrInvalid)
	}
	roles := make(map[string]bool, len(in.Repositories))
	for _, source := range in.Repositories {
		if source.SnapshotID == "" || source.Repository == "" || (source.Role != "frontend" && source.Role != "backend") ||
			!fullCommitSHA(source.CommitSHA) || roles[source.Role] {
			return fmt.Errorf("%w: repositories require unique roles, snapshot_id, repository, and full commit_sha", ErrInvalid)
		}
		roles[source.Role] = true
	}
	return nil
}

func fullCommitSHA(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	for _, c := range sha {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func selectReleaseRepositories(ctx context.Context, tx pgx.Tx, projectID string, inputs []ReleaseRepositoryInput) ([]ReleaseRepository, error) {
	selected := make([]ReleaseRepository, 0, len(inputs))
	for _, input := range inputs {
		var snapshot ReleaseRepository
		var filesJSON []byte
		err := tx.QueryRow(ctx, `SELECT repository,role,commit_sha,content_sha256,files
			FROM repository_snapshots WHERE project_id=$1 AND id=$2`, projectID, input.SnapshotID).
			Scan(&snapshot.Repository, &snapshot.Role, &snapshot.CommitSHA, &snapshot.ContentSHA256, &filesJSON)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: a repository snapshot is not in this project", ErrInvalid)
		}
		if err != nil {
			return nil, err
		}
		if snapshot.Repository != input.Repository || snapshot.Role != input.Role || !strings.EqualFold(snapshot.CommitSHA, input.CommitSHA) {
			return nil, fmt.Errorf("%w: repository details do not match the selected snapshot", ErrInvalid)
		}
		var files []repository.File
		if err := json.Unmarshal(filesJSON, &files); err != nil {
			return nil, err
		}
		if len(files) < 1 || len(files) > 20 {
			return nil, fmt.Errorf("%w: repository snapshot has invalid files", ErrInvalid)
		}
		snapshot.Paths = make([]string, 0, len(files))
		for _, file := range files {
			snapshot.Paths = append(snapshot.Paths, file.Path)
		}
		slices.Sort(snapshot.Paths)
		for i, path := range snapshot.Paths {
			if path == "" || i > 0 && path == snapshot.Paths[i-1] {
				return nil, fmt.Errorf("%w: repository snapshot has invalid paths", ErrInvalid)
			}
		}
		snapshot.SnapshotID = input.SnapshotID
		selected = append(selected, snapshot)
	}
	slices.SortFunc(selected, func(a, b ReleaseRepository) int { return strings.Compare(a.Role, b.Role) })
	return selected, nil
}

func releaseRequestHash(in ReleaseInput, repositories []ReleaseRepository) (string, error) {
	type sourceIdentity struct {
		Repository    string   `json:"repository"`
		Role          string   `json:"role"`
		CommitSHA     string   `json:"commit_sha"`
		ContentSHA256 string   `json:"content_sha256"`
		Paths         []string `json:"paths"`
	}
	identity := struct {
		BaseURL      string           `json:"base_url"`
		Mode         string           `json:"mode"`
		ScenarioIDs  []string         `json:"scenario_ids"`
		Repositories []sourceIdentity `json:"repositories"`
	}{BaseURL: in.BaseURL, Mode: in.Mode, ScenarioIDs: in.ScenarioIDs, Repositories: make([]sourceIdentity, 0, len(repositories))}
	for _, source := range repositories {
		identity.Repositories = append(identity.Repositories, sourceIdentity{source.Repository, source.Role, source.CommitSHA, source.ContentSHA256, source.Paths})
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func (s *Store) GetReleaseByKey(ctx context.Context, projectID, key string) (Release, error) {
	return s.getRelease(ctx, projectID, `deployment_key=$2`, key)
}

func (s *Store) GetRelease(ctx context.Context, projectID, id string) (Release, error) {
	return s.getRelease(ctx, projectID, `id=$2`, id)
}

func (s *Store) getRelease(ctx context.Context, projectID, condition, value string) (Release, error) {
	if err := s.expireLeases(ctx); err != nil {
		return Release{}, err
	}
	release, err := scanRelease(s.db.QueryRow(ctx, releaseSelect+` WHERE l.project_id=$1 AND l.`+condition, projectID, value))
	if errors.Is(err, pgx.ErrNoRows) {
		return Release{}, ErrNotFound
	}
	return release, err
}

func (s *Store) ListReleases(ctx context.Context, projectID string) ([]Release, error) {
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return nil, err
	}
	if err := s.expireLeases(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, releaseSelect+` WHERE l.project_id=$1 ORDER BY l.created_at DESC,l.id DESC LIMIT 100`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	releases := []Release{}
	for rows.Next() {
		release, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	return releases, rows.Err()
}

const releaseSelect = `SELECT l.id,l.project_id,l.deployment_key,l.base_url,l.mode,l.repositories,l.created_at,
	r.id,r.project_id,r.base_url,r.mode,r.status,r.gate,r.scenarios,r.results,r.created_at,r.started_at,r.finished_at
	FROM releases l JOIN runs r ON r.id=l.run_id AND r.project_id=l.project_id`

func scanRelease(row rowScanner) (Release, error) {
	var release Release
	var repositoriesJSON, scenariosJSON, resultsJSON []byte
	err := row.Scan(&release.ID, &release.ProjectID, &release.DeploymentKey, &release.BaseURL, &release.Mode,
		&repositoriesJSON, &release.CreatedAt, &release.Run.ID, &release.Run.ProjectID, &release.Run.BaseURL,
		&release.Run.Mode, &release.Run.Status, &release.Run.Gate, &scenariosJSON, &resultsJSON,
		&release.Run.CreatedAt, &release.Run.StartedAt, &release.Run.FinishedAt)
	if err != nil {
		return Release{}, err
	}
	if err := json.Unmarshal(repositoriesJSON, &release.Repositories); err != nil {
		return Release{}, err
	}
	if err := json.Unmarshal(scenariosJSON, &release.Run.Scenarios); err != nil {
		return Release{}, err
	}
	if err := json.Unmarshal(resultsJSON, &release.Run.Results); err != nil {
		return Release{}, err
	}
	return release, nil
}
