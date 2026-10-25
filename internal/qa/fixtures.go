package qa

import (
	"encoding/json"
	"fmt"
	"os"
)

// FixtureProfile is the public metadata for an engineer-installed worker routine.
// Commands and environment values are never returned by the API or sent to AI.
type FixtureProfile struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Origins     []string `json:"origins"`
	SecretRefs  []string `json:"secret_refs"`
}

func LoadFixtureProfiles(path string) ([]FixtureProfile, error) {
	profiles := []FixtureProfile{}
	if path == "" {
		return profiles, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) > 65536 {
		return nil, fmt.Errorf("fixture configuration is too large")
	}
	if err = json.Unmarshal(raw, &profiles); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	if len(profiles) > 20 {
		return nil, fmt.Errorf("at most twenty fixture profiles are supported")
	}
	for _, p := range profiles {
		if !bindingName.MatchString(p.ID) || seen[p.ID] || p.Name == "" || len(p.Name) > 100 || len(p.Description) > 1000 || len(p.Origins) == 0 || len(p.Origins) > 20 || len(p.SecretRefs) > 50 {
			return nil, fmt.Errorf("invalid fixture profile metadata")
		}
		seen[p.ID] = true
		for _, origin := range p.Origins {
			if _, err := ParseAllowedOrigins(origin); err != nil {
				return nil, err
			}
		}
		for _, ref := range p.SecretRefs {
			if !validSecretEnv(ref) {
				return nil, fmt.Errorf("fixture credentials must use QA_TEST_ references")
			}
		}
	}
	return profiles, nil
}
