package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/gatetest"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// testStepModule is a branch off main in a repository holding a Go module:
// a leaf package a, a package b whose test imports a and fails when it sees
// the fleet's CFO_HOME, and a package c nothing imports whose test fails.
// base adds files to the default branch, and the branch then changes what
// change names. The run's reports go to a store of the test's own, and it
// takes turns one at a time whatever CFO_VERIFY_SLOTS this machine has set.
func testStepModule(t *testing.T, base, change map[string]string) string {
	t.Helper()
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	t.Setenv("CFO_VERIFY_SLOTS", "")
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
	exit := gateTest(&stdout, &stderr)

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
	exit := gateTest(&stdout, &stderr)

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
	exit := gateTest(&stdout, &stderr)

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
	exit := gateTest(&stdout, &stderr)

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
		{"go", "test", "-json", "-count=1", "-p", "2", "-timeout", "45m", "example.com/m/a", "example.com/m/b"},
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
	exit := gateTest(&stdout, &stderr, "--plan")

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"cfo gate test: level affected: the default for a change",
		"- example.com/m/c (changed)",
		"policy: built-in defaults",
		"would run: go vet example.com/m/c",
		"would run: go test -json -count=1 -p 2 -timeout 45m example.com/m/c",
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
	exit := gateTest(&stdout, &stderr, "--level", "full", "--plan")

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"cfo gate test: level full, asked for; the change requires affected: the default for a change",
		"cfo gate test: every package is tested",
		"would run: go vet ./...",
		"would run: go test -json -count=1 -p 2 -timeout 45m ./...",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
}

// A run records what became of each package's tests, prints what go test
// prints without -v, and keeps every line the tests wrote in its log, where
// each test's own time can be read.
func TestGateTestRecordsEachPackagesResultAndKeepsTheFullOutputInItsLog(t *testing.T) {
	// Arrange: the change reaches a, which has no tests, and b, whose test
	// passes.
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTest(&stdout, &stderr)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	report, _ := lastReport(t)
	if len(report.Checks) != 2 {
		t.Fatalf("the report holds %d check(s), want go vet and go test: %+v", len(report.Checks), report.Checks)
	}
	packages := report.Checks[1].Packages
	if len(packages) != 2 || packages[0].Package != "example.com/m/a" || packages[0].Status != "no_tests" ||
		packages[1].Package != "example.com/m/b" || packages[1].Status != "passed" || packages[1].Tests != 1 || packages[1].Seconds <= 0 {
		t.Errorf("the tests' packages are %+v; want a with no tests and b passed with its one test and its time", packages)
	}
	if len(report.Checks[0].Packages) != 0 {
		t.Errorf("go vet is recorded with packages %+v; want none, it runs no tests", report.Checks[0].Packages)
	}
	if !strings.Contains(stdout.String(), "ok  \texample.com/m/b") || strings.Contains(stdout.String(), "=== RUN") || strings.Contains(stdout.String(), `"Action"`) {
		t.Errorf("stdout %q; want b's summary line, and neither a passing test's lines nor an event", stdout.String())
	}
	if strings.Contains(stdout.String(), "go test did not pass in ") {
		t.Errorf("stdout %q reports a failure; want no failure summary for a successful run with a package that has no tests", stdout.String())
	}
	log, err := os.ReadFile(report.Log)
	if err != nil || !strings.Contains(string(log), "=== RUN   TestB\n") || !strings.Contains(string(log), "--- PASS: TestB") {
		t.Errorf("the log %s holds %q (%v); want every line the tests wrote", report.Log, log, err)
	}
}

// A failed run ends by naming each package that did not pass and the tests in
// it that failed, after the tests' own output, so the finding is read in one
// place.
func TestGateTestNamesTheFailedTestsOfEachPackageThatDidNotPass(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"c/c.go": "package c\n\n// changed\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTest(&stdout, &stderr)

	// Assert
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"--- FAIL: TestC",
		"c is never chosen",
		"cfo gate test: go test did not pass in 1 of 1 package(s):\n- example.com/m/c: TestC failed\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
	report, _ := lastReport(t)
	if packages := report.Checks[1].Packages; len(packages) != 1 || packages[0].Status != "failed" || !slices.Equal(packages[0].Failed, []string{"TestC"}) {
		t.Errorf("the tests' packages are %+v; want c failed in TestC", packages)
	}
}

