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

const scripts = `{"scripts":{"cms:validate":"node validate.mjs","test":"node --test","test:marketing":"node --test marketing","lint":"eslint ."}}`

// The check's own draft has to pass the check. A gate test command of two
// parts was dropped, and with no test command in the gate file a draft had
// no verification tier at all, which the record area reports: every draft of
// a run over eleven projects failed it. The fast tier is the gate's test
// command, one command for each part, then the test commands the workflows
// run, then those the instruction files name, and the report says which.
func TestTheDraftPassesTheRecordAreaOncePlaced(t *testing.T) {
	for _, test := range []struct {
		name    string
		tracked map[string]string
		fast    []project.Command
		from    string
	}{
		{
			name: "a gate test command of two parts",
			tracked: map[string]string{
				".no-mistakes.yaml": "commands:\n  test: \"npm run cms:validate && npm test\"\n",
				"package.json":      scripts,
				"AGENTS.md":         "Test with `npm run test:marketing`.\n",
			},
			fast: []project.Command{{"npm", "run", "cms:validate"}, {"npm", "test"}},
			from: "commands.test of .no-mistakes.yaml, as 2 commands",
		},
		{
			name: "no gate test command and a workflow that runs the tests",
			tracked: map[string]string{
				".no-mistakes.yaml": "auto_fix:\n  review: 0\n",
				"package.json":      scripts,
				"AGENTS.md":         "Test with `npm run test:marketing`.\n",
				".github/workflows/ci.yml": "on: pull_request\njobs:\n  verify:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - run: npm ci\n      - run: npm run lint\n" +
					"      - run: |\n          npm test\n          npm run test:gone\n          FORCE_COLOR=1 npm run test:marketing\n      - run: npm test\n        working-directory: web\n",
			},
			fast: []project.Command{{"npm", "test"}},
			from: "the 1 test command the workflows run at the repository's root (.github/workflows/ci.yml)",
		},
		{
			name: "neither, and instructions that name a test command",
			tracked: map[string]string{
				"package.json":     scripts,
				"web/package.json": `{"scripts":{"test:web":"vitest run"}}`,
				"AGENTS.md":        "Lint with `npm run lint`, test with `npm run test:marketing` and `npm run test:web`, deploy with `npm run deploy`.\n",
			},
			fast: []project.Command{{"npm", "run", "test:marketing"}},
			from: "the 1 test command the instruction files name (AGENTS.md)",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, test.tracked)

			// Act
			report := f.check("npm")

			// Assert
			if !reflect.DeepEqual(report.Draft.Verification.Fast, test.fast) {
				t.Errorf("the draft's fast tier is %v, want %v", report.Draft.Verification.Fast, test.fast)
			}
			contains(t, "draft tier", report.DraftTier, test.from, "This check ran none of them")
			placed, err := json.Marshal(report.Draft)
			if err != nil {
				t.Fatal(err)
			}
			f.manifest("project.json", string(placed))
			again := f.check("npm")
			none(t, again, "tiers-empty")
			if !again.Passed(AreaRecord) {
				t.Errorf("the placed draft does not pass the record area:\n%s", again.Only([]string{AreaRecord}).Text())
			}
		})
	}
}

// Where a repository names no test command anywhere, there is none to vouch
// for, and the report says so in place of drafting a record that would fail.
func TestADraftWithNoVerificationCommandSaysWhy(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{"AGENTS.md": "Lint with `npm run lint`.\n", "package.json": scripts})

	// Act
	report := f.check("npm")

	// Assert
	if report.DraftVerifies() {
		t.Errorf("the draft verifies with %v, and the repository names no test command", report.Draft.Verification)
	}
	contains(t, "draft tier", report.DraftTier, "names no verification command", "cfo verify pass with nothing run")
}
