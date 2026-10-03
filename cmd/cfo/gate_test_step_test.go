package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// testStepModule is a branch off main in a repository holding a Go module:
// a leaf package a, a package b whose test imports a and fails when it sees
// the fleet's CFO_HOME, and a package c nothing imports whose test fails.
// base adds files to the default branch, and the branch then changes what
// change names. The run's reports go to a store of the test's own.
func testStepModule(t *testing.T, base, change map[string]string) string {
	t.Helper()
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	dir := t.TempDir()
	write := func(files map[string]string) {
		for name, content := range files {
			path := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write(map[string]string{
		"go.mod":      "module example.com/m\n\ngo 1.22\n",
		"a/a.go":      "package a\n\nfunc A() int { return 1 }\n",
		"b/b.go":      "package b\n",
		"b/b_test.go": "package b\n\nimport (\n\t\"os\"\n\t\"testing\"\n\n\t\"example.com/m/a\"\n)\n\nfunc TestB(t *testing.T) {\n\tif os.Getenv(\"CFO_HOME\") != \"\" || a.A() == 0 {\n\t\tt.Fatal(\"the fleet's CFO_HOME reached the test\")\n\t}\n}\n",
		"c/c.go":      "package c\n",
		"c/c_test.go": "package c\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) { t.Fatal(\"c is never chosen\") }\n",
		"README.md":   "module\n",
	})
	write(base)
	git("init", "-q", "--initial-branch=main")
	git("config", "user.email", "t@example.invalid")
	git("config", "user.name", "t")
	git("add", ".")
	git("commit", "-qm", "base")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("switch", "-qc", "feature")
	write(change)
	git("add", ".")
	git("commit", "-qm", "change")
	return dir
}

// The step names each package it chose and why, tests only those (c, whose
// test always fails, is never run), and runs them without the fleet's home
// even when the gate step inherited it.
func TestGateTestRunsTheChangedPackagesAndTheirImportersWithoutTheFleetHome(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	t.Setenv("CFO_HOME", filepath.Join(dir, "fleet"))

	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "test"}, &stdout, &stderr)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{"- example.com/m/a (changed)", "- example.com/m/b (imports example.com/m/a)", "CI runs every package"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "example.com/m/c") {
		t.Errorf("stdout %q names a package the branch neither changed nor feeds", stdout.String())
	}
}

// A branch that changed no Go package says so in one line and passes.
func TestGateTestSaysSoWhenNoPackageChanged(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"README.md": "more\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "test"}, &stdout, &stderr)

	// Assert
	if exit != 0 || !strings.Contains(stdout.String(), "no Go package changed") {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want a clear line and exit 0", exit, stdout.String(), stderr.String())
	}
}

// A failing test in a chosen package fails the step, which is what parks or
// fixes the gate's test step.
func TestGateTestFailsWhenAChosenPackageFails(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"c/c.go": "package c\n\n// changed\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "test"}, &stdout, &stderr)

	// Assert
	if exit != 1 || !strings.Contains(stdout.String(), "- example.com/m/c (changed)") {
		t.Fatalf("exit=%d stdout=%q, want c chosen and the step failed", exit, stdout.String())
	}
	report, _ := lastReport(t)
	if report.Status != "failed" || len(report.Checks) != 2 || report.Checks[1].Status != "failed" || report.Checks[1].ExitCode != 1 {
		t.Errorf("the report says %s with checks %+v; want failed, with go test failed on exit code 1", report.Status, report.Checks)
	}
	if !strings.Contains(stdout.String(), "cfo gate test: failed at level affected") {
		t.Errorf("stdout %q lacks the verdict line", stdout.String())
	}
}

// lastReport reads the one report a run left in the test's store.
func lastReport(t *testing.T) (verify.Report, string) {
	t.Helper()
	reports, err := filepath.Glob(filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "reports", "*", "*.json"))
	if err != nil || len(reports) != 1 {
		t.Fatalf("the run left %d report(s) in the store (%v), want 1", len(reports), err)
	}
	data, err := os.ReadFile(reports[0])
	if err != nil {
		t.Fatal(err)
	}
	var report verify.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return report, reports[0]
}