// A package whose tests were still running when go test ended, as when the
// run was stopped, is recorded as unfinished with the test it was in, and the
// run says so: it is never left looking like a package that passed or was
// not there.
func TestGateTestRecordsAPackageWhoseTestsNeverEndedAsUnfinished(t *testing.T) {
	// Arrange: go test ends, failing, while b's TestHangs still runs.
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	runtime := standIn()
	runtime.gateRun = func(command []string, _ string, _ []string, stdout, _ io.Writer) (int, error) {
		if command[1] != "test" {
			return 0, nil
		}
		io.WriteString(stdout, `{"Action":"start","Package":"example.com/m/b"}`+"\n"+
			`{"Action":"run","Package":"example.com/m/b","Test":"TestHangs"}`+"\n"+
			`{"Action":"output","Package":"example.com/m/b","Test":"TestHangs","Output":"    b_test.go:9: about to hang\n"}`+"\n")
		return 1, errors.New("exit status 1")
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTestWith(runtime, &stdout, &stderr)

	// Assert
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"    b_test.go:9: about to hang\n",
		"cfo gate test: go test did not pass in 1 of 1 package(s):\n- example.com/m/b: TestHangs did not finish\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
	report, _ := lastReport(t)
	if packages := report.Checks[1].Packages; report.Status != "failed" || len(packages) != 1 || packages[0].Status != "unfinished" || !slices.Equal(packages[0].Unfinished, []string{"TestHangs"}) {
		t.Errorf("the report says %s with packages %+v; want failed, with b unfinished in TestHangs", report.Status, packages)
	}
}

// What each kind of package that did not pass is said to have done: a test
// failed, a test never finished, as one that hangs, or the package did not
// compile. Names past five are counted.
func TestNotPassedSaysWhatBecameOfAPackage(t *testing.T) {
	for name, test := range map[string]struct {
		result gatetest.PackageResult
		want   string
	}{
		"tests failed":       {gatetest.PackageResult{ImportPath: "m/a", Status: "failed", Failed: []string{"TestA", "TestB"}}, "m/a: TestA, TestB failed"},
		"a test hangs":       {gatetest.PackageResult{ImportPath: "m/a", Status: "failed", Unfinished: []string{"TestHangs"}}, "m/a: TestHangs did not finish"},
		"both":               {gatetest.PackageResult{ImportPath: "m/a", Status: "failed", Failed: []string{"TestA"}, Unfinished: []string{"TestB"}}, "m/a: TestA failed; TestB did not finish"},
		"did not compile":    {gatetest.PackageResult{ImportPath: "m/a", Status: "build_failed"}, "m/a: did not compile"},
		"stopped":            {gatetest.PackageResult{ImportPath: "m/a", Status: "unfinished"}, "m/a: did not finish"},
		"failed, no test":    {gatetest.PackageResult{ImportPath: "m/a", Status: "failed"}, "m/a: failed outside any test"},
		"more than it names": {gatetest.PackageResult{ImportPath: "m/a", Status: "failed", Failed: []string{"T1", "T2", "T3", "T4", "T5", "T6", "T7"}}, "m/a: T1, T2, T3, T4, T5 and 2 more failed"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := notPassed(test.result); got != test.want {
				t.Errorf("notPassed = %q, want %q", got, test.want)
			}
		})
	}
}

