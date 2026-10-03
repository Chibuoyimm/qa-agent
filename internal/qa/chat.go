package qa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Proposal is an unapproved draft, shared by generation and conversation history.
type Proposal struct {
	CredentialMode      string              `json:"credential_mode"`
	ChatGPTProfileID    string              `json:"chatgpt_profile_id,omitempty"`
	Provider            string              `json:"provider"`
	Model               string              `json:"model"`
	ContextSHA256       string              `json:"context_sha256"`
	Scenarios           []ScenarioInput     `json:"scenarios"`
	Questions           []string            `json:"questions"`
	Assumptions         []string            `json:"assumptions"`
	RepositorySnapshots []RepositorySummary `json:"repository_snapshots"`
	DiscoveryID         string              `json:"discovery_id,omitempty"`
}

type ChatTurn struct {
	ID        string    `json:"id"`
	Sequence  int64     `json:"sequence"`
	ProjectID string    `json:"project_id"`
	Prompt    string    `json:"prompt"`
	Status    string    `json:"status"`
	Reply     string    `json:"reply"`
	Proposal  *Proposal `json:"proposal,omitempty"`
	RunID     string    `json:"run_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ChatPage struct {
	Turns    []ChatTurn `json:"turns"`
	HasOlder bool       `json:"has_older"`
}

const chatColumns = `id,sequence,project_id,prompt,status,reply,proposal,COALESCE(run_id,''),created_at`

func scanChatTurn(row rowScanner) (ChatTurn, error) {
	var turn ChatTurn
	var raw []byte
	err := row.Scan(&turn.ID, &turn.Sequence, &turn.ProjectID, &turn.Prompt, &turn.Status, &turn.Reply, &raw, &turn.RunID, &turn.CreatedAt)
	if err != nil {
		return ChatTurn{}, err
	}
	if raw != nil {
		if err := json.Unmarshal(raw, &turn.Proposal); err != nil {
			return ChatTurn{}, err
		}
	}
	return turn, nil
}

func ValidateChatRequest(id, prompt string) error {
	if len(id) != 32 || strings.IndexFunc(id, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') }) != -1 {
		return fmt.Errorf("%w: request_id must be 32 lowercase hexadecimal characters", ErrInvalid)
	}
	if len(strings.TrimSpace(prompt)) == 0 || len(prompt) > 4000 {
		return fmt.Errorf("%w: message must be 1–4000 bytes", ErrInvalid)
	}
	return nil
}

const interruptedChatReply = "This request was interrupted or failed. No checks were run. Send a new message to try again."

func (s *Store) ListChat(ctx context.Context, projectID string, before int64) (ChatPage, error) {
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return ChatPage{}, err
	}
	if _, err := s.db.Exec(ctx, `UPDATE chat_turns SET status='error',reply=$2 WHERE project_id=$1 AND status='pending' AND created_at < clock_timestamp()-interval '100 seconds'`, projectID, interruptedChatReply); err != nil {
		return ChatPage{}, err
	}
	rows, err := s.db.Query(ctx, `SELECT `+chatColumns+` FROM chat_turns WHERE project_id=$1 AND ($2::bigint=0 OR sequence<$2) ORDER BY sequence DESC LIMIT 21`, projectID, before)
	if err != nil {
		return ChatPage{}, err
	}
	defer rows.Close()
	page := ChatPage{Turns: []ChatTurn{}}
	for rows.Next() {
		turn, err := scanChatTurn(rows)
		if err != nil {
			return ChatPage{}, err
		}
		page.Turns = append(page.Turns, turn)
	}
	if err := rows.Err(); err != nil {
		return ChatPage{}, err
	}
	if len(page.Turns) > 20 {
		page.HasOlder = true
		page.Turns = page.Turns[:20]
	}
	for i, j := 0, len(page.Turns)-1; i < j; i, j = i+1, j-1 {
		page.Turns[i], page.Turns[j] = page.Turns[j], page.Turns[i]
	}
	return page, nil
}

// A short project lock reserves a turn; no database transaction remains open during inference.
func (s *Store) BeginChat(ctx context.Context, projectID, id, prompt, fingerprint string) (ChatTurn, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ChatTurn{}, false, err
	}
	defer tx.Rollback(ctx)
	var exists string
	if err := tx.QueryRow(ctx, `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, projectID).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ChatTurn{}, false, ErrNotFound
		}
		return ChatTurn{}, false, err
	}
	existing, err := scanChatTurn(tx.QueryRow(ctx, `SELECT `+chatColumns+` FROM chat_turns WHERE id=$1 AND project_id=$2`, id, projectID))
	if err == nil {
		var saved string
		if err := tx.QueryRow(ctx, `SELECT fingerprint FROM chat_turns WHERE id=$1`, id).Scan(&saved); err != nil {
			return ChatTurn{}, false, err
		}
		if saved != fingerprint || existing.Status == "pending" {
			return ChatTurn{}, false, ErrConflict
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ChatTurn{}, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE chat_turns SET status='error',reply=$2 WHERE project_id=$1 AND status='pending' AND created_at < clock_timestamp()-interval '100 seconds'`, projectID, interruptedChatReply); err != nil {
		return ChatTurn{}, false, err
	}
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chat_turns WHERE project_id=$1 AND status='pending')`, projectID).Scan(&busy); err != nil {
		return ChatTurn{}, false, err
	}
	if busy {
		return ChatTurn{}, false, ErrConflict
	}
	turn, err := scanChatTurn(tx.QueryRow(ctx, `INSERT INTO chat_turns(id,project_id,fingerprint,prompt,status) VALUES($1,$2,$3,$4,'pending') RETURNING `+chatColumns, id, projectID, fingerprint, prompt))
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return ChatTurn{}, false, ErrConflict
		}
		return ChatTurn{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ChatTurn{}, false, err
	}
	return turn, true, nil
}