func revision(t *testing.T, dir, name string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", name).Output()
	if err != nil {
		t.Fatalf("git rev-parse %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

// A run leaves a report of exactly what it verified: the commit and where
// the branch left the default branch, the level, each package with why, each
// command with what became of it, and a log of what the commands wrote.
func TestGateTestRecordsWhatItRanInAReport(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	t.Setenv("CFO_TASK_ID", "cg-example")
	t.Setenv("NO_MISTAKES_GATE", "")

	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "test"}, &stdout, &stderr)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	report, path := lastReport(t)
	if report.Project != "m" || report.Task != "cg-example" || report.Commit != revision(t, dir, "HEAD") || report.Base != revision(t, dir, "origin/main") || report.Uncommitted != 0 {
		t.Errorf("the report is of project %q, task %q, commit %q, base %q, %d uncommitted; want m, cg-example, the branch's head, origin/main and 0", report.Project, report.Task, report.Commit, report.Base, report.Uncommitted)
	}
	if report.Level != "affected" || report.RequiredLevel != "affected" || report.Status != "passed" {
		t.Errorf("the report says level %q, required %q, status %q; want affected, affected, passed", report.Level, report.RequiredLevel, report.Status)
	}
	if !fsx.SamePath(report.Root, dir) {
		t.Errorf("the report's root is %q; want the repository's top directory %q", report.Root, dir)
	}
	wantSelected := []verify.Selection{{Package: "example.com/m/a", Why: "changed"}, {Package: "example.com/m/b", Why: "imports example.com/m/a"}}
	if !slices.Equal(report.Selected, wantSelected) {
		t.Errorf("the report selected %+v; want %+v", report.Selected, wantSelected)
	}
	wantCommands := [][]string{
		{"go", "vet", "example.com/m/a", "example.com/m/b"},
		{"go", "test", "-count=1", "-p", "2", "-timeout", "45m", "example.com/m/a", "example.com/m/b"},
	}
	if len(report.Checks) != len(wantCommands) {
		t.Fatalf("the report holds %d check(s), want %d: %+v", len(report.Checks), len(wantCommands), report.Checks)
	}
	for index, check := range report.Checks {
		if !slices.Equal(check.Command, wantCommands[index]) || check.Status != "passed" || check.ExitCode != 0 || check.DurationSeconds <= 0 || check.Start.IsZero() {
			t.Errorf("check %d is %+v; want %q passed on exit code 0 with a start and a duration", index, check, wantCommands[index])
		}
	}
	log, err := os.ReadFile(report.Log)
	if err != nil || !strings.Contains(string(log), "ok  \texample.com/m/b") {
		t.Errorf("the log %s holds %q (%v); want the tests' own output", report.Log, log, err)
	}
	if want := "cfo gate test: passed at level affected"; !strings.Contains(stdout.String(), want) || !strings.Contains(stdout.String(), path) {
		t.Errorf("stdout %q lacks %q or the report's path %s", stdout.String(), want, path)
	}
}

// A step under the gate runs in the shared gate daemon's environment, which
// can be another goblin's, so only a goblin's own run names its task.
func TestOnlyAGoblinsOwnRunNamesItsTask(t *testing.T) {
	for name, test := range map[string]struct {
		environment map[string]string
		want        string
	}{
		"a goblin's own run":    {map[string]string{"CFO_TASK_ID": "cg-example"}, "cg-example"},
		"a step under the gate": {map[string]string{"CFO_TASK_ID": "cg-other", "NO_MISTAKES_GATE": "1"}, ""},
		"a run outside a fleet": {map[string]string{}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := taskID(func(name string) string { return test.environment[name] }); got != test.want {
				t.Errorf("taskID = %q; want %q", got, test.want)
			}
		})
	}
}

// --plan prints what a run would do and does none of it: c's test always
// fails, and the plan for a change to c still exits 0 and leaves no report.
func TestGateTestPlanPrintsThePlanAndRunsNothing(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"c/c.go": "package c\n\n// changed\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "test", "--plan"}, &stdout, &stderr)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"cfo gate test: level affected: the default for a change",
		"- example.com/m/c (changed)",
		"policy: built-in defaults",
		"would run: go vet example.com/m/c",
		"would run: go test -count=1 -p 2 -timeout 45m example.com/m/c",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
	if reports, _ := filepath.Glob(filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "reports", "*", "*")); len(reports) != 0 {
		t.Errorf("a plan left %q in the store; want nothing", reports)
	}
}

