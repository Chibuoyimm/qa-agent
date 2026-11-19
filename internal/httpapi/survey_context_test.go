package httpapi

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
)

func TestSurveyContextIncludesLateJourneys(t *testing.T) {
	sources := make([]qa.SourceSurvey, 2)
	for i := range sources {
		for j := 0; j < 200; j++ {
			sources[i].Journeys = append(sources[i].Journeys, qa.InferredJourney{Name: fmt.Sprintf("Checkout %d", j), Evidence: "Open checkout " + strings.Repeat("界", 900) + " expect receipt"})
		}
	}
	text, refs, err := wholeAppContext("Inspect shopping", sources)
	if err != nil || len(refs) != 400 || !refs["R2.J200"] || len(text) > qa.MaxSurveyContextBytes || !utf8.ValidString(text) || strings.Count(text, "expect receipt") != 400 {
		t.Fatalf("invalid synthesis: %v, %d references, %d bytes", err, len(refs), len(text))
	}
}
