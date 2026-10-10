package projectcheck

import (
	"strings"
	"testing"
)

// setupAuth gives every task one credential the repository reads, so a test
// setup that names it shows as a variable left out.
const setupAuth = `{"project":"northwind","services":[{"name":"postgres","method":"env","env":["DATABASE_URL"],"default":true}]}`

const setupSource = "import os\n\nurl = os.environ[\"DATABASE_URL\"]\n"

// "Test setup read: none this check knows" stood on five of eleven projects.
// A project pins what its tests may see in more places than a conftest.py
// and a runner's config: the pytest section of pyproject.toml, a file the
// test command loads before the tests, and a plain script that is the test
// command. Each is read, through the package script a command runs.
func TestATestSetupTheCheckHadNoNameForIsRead(t *testing.T) {
	for _, test := range []struct {
		name    string
		tracked map[string]string
		gate    string
		by      string
	}{
		{
			name:    "the pytest section of pyproject.toml",
			tracked: map[string]string{"pyproject.toml": "[project]\nname = \"northwind\"\n\n[tool.pytest.ini_options]\nenv = [\n    \"DATABASE_URL=sqlite://\",\n]\n"},
			gate:    "pytest -q",
			by:      "pyproject.toml",
		},
		{
			name:    "the pytest section of setup.cfg",
			tracked: map[string]string{"setup.cfg": "[tool:pytest]\nenv =\n    DATABASE_URL=sqlite://\n"},
			gate:    "pytest -q",
			by:      "setup.cfg",
		},
		{
			name: "a file the package's test script loads before the tests",
			tracked: map[string]string{
				"package.json":    `{"scripts":{"test":"node --import ./tests/setup.mjs --test tests/"}}`,
				"tests/setup.mjs": "process.env.DATABASE_URL = 'postgres://localhost/test'\n",
			},
			gate: "npm test",
			by:   "tests/setup.mjs",
		},
		{
			name: "a script a package script runs through another",
			tracked: map[string]string{
				"package.json":          `{"scripts":{"test":"npm run test:unit","test:unit":"node scripts/run-tests.mjs"}}`,
				"scripts/run-tests.mjs": "process.env.DATABASE_URL = 'postgres://localhost/test'\n",
			},
			gate: "npm test",
			by:   "scripts/run-tests.mjs",
		},
		{
			name: "a plain Python test script the instructions name, where the gate names no test command",
			tracked: map[string]string{
				"AGENTS.md":            "Test with `python scripts/gate_test.py`.\n",
				"scripts/gate_test.py": "import os\n\nos.environ[\"DATABASE_URL\"] = \"sqlite://\"\n",
			},
			by: "scripts/gate_test.py",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			files := map[string]string{"src/db.py": setupSource}
			for name, content := range test.tracked {
				files[name] = content
			}
			if test.gate != "" {
				files[".no-mistakes.yaml"] = "commands:\n  test: \"" + test.gate + "\"\n"
			}
			f := newFixture(t, files)
			f.manifest("auth.json", setupAuth)

			// Act
			report := f.check("pytest", "npm", "node", "python")

			// Assert
			contains(t, "evidence", only(t, report, "test-env-examined").Evidence, test.by)
			none(t, report, "terminal-reaches-production")
			examined := only(t, report, "terminal-credentials-examined")
			contains(t, "says", examined.Says, "1 of those named by the test setup")
			contains(t, "evidence", examined.Evidence, test.by)
		})
	}
}

// A file that only shares a name with a test setup is none: a pyproject.toml
// with no pytest section says nothing of how tests start.
func TestAProjectFileWithNoTestSectionIsNoTestSetup(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		"src/db.py":         setupSource,
		"pyproject.toml":    "[project]\nname = \"northwind\"\n\n[tool.uv]\nenv = [\"DATABASE_URL\"]\n",
		".no-mistakes.yaml": "commands:\n  test: \"pytest -q\"\n",
	})
	f.manifest("auth.json", setupAuth)

	// Act
	report := f.check("pytest")

	// Assert
	finding := only(t, report, "terminal-reaches-production")
	contains(t, "evidence", finding.Evidence, "postgres: DATABASE_URL (")
	if strings.Contains(only(t, report, "test-env-examined").Evidence, "pyproject.toml") {
		t.Errorf("pyproject.toml was read as a test setup and holds no pytest section:\n%s", report.Text())
	}
}

// A suite of node's own test runner, of tsx or of go test has no setup file:
// each test file sets what it needs, in a process of its own. The line said
// "none this check knows", which reads as a setup the check could not find.
// It now says what the test command starts and that such a suite has none.
func TestASuiteWithNoSetupFileIsNamedAsOne(t *testing.T) {
	for _, test := range []struct {
		name, gate string
		tracked    map[string]string
		named      []string
	}{
		{"node's test runner behind a package script", "npm test", map[string]string{"package.json": `{"scripts":{"test":"node --test tests/"}}`}, []string{"`npm test` runs `node --test tests/`", "node --test", "has no setup file"}},
		{"tsx behind a package script", "npm run test:unit", map[string]string{"package.json": `{"scripts":{"test:unit":"tsx --test tests/unit.test.ts"}}`, "tests/unit.test.ts": "import test from 'node:test'\n"}, []string{"tsx --test", "has no setup file"}},
		{"go test", "go test ./...", nil, []string{"go test", "has no setup file"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			files := map[string]string{".no-mistakes.yaml": "commands:\n  test: \"" + test.gate + "\"\n"}
			for name, content := range test.tracked {
				files[name] = content
			}
			f := newFixture(t, files)

			// Act
			report := f.check("npm", "go")

			// Assert
			evidence := only(t, report, "test-env-examined").Evidence
			contains(t, "evidence", evidence, test.named...)
			if strings.Contains(evidence, "none this check knows") {
				t.Errorf("evidence %q says no test setup is known, and the suite has none to know", evidence)
			}
		})
	}
}