// The full level asked for takes every package whatever the change reaches,
// and the plan still says what the change itself requires.
func TestGateTestPlansEveryPackageAtTheFullLevel(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "test", "--level", "full", "--plan"}, &stdout, &stderr)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"cfo gate test: level full, asked for; the change requires affected: the default for a change",
		"cfo gate test: every package is tested",
		"would run: go vet ./...",
		"would run: go test -count=1 -p 2 -timeout 45m ./...",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
}

// The fast level leaves a slow package's tests to the affected level and
// says so: c's test always fails and is not run, the run passes, and both
// its last line and its report say the change still requires affected.
func TestGateTestAtFastLeavesASlowPackagesTestsAndSaysWhatIsStillRequired(t *testing.T) {
	// Arrange
	dir := testStepModule(t,
		map[string]string{"config/verify.json": `{"version": 1, "slow_packages": ["c"]}`},
		map[string]string{"c/c.go": "package c\n\n// changed\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "test", "--level", "fast"}, &stdout, &stderr)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"cfo gate test: level fast, asked for; the change requires affected: the default for a change",
		"left to the affected level:",
		"- tests of example.com/m/c (a slow package)",
		"cfo gate test: passed at level fast",
		"cfo gate test: the change still requires the affected level before it merges",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
	report, _ := lastReport(t)
	wantLeft := []verify.Left{{Check: "tests of example.com/m/c", Why: "a slow package"}}
	if report.Level != "fast" || report.RequiredLevel != "affected" || report.Status != "passed" || !slices.Equal(report.Left, wantLeft) {
		t.Errorf("the report says level %q, required %q, status %q, left %+v; want fast, affected, passed, %+v", report.Level, report.RequiredLevel, report.Status, report.Left, wantLeft)
	}
	if len(report.Checks) != 1 || !slices.Equal(report.Checks[0].Command, []string{"go", "vet", "example.com/m/c"}) {
		t.Errorf("the report's checks are %+v; want go vet on c alone", report.Checks)
	}
}

// A level that is none of the three is refused before anything runs.
func TestGateTestRefusesAnUnknownLevel(t *testing.T) {
	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "test", "--level", "quick"}, &stdout, &stderr)

	// Assert
	if exit != 2 || !strings.Contains(stderr.String(), "fast, affected or full") {
		t.Fatalf("exit=%d stderr=%q, want 2 and a refusal naming fast, affected or full", exit, stderr.String())
	}
}

// When vet fails the tests do not run, and the report says exactly that
// rather than leaving a test run that never started looking like a pass.
func TestGateTestReportsTheTestsAsNotRunWhenVetFails(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nimport \"fmt\"\n\nfunc A() int {\n\tfmt.Printf(\"%d\", \"one\")\n\treturn 2\n}\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "test"}, &stdout, &stderr)

	// Assert
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	_, path := lastReport(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Status string
		Checks []map[string]any
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "failed" || len(report.Checks) != 2 || report.Checks[0]["status"] != "failed" || report.Checks[0]["exit_code"] == float64(0) {
		t.Fatalf("the report says %s with checks %v; want failed, with go vet failed on a non-zero exit code", report.Status, report.Checks)
	}
	unrun := report.Checks[1]
	if _, started := unrun["start"]; unrun["status"] != "not_run" || unrun["exit_code"] != float64(-1) || started {
		t.Errorf("the go test check is %v; want not_run, exit code -1 and no start", unrun)
	}
}

// A run whose report cannot be written says so and keeps its verdict: the
// checks decide the exit code, never the store.
func TestGateTestKeepsItsVerdictWhenItCanLeaveNoReport(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"README.md": "more\n"})
	t.Chdir(dir)
	blocked := filepath.Join(t.TempDir(), "a-file-where-the-store-should-be")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_VERIFY_DIR", blocked)

	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "test"}, &stdout, &stderr)

	// Assert
	if exit != 0 || !strings.Contains(stdout.String(), "no Go package changed") || !strings.Contains(stderr.String(), "cfo gate test: this run leaves no report") {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want 0, the plan's line and a line saying the run leaves no report", exit, stdout.String(), stderr.String())
	}
}