// While its tests run, a run says beside its turn how far they are: how many
// packages are done, which test is running, and the last line of output. A
// gate shows a step's output only once the step has ended, so this is where
// its progress is read.
func TestGateTestSaysHowFarItsTestsAreWhileItHoldsItsTurn(t *testing.T) {
	// Arrange: go test has finished a and is inside b's TestSlow, and stays
	// there until the test lets it go.
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	release := make(chan struct{})
	runtime := standIn()
	runtime.gateProgress = 10 * time.Millisecond
	runtime.gateRun = func(command []string, _ string, _ []string, stdout, _ io.Writer) (int, error) {
		if command[1] == "test" {
			io.WriteString(stdout, `{"Action":"start","Package":"example.com/m/a"}`+"\n"+
				`{"Action":"output","Package":"example.com/m/a","Output":"ok  \texample.com/m/a\t0.1s\n"}`+"\n"+
				`{"Action":"pass","Package":"example.com/m/a","Elapsed":0.1}`+"\n"+
				`{"Action":"start","Package":"example.com/m/b"}`+"\n"+
				`{"Action":"run","Package":"example.com/m/b","Test":"TestSlow"}`+"\n")
			<-release
			io.WriteString(stdout, `{"Action":"pass","Package":"example.com/m/b","Test":"TestSlow","Elapsed":1}`+"\n"+
				`{"Action":"output","Package":"example.com/m/b","Output":"ok  \texample.com/m/b\t1.0s\n"}`+"\n"+
				`{"Action":"pass","Package":"example.com/m/b","Elapsed":1}`+"\n")
		}
		return 0, nil
	}
	var stdout, stderr lockedBuffer
	done := make(chan int, 1)

	// Act
	go func() { done <- gateTestWith(runtime, &stdout, &stderr) }()
	slots := filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots")
	var now string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if holding, _, err := verify.Line(slots); err == nil && len(holding) == 1 && strings.Contains(holding[0].Now, "TestSlow") {
			now = holding[0].Now
			break
		}
	}
	close(release)
	exit := <-done

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{"go test: 1 package(s) done", "running example.com/m/b TestSlow for ", "last output: ok  \texample.com/m/a\t0.1s"} {
		if !strings.Contains(now, want) {
			t.Errorf("the run said %q beside its turn; want it to hold %q", now, want)
		}
	}
	if report, _ := lastReport(t); len(report.Checks) != 2 || len(report.Checks[1].Packages) != 2 || report.Checks[1].Packages[1].Status != "passed" {
		t.Errorf("the report's checks are %+v; want both packages recorded as passed once the tests ended", report.Checks)
	}
}

