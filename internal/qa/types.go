package qa

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
)

var (
	ErrInvalid  = errors.New("invalid input")
	ErrNotFound = errors.New("record not found")
	ErrConflict = errors.New("invalid state")
)

type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	BaseURL   string    `json:"base_url"`
	CreatedAt time.Time `json:"created_at"`
}

type ProjectInput struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
}

type Step struct {
	Action    string          `json:"action"`
	Path      string          `json:"path,omitempty"`
	TestID    string          `json:"test_id,omitempty"`
	Locator   *ElementLocator `json:"locator,omitempty"`
	Value     *string         `json:"value,omitempty"`
	SecretEnv *string         `json:"secret_env,omitempty"`
}

// ElementLocator uses exact accessible names; it never accepts CSS or script.
type ElementLocator struct {
	Role  string `json:"role,omitempty"`
	Name  string `json:"name,omitempty"`
	Label string `json:"label,omitempty"`
}

func (l ElementLocator) Valid() bool {
	if l.Label != "" {
		return l.Role == "" && l.Name == "" && validLocatorName(l.Label)
	}
	return validLocatorRole(l.Role) && validLocatorName(l.Name)
}

func validLocatorName(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 300 && validObservationText(value, 300)
}

func validLocatorRole(role string) bool {
	switch role {
	case "button", "link", "textbox", "heading", "checkbox", "radio", "combobox", "option", "tab", "menuitem", "alert", "status", "dialog", "region", "switch", "spinbutton", "searchbox":
		return true
	}
	return false
}

func (s Step) validTarget() bool {
	if s.Locator != nil {
		return s.TestID == "" && s.Locator.Valid()
	}
	return validTestID(s.TestID)
}

func (s Step) TargetName() string {
	if s.Locator != nil {
		if s.Locator.Label != "" {
			return s.Locator.Label
		}
		return s.Locator.Role + " “" + s.Locator.Name + "”"
	}
	return s.TestID
}

type ScenarioInput struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	ExpectedOutcome string `json:"expected_outcome"`
	Approved        bool   `json:"approved"`
	Steps           []Step `json:"steps"`
}

type Scenario struct {
	JourneyID string `json:"journey_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	ScenarioInput
	CreatedAt time.Time `json:"created_at"`
}

type Artifact struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

type ExecutionStepResult struct {
	Index      int    `json:"index"`
	Action     string `json:"action"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Message    string `json:"message,omitempty"`
	Observed   string `json:"observed,omitempty"`
}

type ScenarioResult struct {
	StepResults []ExecutionStepResult `json:"step_results,omitempty"`
	ScenarioID  string                `json:"scenario_id"`
	Status      string                `json:"status"`
	Message     string                `json:"message"`
	DurationMS  int64                 `json:"duration_ms"`
	Artifacts   []Artifact            `json:"artifacts"`
}

type Run struct {
	TaskID      string           `json:"task_id,omitempty"`
	SourceRunID string           `json:"source_run_id,omitempty"`
	ID          string           `json:"id"`
	ProjectID   string           `json:"project_id"`
	BaseURL     string           `json:"base_url"`
	Mode        string           `json:"mode"`
	Status      string           `json:"status"`
	Gate        string           `json:"gate"`
	Scenarios   []Scenario       `json:"scenarios"`
	Results     []ScenarioResult `json:"results"`
	CreatedAt   time.Time        `json:"created_at"`
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	FinishedAt  *time.Time       `json:"finished_at,omitempty"`
}

type RunInput struct {
	ScenarioIDs []string `json:"scenario_ids"`
	Mode        string   `json:"mode"`
}

type CompleteInput struct {
	LeaseToken string           `json:"lease_token"`
	Results    []ScenarioResult `json:"results"`
}

func ValidateProject(in ProjectInput, allowed map[string]bool) (ProjectInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 {
		return in, fmt.Errorf("%w: project name must contain 1–200 characters", ErrInvalid)
	}
	u, err := url.Parse(in.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawFragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.Opaque != "" {
		return in, fmt.Errorf("%w: base_url must be an allowed HTTP(S) origin", ErrInvalid)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return in, fmt.Errorf("%w: base_url must use HTTP(S)", ErrInvalid)
	}
	origin := u.Scheme + "://" + u.Host
	if !allowed[origin] {
		return in, fmt.Errorf("%w: base_url origin is not allowed", ErrInvalid)
	}
	in.BaseURL = origin
	return in, nil
}

func ParseAllowedOrigins(raw string) (map[string]bool, error) {
	allowed := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		u, err := url.Parse(part)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
			return nil, fmt.Errorf("invalid QA_ALLOWED_ORIGINS entry %q", part)
		}
		allowed[part] = true
	}
	if len(allowed) == 0 {
		return nil, errors.New("QA_ALLOWED_ORIGINS must contain at least one HTTP(S) origin")
	}
	return allowed, nil
}

