package projectcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

const workflow = "on: pull_request\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: pytest -q\n"

// A gate command that does not exist fails its step on every run, or leaves
// an agent to choose what runs instead.
func TestAGateCommandThatDoesNotExistIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		".no-mistakes.yaml": "commands:\n  test: \"python scripts/gate_test.py\"\n  lint: \"nosuchlinter .\"\ngates:\n  - name: tests-kept\n    after: lint\n    command: \"cfo gate tests-kept\"\n",
		"scripts/other.py":  "print('x')\n",
	})

	// Act
	report := f.check("python", "cfo")

	// Assert
	finding := only(t, report, "gate-command-missing")
	if finding.Severity != High || finding.Area != AreaGate {
		t.Errorf("gate-command-missing is %s in %s, want high in gate", finding.Severity, finding.Area)
	}
	contains(t, "evidence", finding.Evidence, "commands.test", "scripts/gate_test.py", "commands.lint", "nosuchlinter")
	if strings.Contains(finding.Evidence, "tests-kept") {
		t.Errorf("evidence %q names the tests-kept gate, whose program is there", finding.Evidence)
	}
}

func TestAGateWhoseCommandsExistPasses(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		".no-mistakes.yaml":         "commands:\n  test: \"python scripts/gate_test.py\"\ngates:\n  - name: tests-kept\n    after: lint\n    command: \"cfo gate tests-kept\"\n",
		"scripts/gate_test.py":      "import subprocess\nsubprocess.run(['pytest'])\n",
		".github/workflows/ci.yml":  workflow,
		"tests/test_placeholder.py": "def test_ok():\n    pass\n",
	})

	// Act
	report := f.check("python", "cfo")

	// Assert
	none(t, report, "gate-command-missing")
	none(t, report, "gate-file-missing")
	none(t, report, "gate-test-unbounded")
	finding := only(t, report, "gate-commands-found")
	contains(t, "evidence", finding.Evidence, "commands.test", "gates.tests-kept")
	if !report.Passed(AreaGate) {
		t.Errorf("the gate area did not pass:\n%s", report.Text())
	}
}

// A package script is a command of its own: the gate names it, and it is
// real only when a package file defines it where the command runs.
func TestAGateCommandNamingAPackageScriptIsCheckedAgainstThePackageFile(t *testing.T) {
	for _, test := range []struct {
		name, command string
		missing       bool
	}{
		{"defined at the root", "npm run test:gate", false},
		{"defined under --prefix", "npm --prefix web run test:web", false},
		{"defined nowhere", "npm run test:all", true},
		{"defined only in another folder", "npm --prefix web run test:gate", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, map[string]string{
				".no-mistakes.yaml": "commands:\n  test: \"" + test.command + "\"\n",
				"package.json":      `{"scripts":{"test:gate":"vitest run"}}`,
				"web/package.json":  `{"scripts":{"test:web":"vitest run"}}`,
			})

			// Act
			report := f.check("npm")

			// Assert
			if !test.missing {
				none(t, report, "gate-command-missing")
				return
			}
			finding := only(t, report, "gate-command-missing")
			contains(t, "evidence", finding.Evidence, "package.json")
		})
	}
}

// With no gate file every step of the gate is an agent's choice, the test
// step included.
func TestARepositoryWithNoGateFileIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "gate-file-missing")
	if finding.Severity != Medium {
		t.Errorf("gate-file-missing is %s, want medium", finding.Severity)
	}
	contains(t, "evidence", finding.Evidence, ".no-mistakes.yaml")
}

// A gate file that names no test command leaves the test step to an agent,
// which is how a gate once booted a live backend to "run tests".
func TestAGateWithNoTestCommandIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".no-mistakes.yaml": "auto_fix:\n  review: 0\n"})

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "gate-test-unbounded")
	if finding.Severity != Medium {
		t.Errorf("gate-test-unbounded is %s, want medium", finding.Severity)
	}
}

// The gate's ci step waits for a workflow run, so a repository with no
// workflow holds every gate run there.
func TestARepositoryWithNoWorkflowIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".no-mistakes.yaml": "commands:\n  test: \"pytest -q\"\n"})

	// Act
	report := f.check("pytest")

	// Assert
	finding := only(t, report, "ci-missing")
	if finding.Severity != High {
		t.Errorf("ci-missing is %s, want high", finding.Severity)
	}
}

