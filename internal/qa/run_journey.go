package qa

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

type RunJourneyInput struct {
	RequestID string `json:"request_id"`
	Prompt    string `json:"prompt"`
	Reference string `json:"reference"`
	JourneyID string `json:"journey_id,omitempty"`
	Mode      string `json:"mode"`
}

// Resolve names against the saved selection's own source references. Unrelated
// items in its original whole-app plan must never participate in this lookup.
func savedCandidates(ctx context.Context, tx pgx.Tx, project string) ([]journeyCandidate, error) {
	rows, err := tx.Query(ctx, `SELECT j.id,j.name,ARRAY(
  SELECT flow->>'name' FROM jsonb_array_elements(NULLIF(c.proposal #> '{whole_app,sources}','null'::jsonb)) WITH ORDINALITY src(source,i)
  CROSS JOIN LATERAL jsonb_array_elements(NULLIF(source->'journeys','null'::jsonb)) WITH ORDINALITY listed(flow,k)
  WHERE (j.saved_plan->'source_refs') ? format('R%s.J%s',i,k))
  FROM journeys j JOIN chat_turns c ON c.id=j.saved_plan->>'turn_id' AND c.project_id=j.project_id
  WHERE j.project_id=$1 ORDER BY j.name,j.id`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []journeyCandidate
	for rows.Next() {
		var c journeyCandidate
		if err := rows.Scan(&c.Item, &c.Name, &c.aliases); err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

func matchNamedJourneys(items []journeyCandidate, reference string) []journeyCandidate {
	var matches []journeyCandidate
	query := journeyWords(reference)
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(reference), item.Name) {
			matches = []journeyCandidate{item}
			break
		}
		if titleMatch(item.Name, query) > 0 || slices.ContainsFunc(item.aliases, func(alias string) bool { return titleMatch(alias, query) > 0 }) {
			matches = append(matches, item)
		}
	}
	return matches
}

// The latest deliberately prepared revision supersedes the original saved item.
// Copies displayed for review keep the original version and cannot supersede it.
func journeyDraft(ctx context.Context, tx pgx.Tx, project, journey string) (Proposal, []string, int, error) {
	var p Proposal
	err := tx.QueryRow(ctx, `SELECT proposal FROM chat_turns WHERE project_id=$1 AND proposal->>'saved_flow_id'=$2
 AND proposal->>'flow_version'=id AND status='completed' ORDER BY sequence DESC LIMIT 1`, project, journey).Scan(&p)
	if err == nil {
		return p, p.Questions, -1, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return p, nil, -1, err
	}
	var saved JourneyPlan
	var original *ScenarioInput
	err = tx.QueryRow(ctx, `SELECT j.saved_plan,CASE WHEN j.saved_plan->>'item' LIKE 'ready:%' THEN
 (c.proposal->'scenarios')->split_part(j.saved_plan->>'item',':',2)::int END FROM journeys j JOIN chat_turns c ON c.id=j.saved_plan->>'turn_id' AND c.project_id=j.project_id WHERE j.project_id=$1 AND j.id=$2`, project, journey).Scan(&saved, &original)
	if err != nil {
		return p, nil, -1, err
	}
	p = Proposal{SavedFlowID: journey, FlowVersion: saved.TurnID, Scenarios: []ScenarioInput{}, Questions: []string{}, Assumptions: []string{}}
	if len(saved.Steps) == 0 {
		return p, saved.MissingSetup, -1, nil
	}
	ix, err := strconv.Atoi(strings.TrimPrefix(saved.Item, "ready:"))
	if err != nil || ix < 0 || original == nil {
		return p, nil, -1, ErrConflict
	}
	draft := *original
	draft.Steps = saved.Steps
	draft.ExpectedOutcome = saved.ExpectedOutcome
	p.Scenarios = []ScenarioInput{draft}
	return p, nil, ix, nil
}

func journeyApproval(ctx context.Context, tx pgx.Tx, project, target string, p Proposal, sourceIndex int) ([]string, error) {
	var ids []string
	// A source-plan approval can cover siblings. Pick only this item's position
	// in the reviewer's selection, including when the reviewer reordered it.
	err := tx.QueryRow(ctx, `SELECT CASE WHEN c.id=$3 AND $5::int>=0
 THEN jsonb_build_array(a.scenario_ids->(array_position(a.draft_indices,$5::int)-1)) ELSE a.scenario_ids END
 FROM task_approvals a JOIN chat_turns c ON c.id=a.turn_id
 WHERE c.project_id=$1 AND a.base_url=$2 AND
 ((c.id=$3 AND $5=ANY(a.draft_indices)) OR
 (c.proposal->>'saved_flow_id'=$4 AND c.proposal->>'flow_version'=$3))
 ORDER BY a.created_at DESC,c.sequence DESC LIMIT 1`, project, target, p.FlowVersion, p.SavedFlowID, sourceIndex).Scan(&ids)
	if !errors.Is(err, pgx.ErrNoRows) {
		return ids, err
	}
	// Pre-target approvals have no indices. Reuse one only when a recorded run
	// proves the target and a single approved check exactly matches this item.
	if sourceIndex < 0 {
		return nil, nil
	}
	d := p.Scenarios[0]
	err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(DISTINCT s.id),'[]'::jsonb) FROM task_approvals a
 JOIN scenarios s ON a.scenario_ids ? s.id AND s.project_id=$1
 WHERE a.turn_id=$2 AND a.base_url IS NULL AND s.approved AND s.name=$3 AND s.expected_outcome=$4 AND s.steps=$5
 AND EXISTS(SELECT 1 FROM runs r CROSS JOIN LATERAL jsonb_array_elements(r.scenarios) rs
 WHERE r.project_id=$1 AND r.base_url=$6 AND rs->>'id'=s.id AND rs->'steps'=s.steps AND rs->>'expected_outcome'=s.expected_outcome)`, project, p.FlowVersion, d.Name, d.ExpectedOutcome, d.Steps, target).Scan(&ids)
	if len(ids) != 1 {
		ids = nil
	}
	return ids, err
}

func missingJourneyFixtures(drafts []ScenarioInput, target string, profiles []FixtureProfile) []string {
	var missing []string
	for _, d := range drafts {
		for _, step := range d.Steps {
			if step.Action != "prepare_fixture" || step.Value == nil {
				continue
			}
			id := *step.Value
			found := slices.ContainsFunc(profiles, func(p FixtureProfile) bool { return p.ID == id && slices.Contains(p.Origins, target) })
			message := fmt.Sprintf("Register the %s test data profile for %s.", id, target)
			if !found && !slices.Contains(missing, message) {
				missing = append(missing, message)
			}
		}
	}
	return missing
}

func (s *Store) RunSavedJourney(ctx context.Context, project, task string, in RunJourneyInput, profiles []FixtureProfile) (SaveJourneyResult, error) {
	var out SaveJourneyResult
	if err := ValidateChatRequest(in.RequestID, in.Prompt); err != nil {
		return out, err
	}
	if len(in.Reference) > 4000 || (in.Mode != "advisory" && in.Mode != "blocking") {
		return out, ErrInvalid
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	fingerprint := fmt.Sprintf("run-journey:%x", sha256.Sum256(raw))
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	var target string
	err = tx.QueryRow(ctx, `SELECT p.base_url FROM projects p JOIN testing_tasks t ON t.project_id=p.id WHERE p.id=$1 AND t.id=$2 FOR UPDATE OF p`, project, task).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	var prior string
	err = tx.QueryRow(ctx, `SELECT fingerprint FROM chat_turns WHERE id=$1`, in.RequestID).Scan(&prior)
	if err == nil {
		if prior != fingerprint {
			return out, ErrConflict
		}
		turn, err := scanChatTurn(tx.QueryRow(ctx, `SELECT `+chatColumns+` FROM chat_turns WHERE id=$1 AND project_id=$2 AND task_id=$3`, in.RequestID, project, task))
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrConflict
		}
		return SaveJourneyResult{Turn: &turn}, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	items, err := savedCandidates(ctx, tx, project)
	if err != nil {
		return out, err
	}
	matches := matchNamedJourneys(items, in.Reference)
	if in.JourneyID != "" {
		i := slices.IndexFunc(items, func(c journeyCandidate) bool { return c.Item == in.JourneyID })
		if i < 0 {
			return out, ErrNotFound
		}
		matches = items[i : i+1]
	}
	receipt := ChatTurn{Reply: "This project has no saved journeys yet. Save a journey from a plan in this chat first."}
	if len(items) > 0 {
		if len(matches) != 1 {
			if len(matches) == 0 {
				matches = items
			}
			for _, m := range matches {
				out.Choices = append(out.Choices, m.journeyChoice)
			}
			return out, nil
		}
		receipt, err = s.prepareJourneyRun(ctx, tx, project, task, target, in.Mode, matches[0], profiles)
		if err != nil {
			return out, err
		}
	}
	turn, err := scanChatTurn(tx.QueryRow(ctx, `INSERT INTO chat_turns(id,project_id,task_id,fingerprint,prompt,status,reply,saved_journey_id,proposal,run_id,journey_setup)
 VALUES($1,$2,$3,$4,$5,'completed',$6,NULLIF($7,''),$8,NULLIF($9,''),$10) RETURNING `+chatColumns, in.RequestID, project, task, fingerprint, in.Prompt, receipt.Reply, receipt.SavedJourneyID, receipt.Proposal, receipt.RunID, receipt.JourneySetup))
	if err != nil {
		return out, err
	}
	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	return SaveJourneyResult{Turn: &turn}, nil
}

func (s *Store) prepareJourneyRun(ctx context.Context, tx pgx.Tx, project, task, target, mode string, flow journeyCandidate, profiles []FixtureProfile) (ChatTurn, error) {
	reply := ChatTurn{SavedJourneyID: flow.Item}
	p, requirements, index, err := journeyDraft(ctx, tx, project, flow.Item)
	if err != nil {
		return reply, err
	}
	reply.Reply = fmt.Sprintf("“%s” needs setup before it can run. No checks started.", flow.Name)
	if len(p.Scenarios) == 0 {
		if len(requirements) == 0 {
			requirements = []string{"Prepare executable checks for the saved outcome."}
		}
		reply.JourneySetup = requirements
		return reply, nil
	}
	ids, err := journeyApproval(ctx, tx, project, target, p, index)
	if err != nil {
		return reply, err
	}
	drafts := p.Scenarios
	var approved []Scenario
	if len(ids) > 0 {
		approved, err = approvedRunScenarios(ctx, tx, project, ids)
		if err != nil {
			return reply, err
		}
		drafts = make([]ScenarioInput, 0, len(approved))
		for _, sc := range approved {
			drafts = append(drafts, sc.ScenarioInput)
		}
	}
	if reply.JourneySetup = missingJourneyFixtures(drafts, target, profiles); len(reply.JourneySetup) > 0 {
		return reply, nil
	}
	if len(approved) == 0 {
		reply.Proposal = &p
		reply.Reply = fmt.Sprintf("Review the saved checks for “%s” before running them on %s.", flow.Name, target)
		return reply, nil
	}
	if _, err := ValidateProject(ProjectInput{Name: "Journey run", BaseURL: target}, s.allowed); err != nil {
		return reply, err
	}
	id, err := newID()
	if err != nil {
		return reply, err
	}
	if _, err := insertRunSnapshot(ctx, tx, id, project, target, mode, task, approved); err != nil {
		return reply, err
	}
	reply.RunID = id
	reply.Reply = fmt.Sprintf("Queued “%s” on %s using its approved checks. Results will appear here.", flow.Name, target)
	return reply, nil
}