func ValidateScenario(in ScenarioInput) (ScenarioInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.ExpectedOutcome = strings.TrimSpace(in.ExpectedOutcome)
	if in.Name == "" || len(in.Name) > 200 || len(in.Description) > 4000 || in.ExpectedOutcome == "" || len(in.ExpectedOutcome) > 4000 {
		return in, fmt.Errorf("%w: scenario requires a name (1–200), description (up to 4000), and expected_outcome (1–4000)", ErrInvalid)
	}
	if len(in.Steps) < 1 || len(in.Steps) > 50 {
		return in, fmt.Errorf("%w: scenario must have 1–50 steps", ErrInvalid)
	}
	assertion := false
	for i, step := range in.Steps {
		if err := validateStep(step); err != nil {
			return in, fmt.Errorf("%w: step %d: %v", ErrInvalid, i+1, err)
		}
		if step.Action == "assert_text" || step.Action == "assert_visible" {
			assertion = true
		}
	}
	if in.Approved && !assertion {
		return in, fmt.Errorf("%w: approved scenario requires an assertion", ErrInvalid)
	}
	return in, nil
}

func validateStep(s Step) error {
	switch s.Action {
	case "navigate":
		if len(s.Path) > 2048 || !strings.HasPrefix(s.Path, "/") || strings.HasPrefix(s.Path, "//") || strings.ContainsAny(s.Path, "?#\\") || strings.Contains(s.Path, "..") || s.TestID != "" || s.Locator != nil || s.Value != nil || s.SecretEnv != nil {
			return errors.New("navigate requires only a safe same-origin path")
		}
	case "fill":
		if s.Path != "" || !s.validTarget() || (s.Value == nil) == (s.SecretEnv == nil) {
			return errors.New("fill requires one valid target and exactly one of value or secret_env")
		}
		if s.Value != nil && len(*s.Value) > 4000 {
			return errors.New("fill value is too long")
		}
		if s.SecretEnv != nil && !validSecretEnv(*s.SecretEnv) {
			return errors.New("secret_env must be a QA_TEST_ environment name")
		}
	case "click", "assert_visible":
		if s.Path != "" || !s.validTarget() || s.Value != nil || s.SecretEnv != nil {
			return errors.New("action requires only one valid target")
		}
	case "assert_text":
		if s.Path != "" || !s.validTarget() || s.Value == nil || s.SecretEnv != nil || len(*s.Value) > 4000 {
			return errors.New("assert_text requires one valid target and exact value")
		}
	default:
		return errors.New("unsupported action")
	}
	return nil
}

func validTestID(s string) bool {
	if s == "" || len(s) > 200 {
		return false
	}
	for _, r := range s {
		if r < 0x21 || r > 0x7e || r == '\'' || r == '"' || r == '\\' {
			return false
		}
	}
	return true
}

func validSecretEnv(s string) bool {
	if !strings.HasPrefix(s, "QA_TEST_") || len(s) <= len("QA_TEST_") || len(s) > 100 {
		return false
	}
	for _, r := range s[len("QA_TEST_"):] {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func ValidateResults(scenarios []Scenario, results []ScenarioResult) (string, error) {
	if len(results) != len(scenarios) {
		return "", fmt.Errorf("%w: expected exactly one result per scenario", ErrInvalid)
	}
	known := make(map[string]Scenario, len(scenarios))
	for _, scenario := range scenarios {
		known[scenario.ID] = scenario
	}
	seen := make(map[string]bool, len(results))
	status := "passed"
	for _, result := range results {
		scenario, exists := known[result.ScenarioID]
		if !exists || seen[result.ScenarioID] {
			return "", fmt.Errorf("%w: result has an unknown or duplicate scenario_id", ErrInvalid)
		}
		seen[result.ScenarioID] = true
		if len(result.StepResults) > len(scenario.Steps) || (len(result.StepResults) > 0 && result.Status == "passed" && len(result.StepResults) != len(scenario.Steps)) {
			return "", fmt.Errorf("%w: step records do not match the scenario length", ErrInvalid)
		}
		for i, step := range result.StepResults {
			if step.Index != i || step.Action != scenario.Steps[i].Action || step.DurationMS < 0 || step.DurationMS > 3600000 || len(step.Message) > 1000 || len(step.Observed) > 4000 || (step.Status != "passed" && step.Status != "failed" && step.Status != "blocked" && step.Status != "error") || (result.Status == "passed" && step.Status != "passed") || (i < len(result.StepResults)-1 && step.Status != "passed") || (step.Observed != "" && (step.Action != "assert_text" || step.Status != "failed")) {
				return "", fmt.Errorf("%w: invalid execution step result", ErrInvalid)
			}
		}
		if len(result.Message) > 4000 || result.DurationMS < 0 || result.DurationMS > 3_600_000 || len(result.Artifacts) > 20 {
			return "", fmt.Errorf("%w: result message, duration, or artifacts exceed limits", ErrInvalid)
		}
		for _, artifact := range result.Artifacts {
			if (artifact.Kind != "screenshot" && artifact.Kind != "video") || !validArtifactPath(artifact.Path) {
				return "", fmt.Errorf("%w: invalid artifact", ErrInvalid)
			}
		}
		switch result.Status {
		case "passed":
		case "failed":
			if status == "passed" {
				status = "failed"
			}
		case "blocked":
			if status != "error" {
				status = "blocked"
			}
		case "error":
			status = "error"
		default:
			return "", fmt.Errorf("%w: unsupported result status", ErrInvalid)
		}
	}
	return status, nil
}

func validArtifactPath(p string) bool {
	if p == "" || len(p) > 1024 || strings.Contains(p, ":") || strings.Contains(p, "\\") || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	return true
}
