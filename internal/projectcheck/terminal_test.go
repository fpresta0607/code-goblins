package projectcheck

import (
	"strings"
	"testing"
)

// terminalAuth declares four services with no value in it, as every auth
// manifest does: two a task carries whatever its brief says, and two it
// carries only when its brief names them.
const terminalAuth = `{"project":"northwind","services":[
	{"name":"github","method":"cli","env":["GITHUB_TOKEN"],"probe":["gh","auth","status"],"default":true},
	{"name":"postgres","method":"env","env":["DATABASE_URL"],"default":true},
	{"name":"plaid","method":"env","env":["PLAID_CLIENT_ID","PLAID_SECRET"]},
	{"name":"supabase","method":"env","env":["NEXT_PUBLIC_SUPABASE_URL","SUPABASE_SERVICE_ROLE_KEY"],"optional":true}
]}`

// terminalSource reads each credential of terminalAuth but the token of the
// command line tool, which no repository names.
var terminalSource = map[string]string{
	"src/db.py":     "import os\n\nurl = os.environ[\"DATABASE_URL\"]\n",
	"src/plaid.py":  "import os\n\nclient = os.environ[\"PLAID_CLIENT_ID\"]\nsecret = os.environ[\"PLAID_SECRET\"]\n",
	"src/admin.py":  "import os\n\nkey = os.environ[\"SUPABASE_SERVICE_ROLE_KEY\"]\nurl = os.environ[\"NEXT_PUBLIC_SUPABASE_URL\"]\n",
	"tests/test.py": "def test_nothing():\n    pass\n",
}

// terminalFiles returns terminalSource with more files beside it.
func terminalFiles(more map[string]string) map[string]string {
	files := map[string]string{}
	for name, content := range terminalSource {
		files[name] = content
	}
	for name, content := range more {
		files[name] = content
	}
	return files
}

// An env file is one of two roads a test run has to production. A spawn
// also writes the stored credentials of a task's services into the task's
// terminal, and a test run inherits those with no env file at all. The check
// read env files only. It now reads auth.json and names what a terminal
// carries that the repository reads and no test setup names: what every task
// carries by default, and what a task carries when its brief names a service.
func TestTheCredentialsATasksTerminalCarriesAreWithinATestRunsReach(t *testing.T) {
	// Arrange
	f := newFixture(t, terminalFiles(map[string]string{".no-mistakes.yaml": "commands:\n  test: \"pytest -q\"\n"}))
	f.manifest("auth.json", terminalAuth)

	// Act
	report := f.check("pytest")

	// Assert
	finding := only(t, report, "terminal-reaches-production")
	if finding.Severity != High || finding.Area != AreaGate {
		t.Errorf("terminal-reaches-production is %s in %s, want high in gate", finding.Severity, finding.Area)
	}
	contains(t, "says", finding.Says, "4 credentials", "every task")
	byDefault, named, split := strings.Cut(finding.Evidence, "Carried when a brief names the service: ")
	if !split {
		t.Fatalf("evidence %q does not say what a task carries when its brief names a service", finding.Evidence)
	}
	contains(t, "what every task carries", byDefault, "whose brief has no credentials line", "postgres: DATABASE_URL (src/db.py)")
	contains(t, "what a named service carries", named, "plaid: PLAID_CLIENT_ID (src/plaid.py), PLAID_SECRET (src/plaid.py)", "supabase: SUPABASE_SERVICE_ROLE_KEY (src/admin.py)", "The credential store is not opened")
	for _, wrong := range []string{"GITHUB_TOKEN", "NEXT_PUBLIC_SUPABASE_URL"} {
		if strings.Contains(finding.Evidence, wrong) {
			t.Errorf("evidence %q names %s, which no tracked file reads or which is publishable", finding.Evidence, wrong)
		}
	}
	examined := only(t, report, "terminal-credentials-examined")
	contains(t, "says", examined.Says, "4 services", "5 variables", "4 of them read by the repository", "0 of those named by the test setup")
	contains(t, "evidence", examined.Evidence, "Read by no tracked file: github: GITHUB_TOKEN", "Left out as publishable: NEXT_PUBLIC_SUPABASE_URL", "Optional, so carried only when stored: supabase")
}

// How bad the line is follows who carries the credential and what bounds the
// test step, by the rule the env file line uses.
func TestHowBadATerminalsReachIsFollowsWhoCarriesItAndWhatBoundsTheTestStep(t *testing.T) {
	const named = `{"project":"northwind","services":[{"name":"plaid","method":"env","env":["PLAID_CLIENT_ID","PLAID_SECRET"]}]}`
	const gate = "commands:\n  test: \"pytest -q\"\n"
	for _, test := range []struct {
		name, auth, gate string
		severity         Severity
		says             string
	}{
		{"carried by every task, behind the repository's own test command", terminalAuth, gate, High, "every task"},
		{"carried by every task, with the test step an agent's choice", terminalAuth, "", Critical, "every task"},
		{"carried only when a brief names the service", named, gate, Medium, "whose brief names"},
		{"carried only when a brief names the service, with the test step an agent's choice", named, "", Medium, "whose brief names"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			files := terminalFiles(nil)
			if test.gate != "" {
				files[".no-mistakes.yaml"] = test.gate
			}
			f := newFixture(t, files)
			f.manifest("auth.json", test.auth)

			// Act
			report := f.check("pytest")

			// Assert
			finding := only(t, report, "terminal-reaches-production")
			if finding.Severity != test.severity {
				t.Errorf("terminal-reaches-production is %s, want %s", finding.Severity, test.severity)
			}
			contains(t, "says", finding.Says, test.says)
		})
	}
}

