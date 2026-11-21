package qa

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
)

// JourneyPlan retains the selected draft and its provenance without approving any steps.
type JourneyPlan struct {
	TaskID          string   `json:"task_id"`
	TurnID          string   `json:"turn_id"`
	Item            string   `json:"item"`
	ExpectedOutcome string   `json:"expected_outcome"`
	MissingSetup    []string `json:"missing_setup,omitempty"`
	Steps           []Step   `json:"steps,omitempty"`
	SourceRefs      []string `json:"source_refs,omitempty"`
}

func (s *Store) SavedJourneyPlan(ctx context.Context, project, journey string) (*JourneyPlan, error) {
	var plan *JourneyPlan
	err := s.db.QueryRow(ctx, `SELECT saved_plan FROM journeys WHERE project_id=$1 AND id=$2`, project, journey).Scan(&plan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return plan, err
}

var ErrJourneyNameTaken = errors.New("a different plan is already tracked under this journey name in Coverage")

type SaveJourneyInput struct {
	RequestID string `json:"request_id"`
	Prompt    string `json:"prompt"`
	Reference string `json:"reference"`
	TurnID    string `json:"turn_id,omitempty"`
	Item      string `json:"item,omitempty"`
}

type journeyChoice struct {
	Item string `json:"item"`
	Name string `json:"name"`
}
type SaveJourneyResult struct {
	Turn       *ChatTurn       `json:"turn,omitempty"`
	PlanTurnID string          `json:"plan_turn_id,omitempty"`
	Choices    []journeyChoice `json:"choices,omitempty"`
}

type journeyCandidate struct {
	journeyChoice
	plan    JourneyPlan
	aliases []string
}

func planCandidates(t ChatTurn) []journeyCandidate {
	var items []journeyCandidate
	p := t.Proposal
	for i, s := range p.Scenarios {
		key := fmt.Sprintf("ready:%d", i)
		plan := JourneyPlan{TaskID: t.TaskID, TurnID: t.ID, Item: key, ExpectedOutcome: s.ExpectedOutcome, Steps: s.Steps}
		if p.WholeApp != nil && i < len(p.WholeApp.Coverage) {
			plan.SourceRefs = p.WholeApp.Coverage[i]
		}
		items = append(items, journeyCandidate{journeyChoice: journeyChoice{key, s.Name}, plan: plan})
	}
	if p.WholeApp != nil {
		for i, s := range p.WholeApp.NeedsSetup {
			key := fmt.Sprintf("setup:%d", i)
			items = append(items, journeyCandidate{journeyChoice: journeyChoice{key, s.Name}, plan: JourneyPlan{TaskID: t.TaskID, TurnID: t.ID, Item: key, ExpectedOutcome: s.ExpectedOutcome, MissingSetup: s.MissingSetup, SourceRefs: s.SourceRefs}})
		}
	}
	aliases := map[string]string{}
	if p.WholeApp != nil {
		for i, s := range p.WholeApp.Sources {
			for j, v := range s.Journeys {
				aliases[JourneyRef(i, j)] = v.Name
			}
		}
	}
	for i := range items {
		for _, ref := range items[i].plan.SourceRefs {
			items[i].aliases = append(items[i].aliases, aliases[ref])
		}
	}
	return items
}

// Match short descriptions against both the reviewed titles and their cited source journeys.
func journeyWords(s string) []string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := words[:0]
	for _, w := range words {
		switch w {
		case "the", "an", "a", "to", "of", "in", "from", "and", "or", "its", "as", "for", "this", "that", "my", "journey", "flow", "check", "workflow":
			continue
		}
		for _, end := range []string{"ance", "ing", "ed", "s"} {
			if strings.HasSuffix(w, end) && len(w)-len(end) >= 3 {
				w = strings.TrimSuffix(w, end)
				break
			}
		}
		out = append(out, w)
	}
	return out
}

func titleMatch(title string, query []string) int {
	words := journeyWords(title)
	if len(words) == 0 || len(query) == 0 {
		return 0
	}
	for _, q := range query {
		if !slices.Contains(words, q) {
			return 0
		}
	}
	// A leading action and nearby terms distinguish performing an action from merely mentioning it.
	score := 1
	if slices.Contains(query, words[0]) {
		score += 2
	}
	for i := 0; i+len(query) <= len(words); i++ {
		contiguous := true
		for _, q := range query {
			if !slices.Contains(words[i:i+len(query)], q) {
				contiguous = false
				break
			}
		}
		if contiguous {
			score++
			break
		}
	}
	return score
}