// A test command the check can make nothing of still says so, so a setup it
// could not find does not read as a suite that has none.
func TestATestCommandOfNoKnownKindStillSaysNoSetupIsKnown(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".no-mistakes.yaml": "commands:\n  test: \"make check\"\n"})

	// Act
	report := f.check("make")

	// Assert
	contains(t, "evidence", only(t, report, "test-env-examined").Evidence, "none this check knows")
}

// The gate and CI are compared by the test runners each starts. A suite of
// node's own test runner was no runner the check knew, so the line said the
// two could not be compared.
func TestNodesOwnTestRunnerIsARunnerTheGateAndCIAreComparedBy(t *testing.T) {
	for _, runner := range []string{"node --test", "tsx --test"} {
		t.Run(runner, func(t *testing.T) {
			// Arrange
			f := newFixture(t, map[string]string{
				".no-mistakes.yaml":        "commands:\n  test: \"" + runner + " tests/\"\n",
				".github/workflows/ci.yml": "on: pull_request\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: " + runner + " tests/\n",
			})

			// Act
			report := f.check("node", "tsx")

			// Assert
			none(t, report, "gate-ci-unknown")
			contains(t, "says", only(t, report, "gate-ci-agree").Says, runner)
		})
	}
}

// A test command names its test files, and a test file that reads a
// variable has not pinned it. Read as setup, such a file hid the variable
// it reads. Only what a test command runs before or around the tests is
// setup: the script an interpreter is handed, and the files it preloads.
func TestATestFileATestCommandNamesIsNoTestSetup(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		".no-mistakes.yaml": "commands:\n  test: \"npm test\"\n",
		"package.json":      `{"scripts":{"test":"tsx --test tests/db.test.ts"}}`,
		"tests/db.test.ts":  "const url = process.env.DATABASE_URL\n",
	})
	f.manifest("auth.json", setupAuth)

	// Act
	report := f.check("npm")

	// Assert
	contains(t, "evidence", only(t, report, "terminal-reaches-production").Evidence, "DATABASE_URL (tests/db.test.ts)")
	contains(t, "says", only(t, report, "terminal-credentials-examined").Says, "0 of those named by the test setup")
}

// The script an interpreter is handed is what runs around the tests, and
// what follows it on the line are that script's own arguments.
func TestOnlyTheScriptATestCommandRunsIsReadAsItsSetup(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		".no-mistakes.yaml":    "commands:\n  test: \"uv run --no-project scripts/gate_test.py tests/test_db.py\"\n",
		"scripts/gate_test.py": "import os\n\nos.environ[\"DATABASE_URL\"] = \"sqlite://\"\n",
		"tests/test_db.py":     "import os\n\nkey = os.environ[\"OPENAI_API_KEY\"]\n",
	})
	f.manifest("auth.json", `{"project":"northwind","services":[{"name":"postgres","method":"env","env":["DATABASE_URL"],"default":true},{"name":"openai","method":"env","env":["OPENAI_API_KEY"],"default":true}]}`)

	// Act
	report := f.check("uv")

	// Assert
	finding := only(t, report, "terminal-reaches-production")
	contains(t, "evidence", finding.Evidence, "OPENAI_API_KEY (tests/test_db.py)")
	if strings.Contains(finding.Evidence, "DATABASE_URL") {
		t.Errorf("evidence %q names DATABASE_URL, which the script the test command runs sets", finding.Evidence)
	}
	setup := strings.SplitN(only(t, report, "test-env-examined").Evidence, "Test setup read", 2)[1]
	contains(t, "test setup", setup, "scripts/gate_test.py")
	if strings.Contains(setup, "tests/test_db.py") {
		t.Errorf("an argument of the script was read as test setup: %s", setup)
	}
}

// Instruction files name a test command more than once, and a shell block
// ends it with a comment. What its suite is gets said once for a runner,
// with the commands that start it.
func TestASuiteIsSaidOnceForARunnerWithTheCommandsThatStartIt(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		"AGENTS.md": "Run `go test ./...` before a push.\n\n```bash\ngo test ./...          # the unit tests\ngo test -race ./...\n```\n",
	})

	// Act
	report := f.check("go")

	// Assert
	evidence := only(t, report, "test-env-examined").Evidence
	if said := strings.Count(evidence, "has no setup file"); said != 1 {
		t.Errorf("the suite is said %d times, want once: %s", said, evidence)
	}
	contains(t, "evidence", evidence, "`go test ./...`", "`go test -race ./...`")
	if strings.Contains(evidence, "the unit tests") {
		t.Errorf("evidence %q repeats a comment as part of a command", evidence)
	}
}