// A credential is within a test run's reach when something a test run starts
// reads it. A test file is such a reader. A document, an env example and a
// workflow, where GitHub supplies the value, are not.
func TestOnlyWhatATestRunStartsCountsAsReadingATerminalsCredential(t *testing.T) {
	const auth = `{"project":"northwind","services":[{"name":"postgres","method":"env","env":["DATABASE_URL"],"default":true}]}`
	for _, test := range []struct {
		name, file, content string
		reads               bool
	}{
		{"a test file", "tests/test_db.py", "import os\n\nurl = os.environ[\"DATABASE_URL\"]\n", true},
		{"application code", "src/db.py", "import os\n\nurl = os.environ[\"DATABASE_URL\"]\n", true},
		{"a document", "docs/setup.md", "Set `DATABASE_URL` first.\n", false},
		{"an env example", ".env.example", "DATABASE_URL=\n", false},
		{"a file that is no source code", "deploy/reference.yml", "env:\n  DATABASE_URL: postgres://localhost/app\n", false},
		{"a workflow", ".github/workflows/ci.yml", "on: pull_request\njobs:\n  test:\n    runs-on: ubuntu-latest\n    env:\n      DATABASE_URL: postgres://localhost/test\n    steps:\n      - run: pytest -q\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, map[string]string{test.file: test.content, ".no-mistakes.yaml": "commands:\n  test: \"pytest -q\"\n"})
			f.manifest("auth.json", auth)

			// Act
			report := f.check("pytest")

			// Assert
			if !test.reads {
				none(t, report, "terminal-reaches-production")
				contains(t, "evidence", only(t, report, "terminal-credentials-examined").Evidence, "Read by no tracked file: postgres: DATABASE_URL")
				return
			}
			contains(t, "evidence", only(t, report, "terminal-reaches-production").Evidence, "DATABASE_URL ("+test.file+")")
		})
	}
}

// A variable the test setup names is one the project pinned for its tests,
// so a test run does not use what the terminal carries for it. The line that
// passes still says what it examined.
func TestATerminalsCredentialTheTestSetupNamesIsLeftOut(t *testing.T) {
	// Arrange
	f := newFixture(t, terminalFiles(map[string]string{
		".no-mistakes.yaml": "commands:\n  test: \"pytest -q\"\n",
		"conftest.py":       "import os\n\nfor name in (\"DATABASE_URL\", \"PLAID_CLIENT_ID\", \"PLAID_SECRET\", \"SUPABASE_SERVICE_ROLE_KEY\"):\n    os.environ[name] = \"\"\n",
	}))
	f.manifest("auth.json", terminalAuth)

	// Act
	report := f.check("pytest")

	// Assert
	none(t, report, "terminal-reaches-production")
	examined := only(t, report, "terminal-credentials-examined")
	if examined.Severity != OK || examined.Area != AreaGate {
		t.Errorf("terminal-credentials-examined is %s in %s, want ok in gate", examined.Severity, examined.Area)
	}
	contains(t, "says", examined.Says, "4 of them read by the repository", "4 of those named by the test setup")
	contains(t, "evidence", examined.Evidence, "conftest.py")
}

// A project that declares no services puts no credential in a terminal, and
// the line says that in place of saying nothing.
func TestAProjectThatDeclaresNoServicesCarriesNoCredentialIntoATerminal(t *testing.T) {
	// Arrange
	f := newFixture(t, terminalFiles(nil))

	// Act
	report := f.check()

	// Assert
	none(t, report, "terminal-reaches-production")
	examined := only(t, report, "terminal-credentials-examined")
	contains(t, "says", examined.Says, "carries no credential")
	contains(t, "evidence", examined.Evidence, "auth.json")
}

// A manifest with no default service gives a task whose brief has no
// credentials line nothing, and the line says so by name.
func TestAManifestWithNoDefaultServiceSaysEveryTaskCarriesNothingByDefault(t *testing.T) {
	// Arrange
	f := newFixture(t, terminalFiles(nil))
	f.manifest("auth.json", `{"project":"northwind","services":[{"name":"plaid","method":"env","env":["PLAID_CLIENT_ID","PLAID_SECRET"]}]}`)

	// Act
	report := f.check()

	// Assert
	contains(t, "evidence", only(t, report, "terminal-credentials-examined").Evidence, "carries none, since no service is marked default")
}

// A variable named for tests is a test value by the manifest's own word,
// as a key that starts sk_test_ is one by its value. A test database a
// suite is meant to use is not a road to production.
func TestAVariableNamedForTestsIsNoProductionCredential(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		"tests/db.test.ts":  "const url = process.env.TEST_DATABASE_URL\nconst live = process.env.DATABASE_URL\n",
		".no-mistakes.yaml": "commands:\n  test: \"npm test\"\n",
		"package.json":      `{"scripts":{"test":"vitest run"}}`,
	})
	f.manifest("auth.json", `{"project":"northwind","services":[{"name":"neon","method":"env","env":["DATABASE_URL"]},{"name":"test-database","method":"env","env":["TEST_DATABASE_URL"],"optional":true}]}`)

	// Act
	report := f.check("npm")

	// Assert
	finding := only(t, report, "terminal-reaches-production")
	contains(t, "evidence", finding.Evidence, "neon: DATABASE_URL (tests/db.test.ts)")
	if strings.Contains(finding.Evidence, "TEST_DATABASE_URL") {
		t.Errorf("evidence %q names TEST_DATABASE_URL, which is named for tests", finding.Evidence)
	}
	contains(t, "evidence", only(t, report, "terminal-credentials-examined").Evidence, "Left out as named for tests: TEST_DATABASE_URL")
}
