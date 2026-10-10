package projectcheck

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/project"
)

// The draft is the record a project is missing, holding only what this run
// proved: the services that are declared and used, and the gate's test
// command as the fast tier. A command an instruction file names is prose
// until someone runs it, so none becomes a tier here. It names credentials
// and never holds one.
func TestTheDraftedRecordHoldsWhatWasProvenAndNoValue(t *testing.T) {
	// Arrange
	secret := "sk_live_" + strings.Repeat("a1B2", 8)
	f := newFixture(t, map[string]string{
		".gitignore":           ".env\n",
		".no-mistakes.yaml":    "commands:\n  test: \"python scripts/gate_test.py\"\n",
		"scripts/gate_test.py": "print('gate')\n",
		"AGENTS.md":            "Test with `go test ./...` and `python scripts/gate_test.py`, lint with `go vet ./...`, and never `go test ./internal/gone/...`.\n",
		"app/pay.py":           "import os\nkey = os.environ[\"STRIPE_SECRET_KEY\"]\ndb = os.environ[\"DATABASE_URL\"]\n",
	})
	f.write(".env", "STRIPE_SECRET_KEY="+secret+"\n")
	f.manifest("auth.json", `{"project":"northwind","services":[
		{"name":"stripe","method":"env","env":["STRIPE_SECRET_KEY"]},
		{"name":"postgres","method":"env","env":["DATABASE_URL"]},
		{"name":"resend","method":"env","env":["RESEND_API_KEY"]}
	]}`)

	// Act
	draft := f.check("python", "go").Draft

	// Assert
	if err := draft.Validate(); err != nil {
		t.Fatalf("the loader refuses the draft: %v", err)
	}
	want := project.Manifest{
		Project: "northwind",
		Services: []project.Service{
			{Name: "stripe", Kind: "external", Provider: "stripe", Env: []string{"STRIPE_SECRET_KEY"}},
			{Name: "postgres", Kind: "database", Provider: "postgres", Env: []string{"DATABASE_URL"}},
		},
		Verification: project.Verification{Fast: []project.Command{{"python", "scripts/gate_test.py"}}},
	}
	if !reflect.DeepEqual(draft, want) {
		got, _ := json.MarshalIndent(draft, "", "  ")
		t.Errorf("the draft is\n%s\nwant the used services and the gate's test command as the fast tier, and nothing read out of prose", got)
	}
	if data, _ := json.Marshal(draft); strings.Contains(string(data), "a1B2") {
		t.Errorf("the draft holds a credential's value: %s", data)
	}
}

// A record that is there keeps what it holds: the draft fills only what it
// leaves empty, so routing a person tuned is never drafted away.
func TestTheDraftKeepsWhatAnExistingRecordHolds(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		".no-mistakes.yaml":    "commands:\n  test: \"python scripts/gate_test.py\"\n",
		"scripts/gate_test.py": "print('gate')\n",
	})
	f.manifest("project.json", `{"project":"northwind","verification":{"full":[["go","test","./..."]]},"routing":{"default_lane":"open","lanes":{"open":{"harness":"pi"}}}}`)

	// Act
	draft := f.check("python", "go").Draft

	// Assert
	if draft.Routing.DefaultLane != "open" || draft.Routing.Lanes["open"].Harness != "pi" {
		t.Errorf("the draft dropped the record's routing: %+v", draft.Routing)
	}
	if !reflect.DeepEqual(draft.Verification.Full, []project.Command{{"go", "test", "./..."}}) {
		t.Errorf("the draft changed the record's full tier: %v", draft.Verification.Full)
	}
	if !reflect.DeepEqual(draft.Verification.Fast, []project.Command{{"python", "scripts/gate_test.py"}}) {
		t.Errorf("the draft's fast tier is %v, want the gate's test command, which the record left empty", draft.Verification.Fast)
	}
}