// cfo gate turns shows what the run that holds the turn says it is doing.
func TestGateTurnsShowsWhatTheHolderSaysItIsDoing(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	turn := holdTheTurn(t)
	turn.Say("go test: 2 package(s) done; running example.com/m/b TestSlow for 40s")

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTurns(plenty, &stdout, &stderr)

	// Assert
	if want := "\n  now: go test: 2 package(s) done; running example.com/m/b TestSlow for 40s\n"; exit != 0 || !strings.Contains(stdout.String(), want) {
		t.Errorf("exit=%d stdout=%q stderr=%q; want the holder's line followed by %q", exit, stdout.String(), stderr.String(), want)
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
	exit := gateTest(&stdout, &stderr, "--level", "fast")

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
	exit := gateTest(&stdout, &stderr, "--level", "quick")

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
	exit := gateTest(&stdout, &stderr)

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
	exit := gateTest(&stdout, &stderr)

	// Assert
	if exit != 0 || !strings.Contains(stdout.String(), "no Go package changed") || !strings.Contains(stderr.String(), "cfo gate test: this run leaves no report") {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want 0, the plan's line and a line saying the run leaves no report", exit, stdout.String(), stderr.String())
	}
}

// plenty is the memory of a machine with memory to spare.
func plenty() (uint64, error) { return 64 << 30, nil }

// gateTest runs cfo gate test on a machine with memory to spare, so that a
// run's turn never waits on what this machine happens to have free.
func gateTest(stdout, stderr io.Writer, args ...string) int {
	runtime := defaultCommandRuntime()
	runtime.availableMemory = plenty
	return gateTestWith(runtime, stdout, stderr, args...)
}

func gateTestWith(runtime commandRuntime, stdout, stderr io.Writer, args ...string) int {
	return runWithRuntime(append([]string{"gate", "test"}, args...), stdout, stderr, runtime)
}

// standIn is a runtime on a machine with memory to spare whose go vet and go
// test are not processes: each says that it ran and passes. A test of a run's
// turn plans a real change with it and does not wait for the Go tool.
func standIn() commandRuntime {
	runtime := defaultCommandRuntime()
	runtime.availableMemory = plenty
	runtime.gateRun = func(command []string, _ string, _ []string, stdout, _ io.Writer) (int, error) {
		fmt.Fprintf(stdout, "ran go %s\n", command[1])
		return 0, nil
	}
	return runtime
}

// gateTurns runs cfo gate turns on a machine with the memory available says.
func gateTurns(available func() (uint64, error), stdout, stderr io.Writer) int {
	runtime := defaultCommandRuntime()
	runtime.availableMemory = available
	return runWithRuntime([]string{"gate", "turns"}, stdout, stderr, runtime)
}

// lockedBuffer is a buffer a running command writes while the test reads it.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// holdTheTurn takes the machine's one turn for test runs, as another
// verification run on the machine would.
func holdTheTurn(t *testing.T) verify.Turn {
	t.Helper()
	turn, err := verify.Admission{Dir: filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots"), Slots: 1, Who: "another run", Budget: 90 * time.Minute, Limit: time.Minute, Poll: 10 * time.Millisecond}.Wait(context.Background())
	if err != nil {
		t.Fatalf("the test could not hold the turn: %v", err)
	}
	t.Cleanup(turn.Release)
	return turn
}

// waitInLine starts a run that waits for a turn until the test ends, and
// returns once the run has said that it waits.
func waitInLine(t *testing.T, waiting verify.Admission) {
	t.Helper()
	said := make(chan struct{}, 1)
	waiting.Waiting = func(time.Duration, string) {
		select {
		case said <- struct{}{}:
		default:
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	go func() {
		defer close(stopped)
		waiting.Wait(ctx)
	}()
	select {
	case <-said:
	case <-time.After(30 * time.Second):
		t.Fatal("the waiting run had not joined the line after 30 seconds")
	}
}

// A run at the level a change requires waits for its turn before it starts
// its tests, one run at a time on the machine: while another run holds the
// turn it vets, says which run holds the turn and where it stands in line,
// and runs no test, and once the turn is free it runs and reports how long it
// waited, which its verdict tells apart from the time its checks took.
func TestGateTestWaitsForItsTurnBeforeItRunsItsTests(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	turn := holdTheTurn(t)

	// Act
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)
	go func() { exited <- gateTestWith(standIn(), &stdout, &stderr) }()

	// Assert
	want := fmt.Sprintf("the turn is held by another run (pid %d), for ", os.Getpid())
	for deadline := time.Now().Add(2 * time.Minute); !strings.Contains(stdout.String(), want); time.Sleep(20 * time.Millisecond) {
		select {
		case exit := <-exited:
			t.Fatalf("the run exited %d without waiting for its turn; stdout=%s", exit, stdout.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("stdout %q lacks %q after two minutes", stdout.String(), want)
		}
	}
	// The wait has to reach a second to show in the verdict, which rounds it.
	time.Sleep(1200 * time.Millisecond)
	select {
	case exit := <-exited:
		t.Fatalf("the run exited %d while another run held the turn; stdout=%s", exit, stdout.String())
	default:
	}
	if before := stdout.String(); !strings.Contains(before, "ran go vet") || strings.Contains(before, "ran go test") {
		t.Fatalf("while another run held the turn the run printed %q; want its vet run and its tests not", before)
	}
	for _, said := range []string{"cfo gate test: waiting for its turn (", " of its 1h30m0s budget; this run is next in line"} {
		if !strings.Contains(stdout.String(), said) {
			t.Errorf("stdout %q lacks %q", stdout.String(), said)
		}
	}
	released := time.Now()
	turn.Release()
	select {
	case exit := <-exited:
		if exit != 0 || !strings.Contains(stdout.String(), "ran go test") {
			t.Fatalf("exit = %d, want 0 with the tests run; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("the run did not finish within a minute of the turn being free; stdout=%s", stdout.String())
	}
	for _, said := range []string{"cfo gate test: took its turn after ", "s of it waiting for its turn; report "} {
		if !strings.Contains(stdout.String(), said) {
			t.Errorf("stdout %q lacks %q", stdout.String(), said)
		}
	}
	report, _ := lastReport(t)
	if report.QueueSeconds < 1 || report.QueueNote != "" || report.Status != "passed" {
		t.Errorf("the report says the run waited %v seconds for its turn, notes %q and has status %q; want a second or more, no note and passed", report.QueueSeconds, report.QueueNote, report.Status)
	}
	if len(report.Checks) != 2 || report.Checks[1].Start.Before(released) {
		t.Errorf("the report's checks are %+v; want the tests to have started only once the turn was given up at %s, so the wait is no part of their time", report.Checks, released)
	}
}

// While a run holds its turn the store names it, which is what a run waiting
// behind it reads: its project, commit, level and directory, the task when it
// is a goblin's own run, and the budget its level gives it.
func TestGateTestNamesItselfAndItsBudgetWhileItHoldsItsTurn(t *testing.T) {
	for name, test := range map[string]struct {
		args   []string
		level  string
		budget time.Duration
	}{
		"the affected level": {nil, "affected", 90 * time.Minute},
		"the full level":     {[]string{"--level", "full"}, "full", 3 * time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
			t.Chdir(dir)
			t.Setenv("CFO_TASK_ID", "cg-example")
			t.Setenv("NO_MISTAKES_GATE", "")
			slots := filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots")
			// The run's tests last until the test has looked at its turn.
			looked := make(chan struct{})
			runtime := standIn()
			runtime.gateRun = func(command []string, _ string, _ []string, _, _ io.Writer) (int, error) {
				if command[1] == "test" {
					<-looked
				}
				return 0, nil
			}

			// Act
			var stdout, stderr lockedBuffer
			exited := make(chan int, 1)
			go func() { exited <- gateTestWith(runtime, &stdout, &stderr, test.args...) }()
			// A run takes its turn and then leaves its card, so the test looks
			// until the holder has one.
			var holding []verify.Standing
			for deadline := time.Now().Add(30 * time.Second); (len(holding) == 0 || holding[0].Budget == 0) && len(exited) == 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				holding, _, _ = verify.Line(slots)
			}
			close(looked)

			// Assert
			select {
			case exit := <-exited:
				if exit != 0 {
					t.Errorf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
				}
			case <-time.After(time.Minute):
				t.Fatalf("the run had not finished a minute after its tests ended; stdout=%s", stdout.String())
			}
			if len(holding) != 1 {
				t.Fatalf("%d run(s) held a turn while the run's tests ran, want 1; stdout=%s stderr=%s", len(holding), stdout.String(), stderr.String())
			}
			who, task, _ := strings.Cut(holding[0].Who, ", task ")
			prefix := fmt.Sprintf("m at %.8s, %s level, in ", revision(t, dir, "HEAD"), test.level)
			if !strings.HasPrefix(who, prefix) || !fsx.SamePath(strings.TrimPrefix(who, prefix), dir) || task != "cg-example" {
				t.Errorf("the run named itself %q; want %q, the repository's directory %s, then the task cg-example", holding[0].Who, prefix, dir)
			}
			if holding[0].Budget != test.budget || holding[0].PID != os.Getpid() {
				t.Errorf("the run held its turn as pid %d under a budget of %s; want this process, %d, and %s", holding[0].PID, holding[0].Budget, os.Getpid(), test.budget)
			}
		})
	}
}

// A run whose tests ran past the budget of its level does not pass, even with
// every test passing: an exceeded budget never counts as a pass.
func TestGateTestDoesNotPassARunWhoseTestsRanPastTheirBudget(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	runtime := standIn()
	runtime.gateBudget = func(gatetest.Level) time.Duration { return time.Millisecond }
	runtime.gateRun = func(command []string, _ string, _ []string, stdout, _ io.Writer) (int, error) {
		// Longer than the budget, by more than a clock's coarsest step.
		time.Sleep(20 * time.Millisecond)
		fmt.Fprintf(stdout, "ran go %s\n", command[1])
		return 0, nil
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTestWith(runtime, &stdout, &stderr)

	// Assert
	if exit != 1 || !strings.Contains(stdout.String(), "ran go test") {
		t.Fatalf("exit = %d, want 1 with the tests run to their end; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{"cfo gate test: go test passed but ran for ", ", past the 1ms budget of the affected level, so the run does not pass"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr %q lacks %q", stderr.String(), want)
		}
	}
	if !strings.Contains(stdout.String(), "cfo gate test: failed at level affected") {
		t.Errorf("stdout %q lacks the verdict line of a failed run", stdout.String())
	}
	report, _ := lastReport(t)
	if report.Status != "failed" || len(report.Checks) != 2 || report.Checks[0].Status != "passed" || report.Checks[1].Status != "over_budget" || report.Checks[1].ExitCode != 0 {
		t.Errorf("the report has status %q and checks %+v; want failed, with vet passed, which has no budget, and the tests over_budget on exit code 0", report.Status, report.Checks)
	}
}

// A run takes the turn of a run that has held it past its own budget, so a
// run that hangs cannot stop every run behind it, and says whose turn it took
// in its output and in its report.
func TestGateTestTakesTheTurnOfARunPastItsBudgetAndSaysSo(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	slots := filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots")
	if err := os.MkdirAll(slots, 0o755); err != nil {
		t.Fatal(err)
	}
	// A holder recorded from another machine cannot be checked, so it counts
	// as still running, as a run that hangs does.
	hung, err := json.Marshal(lock.Info{PID: 4242, OwnerPID: 4242, Hostname: "another-machine", Acquired: time.Now().Add(-2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"slot-1": hung, "slot-1.run": []byte(`{"who":"a run that hangs","budget_seconds":5400}`)} {
		if err := os.WriteFile(filepath.Join(slots, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTestWith(standIn(), &stdout, &stderr)

	// Assert
	if exit != 0 || !strings.Contains(stdout.String(), "ran go test") {
		t.Fatalf("exit = %d, want 0 with the tests run; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	took := "it took the turn from a run that hangs (pid 4242), which had held it for 2h0m"
	for _, want := range []string{"cfo gate test: took its turn after ", took, " against a budget of 1h30m0s and still runs"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
	if report, _ := lastReport(t); !strings.HasPrefix(report.QueueNote, took) {
		t.Errorf("the report notes %q of the run's turn; want it to start %q", report.QueueNote, took)
	}
}

// The fast level takes no turn: it is seconds of static checks and quick
// tests, and it runs while another run holds the machine's turn.
func TestGateTestAtFastTakesNoTurn(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	holdTheTurn(t)

	// Act
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)
	go func() { exited <- gateTestWith(standIn(), &stdout, &stderr, "--level", "fast") }()

	// Assert
	select {
	case exit := <-exited:
		if exit != 0 || strings.Contains(stdout.String(), "waiting for its turn") || !strings.Contains(stdout.String(), "ran go test") {
			t.Fatalf("exit=%d stdout=%q stderr=%q, want 0 with the tests run and no wait", exit, stdout.String(), stderr.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("the fast run had not finished after a minute behind a held turn; stdout=%s", stdout.String())
	}
	if report, _ := lastReport(t); report.QueueSeconds != 0 {
		t.Errorf("the report says the fast run waited %v seconds; want 0", report.QueueSeconds)
	}
}

// A run waits while the machine's available memory is under the fleet's
// floor, says so, and runs once the memory is there.
func TestGateTestWaitsForTheMemoryFloorBeforeItRunsItsTests(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	var readings atomic.Int32
	runtime := standIn()
	runtime.availableMemory = func() (uint64, error) {
		if readings.Add(1) <= 2 {
			return 1 << 30, nil
		}
		return 16 << 30, nil
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTestWith(runtime, &stdout, &stderr)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	waited := "cfo gate test: waiting for its turn (0s so far): 1.0 GB of memory is available and the floor is 4.0 GB; this run is next in line"
	took := "cfo gate test: took its turn after "
	if said := stdout.String(); !strings.Contains(said, waited) || !strings.Contains(said, took) || strings.Index(said, "ran go test") < strings.Index(said, took) {
		t.Errorf("stdout %q; want %q, then %q, and only then the tests run", said, waited, took)
	}
}

// The machine's setting lets more than one run test at once: with
// CFO_VERIFY_SLOTS at 2, a run starts its tests while another holds a turn.
func TestGateTestTakesASecondTurnWhenTheMachineAllowsTwo(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	holdTheTurn(t)
	t.Setenv("CFO_VERIFY_SLOTS", "2")

	// Act
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)
	go func() { exited <- gateTestWith(standIn(), &stdout, &stderr) }()

	// Assert
	select {
	case exit := <-exited:
		if exit != 0 || strings.Contains(stdout.String(), "waiting for its turn") || !strings.Contains(stdout.String(), "ran go test") {
			t.Fatalf("exit=%d stdout=%q stderr=%q, want 0 with the tests run and no wait", exit, stdout.String(), stderr.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("the run had not finished after a minute with a second turn free; stdout=%s", stdout.String())
	}
}

// The command's tests take turns one at a time on any machine: one that sets
// CFO_VERIFY_SLOTS for itself, as the documentation tells its user to, does
// not hand a test's run a second turn behind the holder.
func TestGateTestsTakeTurnsOneAtATimeWhateverTheMachineSets(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_SLOTS", "2")
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	turn := holdTheTurn(t)

	// Act
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)
	go func() { exited <- gateTestWith(standIn(), &stdout, &stderr) }()

	// Assert
	want := fmt.Sprintf("the turn is held by another run (pid %d), for ", os.Getpid())
	for deadline := time.Now().Add(2 * time.Minute); !strings.Contains(stdout.String(), want); time.Sleep(20 * time.Millisecond) {
		select {
		case exit := <-exited:
			t.Fatalf("the run exited %d without waiting for its turn; stdout=%s", exit, stdout.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("stdout %q lacks %q after two minutes", stdout.String(), want)
		}
	}
	turn.Release()
	select {
	case exit := <-exited:
		if exit != 0 || !strings.Contains(stdout.String(), "ran go test") {
			t.Fatalf("exit = %d, want 0 with the tests run; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("the run did not finish within a minute of the turn being free; stdout=%s", stdout.String())
	}
}

// A run whose wait fails says that it takes no turn and runs its tests, and
// the time it had waited stays on record apart from its checks' own: in the
// verdict line and in the report's queue_seconds.
func TestGateTestRecordsTheWaitOfATurnItCouldNotTake(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	holdTheTurn(t)
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)
	go func() { exited <- gateTestWith(standIn(), &stdout, &stderr) }()
	for deadline := time.Now().Add(2 * time.Minute); !strings.Contains(stdout.String(), "cfo gate test: waiting for its turn ("); time.Sleep(20 * time.Millisecond) {
		select {
		case exit := <-exited:
			t.Fatalf("the run exited %d without waiting for its turn; stdout=%s", exit, stdout.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("stdout %q lacks the run's waiting line after two minutes", stdout.String())
		}
	}
	// The wait has to reach a second to show in the verdict, which rounds it.
	time.Sleep(1200 * time.Millisecond)

	// Act
	if err := os.RemoveAll(filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots", "line")); err != nil {
		t.Fatal(err)
	}

	// Assert
	select {
	case exit := <-exited:
		if exit != 0 || !strings.Contains(stdout.String(), "ran go test") || !strings.Contains(stderr.String(), "cfo gate test: this run takes no turn") {
			t.Fatalf("exit=%d stdout=%q stderr=%q, want 0 with the tests run and a line saying the run takes no turn", exit, stdout.String(), stderr.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("the run did not go on within a minute of its line being gone; stdout=%s", stdout.String())
	}
	if said := "s of it waiting for its turn; report "; !strings.Contains(stdout.String(), said) {
		t.Errorf("stdout %q lacks %q", stdout.String(), said)
	}
	if report, _ := lastReport(t); report.QueueSeconds < 1 || report.Status != "passed" {
		t.Errorf("the report says the run waited %v seconds for its turn and has status %q; want a second or more and passed", report.QueueSeconds, report.Status)
	}
}

// cfo gate turns shows the line from outside a run: which run holds the
// turn, for how long and against what budget, and which runs wait, in order.
func TestGateTurnsShowsWhoHoldsTheTurnAndWhoWaits(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	holdTheTurn(t)
	waitInLine(t, verify.Admission{Dir: filepath.Join(store, "slots"), Slots: 1, Who: "a run that waits", Budget: time.Hour, Limit: time.Minute, Poll: 10 * time.Millisecond})

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTurns(plenty, &stdout, &stderr)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	holder := fmt.Sprintf("turn: another run (pid %d), for ", os.Getpid())
	waiter := fmt.Sprintf("1. a run that waits (pid %d), for ", os.Getpid())
	if len(lines) != 3 || !strings.HasPrefix(lines[0], holder) || !strings.HasSuffix(lines[0], " of its 1h30m0s budget") || lines[1] != "waiting:" || !strings.HasPrefix(lines[2], waiter) {
		t.Errorf("cfo gate turns printed %q; want the holder with its time and budget, then the one waiting run", lines)
	}
}

// A holder whose card cannot be read still shows, without a budget no one
// knows.
func TestGateTurnsShowsAHolderThatDidNotNameItself(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	holdTheTurn(t)
	if err := os.Remove(filepath.Join(store, "slots", "slot-1.run")); err != nil {
		t.Fatal(err)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTurns(plenty, &stdout, &stderr)

	// Assert
	want := fmt.Sprintf("turn: a run that did not name itself (pid %d), for ", os.Getpid())
	if line := strings.TrimSpace(stdout.String()); exit != 0 || !strings.HasPrefix(line, want) || strings.Contains(line, "budget") || strings.Contains(line, "\n") {
		t.Errorf("exit=%d stdout=%q stderr=%q; want 0 and one line starting %q that names no budget", exit, stdout.String(), stderr.String(), want)
	}
}

// With no run testing and none waiting, cfo gate turns says so.
func TestGateTurnsSaysWhenTheLineIsEmpty(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTurns(plenty, &stdout, &stderr)

	// Assert
	if want := "no run holds the turn and none waits\n"; exit != 0 || stdout.String() != want {
		t.Errorf("exit=%d stdout=%q stderr=%q; want 0 and %q", exit, stdout.String(), stderr.String(), want)
	}
}

// A run that waits for memory waits with the turn free, so cfo gate turns
// says the turn is free, who waits, and that the machine is short of the
// memory a run waits for.
func TestGateTurnsShowsARunWaitingForMemory(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	short := func() (uint64, error) { return 1 << 30, nil }
	waitInLine(t, verify.Admission{Dir: filepath.Join(store, "slots"), Slots: 1, Floor: 4 << 30, Available: short, Who: "a run that waits", Budget: time.Hour, Limit: time.Minute, Poll: 10 * time.Millisecond})

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTurns(short, &stdout, &stderr)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	waiter := fmt.Sprintf("1. a run that waits (pid %d), for ", os.Getpid())
	if len(lines) != 4 || lines[0] != "turn: free" || lines[1] != "waiting:" || !strings.HasPrefix(lines[2], waiter) || lines[3] != "memory: 1.0 GB is available, under the 4.0 GB floor a run waits for" {
		t.Errorf("cfo gate turns printed %q; want the turn free, the one waiting run, then the memory the machine is short of", lines)
	}
}

// CFO_VERIFY_SLOTS sets how many runs test at once on the machine, one when
// it is not set, and a setting that is not a number above 0 is said, never
// read silently as some number.
func TestGateSlotsReadsTheMachinesSetting(t *testing.T) {
	for setting, want := range map[string]struct {
		slots   int
		problem string
	}{
		"":    {1, ""},
		"1":   {1, ""},
		"3":   {3, ""},
		"0":   {1, `CFO_VERIFY_SLOTS is "0", not a number above 0, so one run tests at a time`},
		"-2":  {1, `CFO_VERIFY_SLOTS is "-2", not a number above 0, so one run tests at a time`},
		"two": {1, `CFO_VERIFY_SLOTS is "two", not a number above 0, so one run tests at a time`},
	} {
		if slots, problem := gateSlots(setting); slots != want.slots || problem != want.problem {
			t.Errorf("gateSlots(%q) = %d, %q; want %d, %q", setting, slots, problem, want.slots, want.problem)
		}
	}
}

// A run that cannot take turns, because the store's folder cannot be made,
// says so and still runs its tests, whose failure is the run's: the store
// never decides the verdict.
func TestGateTestRunsItsTestsWhenItCanTakeNoTurn(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	blocked := filepath.Join(t.TempDir(), "a-file-where-the-store-should-be")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_VERIFY_DIR", blocked)
	runtime := standIn()
	runtime.gateRun = func(command []string, _ string, _ []string, stdout, _ io.Writer) (int, error) {
		fmt.Fprintf(stdout, "ran go %s\n", command[1])
		if command[1] == "test" {
			return 1, errors.New("exit status 1")
		}
		return 0, nil
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTestWith(runtime, &stdout, &stderr)

	// Assert
	if exit != 1 || !strings.Contains(stdout.String(), "ran go test") || !strings.Contains(stderr.String(), "cfo gate test: this run takes no turn") {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want 1 from the failing tests and a line saying the run takes no turn", exit, stdout.String(), stderr.String())
	}
}
