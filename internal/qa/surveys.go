package qa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Chibuoyimm/qa-agent/internal/repository"
	"github.com/jackc/pgx/v5"
)

type ProjectSource struct {
	ID             string `json:"id"`
	Repository     string `json:"repository"`
	Provider       string `json:"provider"`
	Role           string `json:"role"`
	Ref            string `json:"ref"`
	SnapshotID     string `json:"-"`
	HasSavedAccess bool   `json:"has_saved_access"`
	Credential     []byte `json:"-"`
}

// Explicit connections retain their chosen ref when CI imports a different revision.
// Projects created before connections existed use the newest import for each repository.
func (s *Store) ProjectSources(ctx context.Context, projectID string) ([]ProjectSource, error) {
	rows, err := s.db.Query(ctx, `WITH latest AS (
	    SELECT DISTINCT ON (provider, lower(repository)) id,provider,repository,role,ref
	    FROM repository_snapshots WHERE project_id=$1
	    ORDER BY provider,lower(repository),created_at DESC,id DESC
	)
	SELECT c.id,c.provider,c.repository,c.role,c.ref,COALESCE((
	    SELECT id FROM repository_snapshots WHERE project_id=$1 AND provider=c.provider
	    AND lower(repository)=lower(c.repository) AND ref=c.ref ORDER BY created_at DESC,id DESC LIMIT 1
	),''), c.credential FROM project_repository_sources c WHERE c.project_id=$1 AND c.included
	UNION ALL
	SELECT s.id,s.provider,s.repository,s.role,s.ref,s.id,NULL FROM latest s
	WHERE NOT EXISTS(SELECT 1 FROM project_repository_sources c WHERE c.project_id=$1 AND c.provider=s.provider AND lower(c.repository)=lower(s.repository))
	ORDER BY 2,3`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ProjectSource{}
	for rows.Next() {
		var v ProjectSource
		if err := rows.Scan(&v.ID, &v.Provider, &v.Repository, &v.Role, &v.Ref, &v.SnapshotID, &v.Credential); err != nil {
			return nil, err
		}
		v.HasSavedAccess = len(v.Credential) > 0
		result = append(result, v)
	}
	return result, rows.Err()
}

func (s *Store) ConnectSource(ctx context.Context, projectID string, source ProjectSource) (ProjectSource, error) {
	id, err := newID()
	if err != nil {
		return source, err
	}
	err = s.db.QueryRow(ctx, `INSERT INTO project_repository_sources(id,project_id,provider,repository,role,ref,credential)
	    VALUES($1,$2,$3,$4,$5,$6,$7)
	    ON CONFLICT(project_id,provider,lower(repository)) DO UPDATE SET role=excluded.role,ref=excluded.ref,credential=excluded.credential,included=true
	    RETURNING id`, id, projectID, source.Provider, source.Repository, source.Role, source.Ref, source.Credential).Scan(&source.ID)
	source.HasSavedAccess = len(source.Credential) > 0
	return source, err
}

// Retain an explicit opt-out even when historical imports still exist.
func (s *Store) DisconnectSource(ctx context.Context, project, source string) error {
	roots, err := s.ProjectSources(ctx, project)
	if err != nil {
		return err
	}
	for _, root := range roots {
		if root.ID != source {
			continue
		}
		_, err = s.db.Exec(ctx, `INSERT INTO project_repository_sources(id,project_id,provider,repository,ref,role,included)
    VALUES($1,$2,$3,$4,$5,$6,false) ON CONFLICT(project_id,provider,lower(repository))
    DO UPDATE SET included=false,credential=NULL`, root.ID, project, root.Provider, root.Repository, root.Ref, root.Role)
		return err
	}
	return ErrNotFound
}

func (s *Store) ForgetRepositoryCredential(ctx context.Context, project, source string) error {
	result, err := s.db.Exec(ctx, `UPDATE project_repository_sources SET credential=NULL WHERE project_id=$1 AND id=$2`, project, source)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) TaskSurvey(ctx context.Context, project, task, batch, source string) (SurveyWork, error) {
	var survey SurveyWork
	err := s.db.QueryRow(ctx, `SELECT r.survey FROM repository_surveys r JOIN testing_tasks t ON t.id=r.task_id WHERE t.project_id=$1 AND t.id=$2 AND r.batch_id=$3 AND r.source_id=$4`, project, task, batch, source).Scan(&survey)
	if errors.Is(err, pgx.ErrNoRows) {
		return survey, ErrNotFound
	}
	return survey, err
}

type InferredJourney struct {
	Name     string   `json:"name"`
	Evidence string   `json:"evidence"`
	Paths    []string `json:"paths"`
}

type SourceSurvey struct {
	SourceID          string            `json:"source_id"`
	Repository        string            `json:"repository"`
	Provider          string            `json:"provider"`
	Ref               string            `json:"ref"`
	CommitSHA         string            `json:"commit_sha"`
	Role              string            `json:"role"`
	Status            string            `json:"status"`
	Note              string            `json:"note,omitempty"`
	Batches           int               `json:"batches,omitempty"`
	BatchesRead       int               `json:"batches_read,omitempty"`
	InventoryExcluded int               `json:"inventory_excluded,omitempty"`
	ExcludedContent   int               `json:"excluded_content,omitempty"`
	Candidates        int               `json:"eligible_files,omitempty"`
	Paths             []string          `json:"paths"`
	Journeys          []InferredJourney `json:"journeys"`
	Gaps              []string          `json:"gaps"`
}

// Only the source summary is returned to clients. The queued immutable file
// identities stay with the task so interrupted scans can resume.
type SurveyWork struct {
	SourceSurvey
	Pending [][]repository.SurveyFile `json:"pending_files,omitempty"`
}

type WholeAppPlan struct {
	Sources []SourceSurvey `json:"sources"`
	// Each row corresponds to one scenario and contains the source journey IDs it covers.
	Coverage [][]string `json:"coverage"`
}

func JourneyRef(source, journey int) string { return fmt.Sprintf("R%d.J%d", source+1, journey+1) }

func (s *Store) TaskSurveys(ctx context.Context, projectID, taskID, batch string) (map[string]SourceSurvey, error) {
	rows, err := s.db.Query(ctx, `SELECT r.survey FROM repository_surveys r JOIN testing_tasks t ON t.id=r.task_id
        WHERE t.project_id=$1 AND t.id=$2 AND r.batch_id=$3`, projectID, taskID, batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]SourceSurvey{}
	for rows.Next() {
		var v SourceSurvey
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		result[v.SourceID] = v
	}
	return result, rows.Err()
}

func (s *Store) SaveTaskSurvey(ctx context.Context, projectID, taskID, batch string, survey SurveyWork, previous int) (SourceSurvey, error) {
	raw, err := json.Marshal(survey)
	if err != nil {
		return SourceSurvey{}, err
	}
	err = s.db.QueryRow(ctx, `INSERT INTO repository_surveys(task_id,batch_id,source_id,survey)
        SELECT t.id,$3,$4,$5 FROM testing_tasks t
        WHERE t.project_id=$1 AND t.id=$2
        ON CONFLICT(task_id,batch_id,source_id) DO UPDATE SET survey=EXCLUDED.survey
        WHERE repository_surveys.survey->>'status'='scanning' AND COALESCE((repository_surveys.survey->>'batches_read')::int,0)=$6 RETURNING survey`,
		projectID, taskID, batch, survey.SourceID, raw, previous).Scan(&survey)
	if errors.Is(err, pgx.ErrNoRows) {
		current, err := s.TaskSurvey(ctx, projectID, taskID, batch, survey.SourceID)
		return current.SourceSurvey, err
	}
	return survey.SourceSurvey, err
}
