package qa

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var bindingName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
var bindingToken = regexp.MustCompile(`\{\{([a-z][a-z0-9_]{0,39})\}\}`)

func IsJourney(steps []Step) bool {
	for _, step := range steps {
		if step.Action == "phase" || step.Action == "use_actor" || step.Action == "prepare_fixture" {
			return true
		}
	}
	return false
}

func validPointer(pointer string) bool {
	if pointer == "" || len(pointer) > 300 || !strings.HasPrefix(pointer, "/") {
		return false
	}
	for _, part := range strings.Split(pointer[1:], "/") {
		for i := 0; i < len(part); i++ {
			if part[i] == '~' {
				if i+1 == len(part) || (part[i+1] != '0' && part[i+1] != '1') {
					return false
				}
				i++
			}
		}
	}
	return validObservationText(pointer, 300)
}
func identifierPointer(pointer string) bool {
	part := pointer[strings.LastIndex(pointer, "/")+1:]
	return part == "id" || strings.HasSuffix(part, "_id")
}
func safeStepPath(value string) bool {
	return len(value) > 0 && len(value) <= 2048 && strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.ContainsAny(value, "?#\\\r\n\t ") && !strings.Contains(value, "..")
}
func scalarTargetOnly(s Step) bool { return s.TestID == "" && s.Locator == nil && s.SecretEnv == nil }

func validateJourneyStep(s Step) error {
	switch s.Action {
	case "phase", "use_actor", "prepare_fixture", "cleanup_fixture":
		if !scalarTargetOnly(s) || s.Path != "" || s.Value == nil || strings.TrimSpace(*s.Value) == "" || len(*s.Value) > 200 || s.SaveAs != "" || s.JSONPointer != "" || s.ValueRef != "" {
			return errors.New("journey control requires only its name in value")
		}
		if s.Action != "phase" && !bindingName.MatchString(*s.Value) {
			return errors.New("actor and fixture names must be simple identifiers")
		}
	case "capture_id":
		if !scalarTargetOnly(s) || !safeStepPath(s.Path) || !strings.HasSuffix(s.Path, "/") || s.Value != nil || !bindingName.MatchString(s.SaveAs) || s.JSONPointer != "" || s.ValueRef != "" {
			return errors.New("capture_id requires a same-origin path prefix and save_as")
		}
	case "capture_json", "assert_json", "assert_json_number":
		if !scalarTargetOnly(s) || !safeStepPath(s.Path) || !validPointer(s.JSONPointer) {
			return errors.New("JSON reads require a same-origin path and JSON pointer")
		}
		if s.Action == "capture_json" {
			if s.Value != nil || s.ValueRef != "" || !bindingName.MatchString(s.SaveAs) || !identifierPointer(s.JSONPointer) {
				return errors.New("capture_json saves only an identifier field")
			}
			return nil
		}
		if s.SaveAs != "" || (s.Value == nil) == (s.ValueRef == "") {
			return errors.New("JSON assertion requires exactly one expected value")
		}
		if s.Value != nil && len(*s.Value) > 4000 {
			return errors.New("JSON expectation exceeds its size limit")
		}
		if s.ValueRef != "" && (!bindingName.MatchString(s.ValueRef) || !identifierPointer(s.JSONPointer)) {
			return errors.New("only identifier expectations may use a reference")
		}
		if s.Action == "assert_json_number" && (s.Value == nil || !decimalLiteral.MatchString(*s.Value)) {
			return errors.New("numeric assertions require a fixed decimal expectation")
		}
	case "assert_path":
		if !scalarTargetOnly(s) || !safeStepPath(s.Path) || s.Value != nil || s.SaveAs != "" || s.JSONPointer != "" || s.ValueRef != "" {
			return errors.New("assert_path requires only a same-origin path")
		}
	default:
		return errors.New("unsupported journey action")
	}
	return nil
}

var decimalLiteral = regexp.MustCompile(`^[+-]?[0-9]{1,100}(\.[0-9]{1,100})?$`)

// Bindings are declared in plan order and cannot change approved business values.
func validateBindings(steps []Step) error {
	known := map[string]bool{"run_id": true}
	actors := map[string]bool{}
	fixture := ""
	for i, step := range steps {
		values := []string{step.Path}
		if step.Action == "fill" && step.Value != nil {
			values = append(values, *step.Value)
		}
		for _, value := range values {
			for _, match := range bindingToken.FindAllStringSubmatch(value, -1) {
				if !known[match[1]] {
					return fmt.Errorf("step %d references unknown binding %s", i+1, match[1])
				}
			}
			stripped := bindingToken.ReplaceAllString(value, "")
			if strings.Contains(stripped, "{{") || strings.Contains(stripped, "}}") {
				return fmt.Errorf("step %d has a malformed binding", i+1)
			}
		}
		if step.ValueRef != "" && !known[step.ValueRef] {
			return fmt.Errorf("step %d references an unknown identifier", i+1)
		}
		if step.SaveAs != "" {
			if known[step.SaveAs] {
				return errors.New("bindings cannot be overwritten")
			}
			known[step.SaveAs] = true
		}
		if step.Action == "use_actor" {
			actors[*step.Value] = true
			if len(actors) > 5 {
				return errors.New("a journey supports at most five actors")
			}
		}
		if step.Action == "prepare_fixture" {
			if i != 0 {
				return errors.New("prepare_fixture must be the first step")
			}
			fixture = *step.Value
		}
		if step.Action == "cleanup_fixture" {
			if i != len(steps)-1 || fixture == "" || *step.Value != fixture {
				return errors.New("cleanup_fixture must close its prepared fixture as the last step")
			}
		}
	}
	if fixture != "" && steps[len(steps)-1].Action != "cleanup_fixture" {
		return errors.New("a prepared fixture requires an approved cleanup step")
	}
	return nil
}

func ValidateAccessSteps(steps []Step) error {
	for _, step := range steps {
		switch step.Action {
		case "navigate", "fill", "click", "assert_text", "assert_visible":
		default:
			return fmt.Errorf("%w: choose a dedicated sign-in check instead of a complete journey", ErrInvalid)
		}
	}
	return nil
}
