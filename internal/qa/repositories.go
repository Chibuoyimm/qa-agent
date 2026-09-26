package qa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Chibuoyimm/qa-agent/internal/repository"
	"github.com/jackc/pgx/v5"
)

type RepositorySummary struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Repository    string    `json:"repository"`
	Ref           string    `json:"ref"`
	Role          string    `json:"role"`
	CommitSHA     string    `json:"commit_sha"`
	ContentSHA256 string    `json:"content_sha256"`
	FileCount     int       `json:"file_count"`
	TotalBytes    int       `json:"total_bytes"`
	CreatedAt     time.Time `json:"created_at"`
}

type RepositorySnapshot struct {
	RepositorySummary
	Files []repository.File `json:"files"`
}

const repositoryColumns = `id,project_id,repository,ref,role,commit_sha,content_sha256,file_count,total_bytes,created_at`

func scanRepositorySummary(row rowScanner) (RepositorySummary, error) {
	var summary RepositorySummary
	err := row.Scan(&summary.ID, &summary.ProjectID, &summary.Repository, &summary.Ref, &summary.Role,
		&summary.CommitSHA, &summary.ContentSHA256, &summary.FileCount, &summary.TotalBytes, &summary.CreatedAt)
	return summary, err
}

func (s *Store) CreateRepositorySnapshot(ctx context.Context, projectID string, imported repository.Snapshot) (RepositorySnapshot, error) {
	actualBytes := 0
	for _, file := range imported.Files {
		actualBytes += len(file.Content)
	}
	if len(imported.Files) < 1 || len(imported.Files) > 20 || imported.TotalBytes != actualBytes || imported.TotalBytes > 40000 ||
		(imported.Role != "frontend" && imported.Role != "backend") {
		return RepositorySnapshot{}, fmt.Errorf("%w: invalid repository snapshot", ErrInvalid)
	}
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return RepositorySnapshot{}, err
	}
	id, err := newID()
	if err != nil {
		return RepositorySnapshot{}, err
	}
	files, err := json.Marshal(imported.Files)
	if err != nil {
		return RepositorySnapshot{}, err
	}
	row := s.db.QueryRow(ctx, `INSERT INTO repository_snapshots
		(id,project_id,repository,ref,role,commit_sha,content_sha256,files,file_count,total_bytes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+repositoryColumns,
		id, projectID, imported.Repository, imported.Ref, imported.Role, imported.CommitSHA,
		imported.ContentSHA256, files, len(imported.Files), imported.TotalBytes)
	summary, err := scanRepositorySummary(row)
	if err != nil {
		return RepositorySnapshot{}, err
	}
	return RepositorySnapshot{RepositorySummary: summary, Files: imported.Files}, nil
}

func (s *Store) ListRepositorySnapshots(ctx context.Context, projectID string) ([]RepositorySummary, error) {
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT `+repositoryColumns+` FROM repository_snapshots
		WHERE project_id=$1 ORDER BY created_at DESC,id DESC LIMIT 100`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := []RepositorySummary{}
	for rows.Next() {
		summary, err := scanRepositorySummary(rows)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

func (s *Store) GetRepositorySnapshot(ctx context.Context, projectID, snapshotID string) (RepositorySnapshot, error) {
	var snapshot RepositorySnapshot
	var files []byte
	err := s.db.QueryRow(ctx, `SELECT `+repositoryColumns+`,files FROM repository_snapshots
		WHERE project_id=$1 AND id=$2`, projectID, snapshotID).Scan(&snapshot.ID, &snapshot.ProjectID,
		&snapshot.Repository, &snapshot.Ref, &snapshot.Role, &snapshot.CommitSHA, &snapshot.ContentSHA256,
		&snapshot.FileCount, &snapshot.TotalBytes, &snapshot.CreatedAt, &files)
	if errors.Is(err, pgx.ErrNoRows) {
		return RepositorySnapshot{}, ErrNotFound
	}
	if err != nil {
		return RepositorySnapshot{}, err
	}
	return snapshot, json.Unmarshal(files, &snapshot.Files)
}

func (s *Store) SelectRepositorySnapshots(ctx context.Context, projectID string, ids []string) ([]RepositorySnapshot, error) {
	if len(ids) > 2 {
		return nil, fmt.Errorf("%w: select at most two repository snapshots", ErrInvalid)
	}
	selected := make([]RepositorySnapshot, 0, len(ids))
	seenIDs := make(map[string]bool, len(ids))
	seenRoles := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || seenIDs[id] {
			return nil, fmt.Errorf("%w: repository_snapshot_ids must be nonempty and unique", ErrInvalid)
		}
		seenIDs[id] = true
		snapshot, err := s.GetRepositorySnapshot(ctx, projectID, id)
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: a repository snapshot is not in this project", ErrInvalid)
		}
		if err != nil {
			return nil, err
		}
		if seenRoles[snapshot.Role] {
			return nil, fmt.Errorf("%w: select at most one repository snapshot per role", ErrInvalid)
		}
		seenRoles[snapshot.Role] = true
		selected = append(selected, snapshot)
	}
	return selected, nil
}