// CI is the check of what the gate ran, so a test runner the gate uses and
// no workflow runs is a result nobody checks again.
func TestAGateWhoseTestRunnerNoWorkflowRunsIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		".no-mistakes.yaml":        "commands:\n  test: \"python scripts/gate_test.py\"\n",
		"scripts/gate_test.py":     "import subprocess\nsubprocess.run(['pytest', '-q'])\nsubprocess.run(['npx', 'vitest', 'run'])\n",
		".github/workflows/ci.yml": workflow,
	})

	// Act
	report := f.check("python")

	// Assert
	finding := only(t, report, "gate-ci-differs")
	if finding.Severity != Medium {
		t.Errorf("gate-ci-differs is %s, want medium", finding.Severity)
	}
	contains(t, "evidence", finding.Evidence, "vitest", ".github/workflows/ci.yml")
	if strings.Contains(finding.Evidence, "the gate runs pytest") {
		t.Errorf("evidence %q names pytest, which the workflow runs", finding.Evidence)
	}
}

func TestAGateWhoseTestRunnersCIRunsPasses(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		".no-mistakes.yaml":        "commands:\n  test: \"pytest -q\"\n",
		".github/workflows/ci.yml": workflow,
	})

	// Act
	report := f.check("pytest")

	// Assert
	none(t, report, "gate-ci-differs")
	none(t, report, "ci-missing")
	finding := only(t, report, "gate-ci-agree")
	contains(t, "evidence", finding.Evidence, "pytest", ".github/workflows/ci.yml")
}

const (
	policyThree = `{"version":3,"primary":{"harness":"codex","model":"gpt-6.1-sol","effort":"xhigh"},"reviewer":{"harness":"codex","model":"gpt-6.1-sol","effort":"xhigh"},"fixer":{"harness":"codex","model":"gpt-6.1-sol","effort":"xhigh"},` + policyRest
	policySix   = `{"version":6,` + policyRest
	policyRest  = `"auto_fix":{"review":0,"test":1,"lint":1,"rebase":1,"ci":1},"classes":{"ordinary":{"review_cycles":2},"high-risk":{"review_cycles":3},"mechanical":{"review_cycles":2}}}`
)

// A gate file can pin the agent its gate runs on, and the check read only
// its commands. A pin the home's pipeline policy refuses stops every gate
// run of the project at its start, which one project's gate file records
// having happened. The verdict is the pipeline's own: the check asks the
// code that refuses the run.
func TestAnAgentPinnedInTheGateFileIsReadAgainstTheHomesPolicy(t *testing.T) {
	for _, test := range []struct {
		name, policy, pin string
		check             string
		severity          Severity
		named             []string
	}{
		{"a pin the policy refuses", policyThree, "agent: [claude]\n", "gate-agent-refused", High, []string{"claude", "version 3", "codex"}},
		{"a pin that is the policy's own agent", policyThree, "agent: codex\n", "gate-agent-read", OK, []string{"codex", "version 3"}},
		{"a pin under a policy that takes the task's own harness", policySix, "agent: [claude]\n", "gate-agent-read", OK, []string{"claude", "version 6", "replaces"}},
		{"a pin and no policy to ask", "", "agent: [claude]\n", "gate-agent-unjudged", Low, []string{"claude", "pipeline.json"}},
		{"a pin and a policy the loader refuses", `{"version":9}`, "agent: [claude]\n", "gate-agent-unjudged", Low, []string{"claude", "pipeline.json"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, map[string]string{".no-mistakes.yaml": test.pin + "commands:\n  test: \"pytest -q\"\n"})
			options := f.options("pytest")
			options.PolicyFile = filepath.Join(filepath.Dir(f.checkout), "config", "pipeline.json")
			if test.policy != "" {
				writeFile(t, options.PolicyFile, test.policy)
			}

			// Act
			report := f.run(options)

			// Assert
			finding := only(t, report, test.check)
			if finding.Severity != test.severity || finding.Area != AreaGate {
				t.Errorf("%s is %s in %s, want %s in gate", test.check, finding.Severity, finding.Area, test.severity)
			}
			contains(t, "line", finding.Text(), test.named...)
			for _, other := range []string{"gate-agent-refused", "gate-agent-read", "gate-agent-unjudged"} {
				if other != test.check {
					none(t, report, other)
				}
			}
		})
	}
}

// A gate file that pins no agent has nothing to refuse, and says nothing.
func TestAGateFileThatPinsNoAgentHasNoAgentLine(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".no-mistakes.yaml": "commands:\n  test: \"pytest -q\"\n"})
	options := f.options("pytest")
	options.PolicyFile = filepath.Join(filepath.Dir(f.checkout), "config", "pipeline.json")
	writeFile(t, options.PolicyFile, policyThree)

	// Act
	report := f.run(options)

	// Assert
	for _, check := range []string{"gate-agent-refused", "gate-agent-read", "gate-agent-unjudged"} {
		none(t, report, check)
	}
}