func matchJourneys(items []journeyCandidate, ref string) []journeyCandidate {
	if len(items) == 1 && strings.TrimSpace(ref) == "" {
		return items
	}
	query := journeyWords(ref)
	var matches []journeyCandidate
	best := 0
	for _, item := range items {
		score := titleMatch(item.Name, query)
		for _, alias := range item.aliases {
			score = max(score, titleMatch(alias, query))
		}
		// Generic nouns should offer all matching flows.
		if len(query) == 1 && score > 0 {
			score = 1
		}
		if strings.EqualFold(strings.TrimSpace(ref), item.Name) {
			score = 10
		}
		if score == 0 || score < best {
			continue
		}
		if score > best {
			best = score
			matches = nil
		}
		matches = append(matches, item)
	}
	return matches
}

func (s *Store) SavePlanJourney(ctx context.Context, project, task string, in SaveJourneyInput) (SaveJourneyResult, error) {
	var result SaveJourneyResult
	if err := ValidateChatRequest(in.RequestID, in.Prompt); err != nil {
		return result, err
	}
	if len(in.Reference) > 4000 || (in.Item != "" && in.TurnID == "") {
		return result, ErrInvalid
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return result, err
	}
	fingerprint := fmt.Sprintf("save-journey:%x", sha256.Sum256(raw))
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	var parentName string
	err = tx.QueryRow(ctx, `SELECT j.name FROM testing_tasks t JOIN journeys j ON j.id=t.journey_id WHERE t.project_id=$1 AND t.id=$2 FOR UPDATE OF t`, project, task).Scan(&parentName)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	var prior string
	err = tx.QueryRow(ctx, `SELECT fingerprint FROM chat_turns WHERE id=$1`, in.RequestID).Scan(&prior)
	if err == nil {
		if prior != fingerprint {
			return result, ErrConflict
		}
		turn, err := scanChatTurn(tx.QueryRow(ctx, `SELECT `+chatColumns+` FROM chat_turns WHERE project_id=$1 AND task_id=$2 AND id=$3`, project, task, in.RequestID))
		if errors.Is(err, pgx.ErrNoRows) {
			return result, ErrConflict
		}
		return SaveJourneyResult{Turn: &turn}, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}

	src, err := scanChatTurn(tx.QueryRow(ctx, `SELECT `+chatColumns+` FROM chat_turns WHERE project_id=$1 AND task_id=$2 AND status='completed' AND proposal IS NOT NULL
   AND ($3='' OR id=$3) AND (jsonb_array_length(NULLIF(proposal->'scenarios','null'::jsonb))>0 OR jsonb_array_length(NULLIF(proposal #> '{whole_app,needs_setup}','null'::jsonb))>0)
   ORDER BY sequence DESC LIMIT 1`, project, task, in.TurnID))
	if errors.Is(err, pgx.ErrNoRows) {
		return result, fmt.Errorf("%w: open a chat with a proposed journey to save", ErrInvalid)
	}
	if err != nil {
		return result, err
	}
	items := planCandidates(src)
	var matches []journeyCandidate
	if in.Item != "" {
		for _, item := range items {
			if item.Item == in.Item {
				matches = append(matches, item)
				break
			}
		}
		if len(matches) == 0 {
			return result, ErrInvalid
		}
	} else {
		matches = matchJourneys(items, in.Reference)
		if len(matches) != 1 {
			if len(matches) == 0 {
				matches = items
			}
			// An unmatched description always needs confirmation before saving.
			result.PlanTurnID = src.ID
			for _, m := range matches {
				result.Choices = append(result.Choices, m.journeyChoice)
			}
			return result, nil
		}
	}
	selected := matches[0]
	planJSON, err := json.Marshal(selected.plan)
	if err != nil {
		return result, err
	}
	id, err := newID()
	if err != nil {
		return result, err
	}
	name := selected.Name
	if strings.EqualFold(name, parentName) {
		name += " (saved journey)"
	}
	var existingPlan JourneyPlan
	err = tx.QueryRow(ctx, `INSERT INTO journeys(id,project_id,name,saved_plan) VALUES($1,$2,$3,$4)
   ON CONFLICT(project_id,lower(name)) DO UPDATE SET saved_plan=COALESCE(journeys.saved_plan,excluded.saved_plan)
   RETURNING id,saved_plan`, id, project, name, planJSON).Scan(&id, &existingPlan)
	if err != nil {
		return result, err
	}
	if existingPlan.TurnID != src.ID || existingPlan.Item != selected.Item {
		return result, ErrJourneyNameTaken
	}
	reply := fmt.Sprintf("Saved “%s” as a separate journey in Coverage.", name)
	turn, err := scanChatTurn(tx.QueryRow(ctx, `INSERT INTO chat_turns(id,project_id,task_id,fingerprint,prompt,status,reply,saved_journey_id)
   VALUES($1,$2,$3,$4,$5,'completed',$6,$7) RETURNING `+chatColumns, in.RequestID, project, task, fingerprint, in.Prompt, reply, id))
	if err != nil {
		return result, err
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return SaveJourneyResult{Turn: &turn}, nil
}