func (s *Store) FinishChat(ctx context.Context, projectID, id, reply string, proposal *Proposal) (ChatTurn, error) {
	var raw []byte
	var err error
	status := "error"
	if proposal != nil {
		status = "completed"
		raw, err = json.Marshal(proposal)
		if err != nil {
			return ChatTurn{}, err
		}
	}
	turn, err := scanChatTurn(s.db.QueryRow(ctx, `UPDATE chat_turns SET status=$3,reply=$4,proposal=$5 WHERE project_id=$1 AND id=$2 AND status='pending' RETURNING `+chatColumns, projectID, id, status, reply, raw))
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatTurn{}, ErrConflict
	}
	return turn, err
}

type ChatRunInput struct {
	RequestID   string   `json:"request_id"`
	Prompt      string   `json:"prompt"`
	Action      string   `json:"action"`
	Mode        string   `json:"mode"`
	ScenarioIDs []string `json:"scenario_ids,omitempty"`
	SourceRunID string   `json:"source_run_id,omitempty"`
}

func (s *Store) CreateChatRun(ctx context.Context, projectID string, in ChatRunInput, fingerprint string) (ChatTurn, error) {
	if err := ValidateChatRequest(in.RequestID, in.Prompt); err != nil {
		return ChatTurn{}, err
	}
	if in.Mode != "advisory" && in.Mode != "blocking" {
		return ChatTurn{}, fmt.Errorf("%w: choose advisory or blocking mode", ErrInvalid)
	}
	switch in.Action {
	case "all_approved":
		if len(in.ScenarioIDs) > 0 || in.SourceRunID != "" {
			return ChatTurn{}, ErrInvalid
		}
	case "selected":
		if in.SourceRunID != "" || validateRunInput(RunInput{Mode: in.Mode, ScenarioIDs: in.ScenarioIDs}) != nil {
			return ChatTurn{}, ErrInvalid
		}
	case "rerun_failed":
		if len(in.ScenarioIDs) > 0 || in.SourceRunID == "" {
			return ChatTurn{}, ErrInvalid
		}
	default:
		return ChatTurn{}, fmt.Errorf("%w: unsupported chat run action", ErrInvalid)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ChatTurn{}, err
	}
	defer tx.Rollback(ctx)
	var baseURL string
	if err := tx.QueryRow(ctx, `SELECT base_url FROM projects WHERE id=$1 FOR UPDATE`, projectID).Scan(&baseURL); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ChatTurn{}, ErrNotFound
		}
		return ChatTurn{}, err
	}
	existing, err := scanChatTurn(tx.QueryRow(ctx, `SELECT `+chatColumns+` FROM chat_turns WHERE id=$1 AND project_id=$2`, in.RequestID, projectID))
	if err == nil {
		var saved string
		if err := tx.QueryRow(ctx, `SELECT fingerprint FROM chat_turns WHERE id=$1`, in.RequestID).Scan(&saved); err != nil {
			return ChatTurn{}, err
		}
		if saved != fingerprint || existing.RunID == "" {
			return ChatTurn{}, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ChatTurn{}, err
	}
	ids := in.ScenarioIDs
	if in.Action == "all_approved" {
		rows, err := tx.Query(ctx, `SELECT id FROM scenarios WHERE project_id=$1 AND approved ORDER BY created_at,id LIMIT 51`, projectID)
		if err != nil {
			return ChatTurn{}, err
		}
		ids = []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return ChatTurn{}, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return ChatTurn{}, err
		}
	}
	if in.Action == "rerun_failed" {
		source, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1 AND project_id=$2 FOR UPDATE`, in.SourceRunID, projectID))
		if errors.Is(err, pgx.ErrNoRows) {
			return ChatTurn{}, ErrNotFound
		}
		if err != nil {
			return ChatTurn{}, err
		}
		if source.Status == "queued" || source.Status == "running" {
			return ChatTurn{}, fmt.Errorf("%w: wait for the source run to finish", ErrConflict)
		}
		ids = []string{}
		for _, result := range source.Results {
			if result.Status == "failed" {
				ids = append(ids, result.ScenarioID)
			}
		}
	}
	if len(ids) == 0 {
		return ChatTurn{}, fmt.Errorf("%w: no approved checks or failed assertions are available for this request", ErrInvalid)
	}
	if err := validateRunInput(RunInput{Mode: in.Mode, ScenarioIDs: ids}); err != nil {
		return ChatTurn{}, err
	}
	runID, err := newID()
	if err != nil {
		return ChatTurn{}, err
	}
	if _, err := createRunTx(ctx, tx, runID, projectID, baseURL, RunInput{Mode: in.Mode, ScenarioIDs: ids}); err != nil {
		return ChatTurn{}, err
	}
	noun := "checks"
	if len(ids) == 1 {
		noun = "check"
	}
	reply := fmt.Sprintf("Queued %d approved %s. Results will appear here. This covers the selected checks, not every possible app behaviour.", len(ids), noun)
	turn, err := scanChatTurn(tx.QueryRow(ctx, `INSERT INTO chat_turns(id,project_id,fingerprint,prompt,status,reply,run_id) VALUES($1,$2,$3,$4,'completed',$5,$6) RETURNING `+chatColumns, in.RequestID, projectID, fingerprint, in.Prompt, reply, runID))
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return ChatTurn{}, ErrConflict
		}
		return ChatTurn{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ChatTurn{}, err
	}
	return turn, nil
}
