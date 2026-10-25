package qa

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureMetadataNeverReturnsCommandsOrEnvironment(t *testing.T) {
	file := filepath.Join(t.TempDir(), "profiles.json")
	raw := `[{"id":"escrow_local","name":"Local escrow","description":"Fresh artificial customers","origins":["http://127.0.0.1:3000"],"secret_refs":["QA_TEST_PASSWORD"],"prepare":["/private/operator-command","private-command-argument"],"cleanup":["/private/cleanup"],"environment":["QA_FIXTURE_DATABASE_URL"]}]`
	if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	profiles, err := LoadFixtureProfiles(file)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(profiles)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "private") || strings.Contains(string(body), "QA_FIXTURE_DATABASE_URL") {
		t.Fatal("private worker configuration exposed")
	}
	if !strings.Contains(string(body), "QA_TEST_PASSWORD") {
		t.Fatal("test credential references missing")
	}
}
