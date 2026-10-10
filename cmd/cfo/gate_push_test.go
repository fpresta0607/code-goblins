package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// pushPolicy is a default branch's policy for testStepModule's packages: a
// is slow, c guards the whole tree with a test that fails, and the README
// and the board are outside the Go checks.
const pushPolicy = `{
	"version": 1,
	"slow_packages": ["a"],
	"guards": [{"package": "c", "tests": ["TestC"], "why": "it reads the whole tree"}],
	"contracts": [],
	"outside": [{"paths": ["README.md", "frontend/**", "config/**"], "why": "no Go check reads them"}]
}`

// quiet is pushPolicy without the guard, whose test fails.
var quiet = strings.Replace(pushPolicy, `{"package": "c", "tests": ["TestC"], "why": "it reads the whole tree"}`, "", 1)

// passing is package a's test file with one test that passes.
const passing = "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() == 0 {\n\t\tt.Fatal(\"A is 0\")\n\t}\n}\n"

// gatePrepush runs cfo gate prepush on a machine with memory and disk to
// spare.
func gatePrepush(stdout, stderr io.Writer, args ...string) int {
	runtime := defaultCommandRuntime()
	runtime.availableMemory = plenty
	runtime.gateDisk = roomy
	return gatePrepushWith(runtime, stdout, stderr, args...)
}

func gatePrepushWith(runtime commandRuntime, stdout, stderr io.Writer, args ...string) int {
	return runWithRuntime(append([]string{"gate", "prepush"}, args...), stdout, stderr, runtime)
}

// recorded is a runtime on a machine with memory and disk to spare whose
// checks are not processes: each is recorded with the folder it ran in, and
// passes.
func recorded(ran *[]string) commandRuntime {
	runtime := defaultCommandRuntime()
	runtime.availableMemory = plenty
	runtime.gateDisk = roomy
	runtime.pushRun = func(_ context.Context, command []string, dir string, _ []string, _, _ io.Writer) (int, error) {
		*ran = append(*ran, filepath.Base(dir)+": "+strings.Join(command, " "))
		return 0, nil
	}
	return runtime
}

// lastLine is the last line a run printed.
func lastLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// The pick runs one check at a time and stops at the first that fails: the
// change broke a's own test, so b, which imports a, is never tested, and the
// run's last line is one plain sentence naming the check and the test.
func TestGatePrepushStopsAtTheFirstFailureWithOnePlainLine(t *testing.T) {
	// Arrange
	dir := testStepModule(t,
		map[string]string{"config/verify.json": quiet, "a/a_test.go": passing},
		map[string]string{"a/a.go": "package a\n\nfunc A() int { return 0 }\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := gatePrepush(&stdout, &stderr)

	// Assert
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	want := "cfo gate prepush: failed at check 2 of 3, tests of a: TestA failed. CI would fail on it too, so fix it before you push."
	if got := lastLine(stdout.String()); got != want {
		t.Errorf("the run ends with %q, want %q\nstdout=%s", got, want, stdout.String())
	}
	for _, want := range []string{"cfo gate prepush: check 2 of 3: tests of a", "A is 0"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "check 3 of 3") || strings.Contains(stdout.String(), "example.com/m/b") {
		t.Errorf("stdout %q shows a check after the one that failed", stdout.String())
	}
}

// A change that breaks nothing passes: the pick vets what the change
// reaches, tests the changed package and then the package that imports it,
// without the fleet's home, which b's test fails on, and says that all of it
// ran.
func TestGatePrepushRunsTheChangedPackageAndItsImporterAndPasses(t *testing.T) {
	// Arrange
	dir := testStepModule(t,
		map[string]string{"config/verify.json": strings.Replace(quiet, `"slow_packages": ["a"]`, `"slow_packages": []`, 1), "a/a_test.go": passing},
		map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	t.Setenv("CFO_HOME", filepath.Join(dir, "fleet"))

	// Act
	var stdout, stderr bytes.Buffer
	exit := gatePrepush(&stdout, &stderr)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"cfo gate prepush: 1 file changed since ",
		"which picks 3 checks to run within 15m0s",
		"1. go vet of 2 packages (they build against what changed)",
		"2. tests of a (changed)",
		"3. tests of b (imports a)",
		"policy: config/verify.json version 1 at ",
		"ok  \texample.com/m/a",
		"ok  \texample.com/m/b",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
	if got := lastLine(stdout.String()); !strings.HasPrefix(got, "cfo gate prepush: passed. All 3 checks ran in ") {
		t.Errorf("the run ends with %q, want it to say that all 3 checks passed", got)
	}
	if strings.Contains(stdout.String(), "example.com/m/c") {
		t.Errorf("stdout %q names c, which the change does not reach", stdout.String())
	}
}

// A guard runs whatever changed: the branch changed only the README, which
// no Go check reads, and the guard the policy names still runs and fails
// the push.
func TestGatePrepushRunsTheGuardsWhateverChanged(t *testing.T) {
	// Arrange
	dir := testStepModule(t, map[string]string{"config/verify.json": pushPolicy}, map[string]string{"README.md": "changed\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := gatePrepush(&stdout, &stderr)

	// Assert
	want := "cfo gate prepush: failed at check 1 of 1, guard tests of c: TestC: TestC failed. CI would fail on it too, so fix it before you push."
	if got := lastLine(stdout.String()); exit != 1 || got != want {
		t.Errorf("exit = %d and the run ends with %q, want 1 and %q\nstdout=%s stderr=%s", exit, got, want, stdout.String(), stderr.String())
	}
	if want := "1. guard tests of c: TestC (it reads the whole tree)"; !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout %q lacks %q", stdout.String(), want)
	}
}

// The policy is the default branch's, so the guards a branch runs are the
// ones main has: a branch that takes a guard out of its own copy still runs
// it.
func TestGatePrepushRunsTheGuardsOfTheDefaultBranchsPolicy(t *testing.T) {
	// Arrange
	dir := testStepModule(t, map[string]string{"config/verify.json": pushPolicy}, map[string]string{"config/verify.json": quiet})
	t.Chdir(dir)

	// Act
	var ran []string
	var stdout, stderr bytes.Buffer
	exit := gatePrepushWith(recorded(&ran), &stdout, &stderr)

	// Assert
	want := []string{filepath.Base(dir) + ": go test -json -count=1 -p 2 -timeout 0 -run ^(TestC)$ ./c"}
	if exit != 0 || !slices.Equal(ran, want) {
		t.Errorf("exit = %d and the run ran %q, want 0 and %q\nstdout=%s stderr=%s", exit, ran, want, stdout.String(), stderr.String())
	}
}

// A slow package runs without the tests this machine timed at two seconds
// or longer, unless the change touched their file: TestSlow would fail, is
// on record at 30 seconds and is in a file the change left alone, so it is
// left to CI by number and the run passes. TestTouched is on record at 44
// seconds and runs, since the change touched its file. What the run timed
// is kept for the next one.
func TestGatePrepushLeavesTheSlowTestsOfASlowPackageToCIAndKeepsItsTimes(t *testing.T) {
	// Arrange
	touched := "package a\n\nimport \"testing\"\n\nfunc TestTouched(t *testing.T) {}\n"
	dir := testStepModule(t,
		map[string]string{
			"config/verify.json": quiet,
			"a/a_test.go":        passing,
			"a/slow_test.go":     "package a\n\nimport \"testing\"\n\nfunc TestSlow(t *testing.T) { t.Fatal(\"the slow test ran\") }\n",
			"a/untimed_test.go":  "package a\n\nimport \"testing\"\n\nfunc TestNeverTimed(t *testing.T) {}\n",
			"a/touched_test.go":  touched,
		},
		map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n", "a/touched_test.go": touched + "\n// The change says more here.\n"})
	t.Chdir(dir)
	if err := verify.KeepTimes("m", map[string]map[string]float64{"example.com/m/a": {"TestSlow": 30, "TestTouched": 44, "TestA": 0.5}}); err != nil {
		t.Fatal(err)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := gatePrepush(&stdout, &stderr)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"2. tests of a but for 1 slower one (changed)",
		"left to CI:\n- 1 test of a (it takes 30s here and is in a file the change did not touch)",
		"and CI is the check of the 1 left to it, named above.",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
	times, err := verify.Times("m")
	if err != nil {
		t.Fatal(err)
	}
	timed := times["example.com/m/a"]
	if _, isTimed := timed["TestNeverTimed"]; !isTimed || timed["TestSlow"] != 30 || timed["TestTouched"] >= 44 {
		t.Errorf("the record holds %v for a, want TestNeverTimed timed, TestTouched timed again and TestSlow still at 30", timed)
	}
	if _, isTimed := times["example.com/m/b"]["TestB"]; !isTimed {
		t.Errorf("the record holds %v, want b's TestB timed too", times)
	}
}

// The checks stop at the time limit: a check that would start after it is
// left to CI by name, and the run still passes what it ran and says how
// much that was.
func TestGatePrepushLeavesToCIWhatTheTimeLimitCuts(t *testing.T) {
	// Arrange
	dir := testStepModule(t,
		map[string]string{"config/verify.json": strings.Replace(quiet, `"slow_packages": ["a"]`, `"slow_packages": []`, 1), "a/a_test.go": passing},
		map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	var ran []string
	runtime := recorded(&ran)
	run := runtime.pushRun
	runtime.pushRun = func(ctx context.Context, command []string, dir string, env []string, stdout, stderr io.Writer) (int, error) {
		time.Sleep(60 * time.Millisecond)
		return run(ctx, command, dir, env, stdout, stderr)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := gatePrepushWith(runtime, &stdout, &stderr, "--limit", "50ms")

	// Assert
	if exit != 0 || len(ran) != 1 {
		t.Fatalf("exit = %d after %d check(s), want 0 after 1; stdout=%s stderr=%s", exit, len(ran), stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"left to CI, since the time limit of 50ms passed:\n- tests of a (changed)\n- tests of b (imports a)\n",
		"cfo gate prepush: passed what it ran. 1 of 3 checks ran in ",
		"and CI is the check of the 2 left to it, named above.",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
	if said := stdout.String() + stderr.String(); strings.ContainsAny(said, ";—–") {
		t.Errorf("the run's own lines hold a semicolon or a long dash: %q", said)
	}
}

// A check still running at the time limit is ended there and left to CI
// with the ones after it: a run never outlasts its limit, and a check that
// was cut short has not failed.
func TestGatePrepushEndsACheckThatRunsPastTheTimeLimit(t *testing.T) {
	// Arrange
	dir := testStepModule(t,
		map[string]string{"config/verify.json": strings.Replace(quiet, `"slow_packages": ["a"]`, `"slow_packages": []`, 1), "a/a_test.go": passing},
		map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	runtime := defaultCommandRuntime()
	runtime.availableMemory = plenty
	runtime.gateDisk = roomy
	starts := 0
	runtime.pushRun = func(ctx context.Context, _ []string, _ string, _ []string, _, _ io.Writer) (int, error) {
		if starts++; starts == 1 {
			return 0, nil
		}
		<-ctx.Done()
		return -1, ctx.Err()
	}

	// Act
	var stdout, stderr bytes.Buffer
	started := time.Now()
	exit := gatePrepushWith(runtime, &stdout, &stderr, "--limit", "2s")

	// Assert
	if exit != 0 || starts != 2 || time.Since(started) > 30*time.Second {
		t.Fatalf("exit = %d after %d start(s) in %s, want 0 after 2, at the limit; stdout=%s stderr=%s", exit, starts, time.Since(started), stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"left to CI, since the time limit of 2s passed while check 2 ran:\n- tests of a (changed)\n- tests of b (imports a)\n",
		"cfo gate prepush: passed what it ran. 1 of 3 checks ran in ",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
}

// No check starts while the machine is under the memory floor beside a
// working fleet: the checks from there on are left to CI, with what was
// free.
func TestGatePrepushStartsNoCheckWhileMemoryIsUnderTheFloor(t *testing.T) {
	// Arrange
	dir := testStepModule(t,
		map[string]string{"config/verify.json": strings.Replace(quiet, `"slow_packages": ["a"]`, `"slow_packages": []`, 1), "a/a_test.go": passing},
		map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	var ran []string
	runtime := recorded(&ran)
	var isShort atomic.Bool
	runtime.availableMemory = func() (supervisor.Memory, error) {
		if isShort.Load() {
			return supervisor.Memory{Available: 1 << 30, CommitAvailable: 3 << 30}, nil
		}
		return plenty()
	}
	run := runtime.pushRun
	runtime.pushRun = func(ctx context.Context, command []string, dir string, env []string, stdout, stderr io.Writer) (int, error) {
		isShort.Store(true)
		return run(ctx, command, dir, env, stdout, stderr)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := gatePrepushWith(runtime, &stdout, &stderr)

	// Assert
	if exit != 0 || len(ran) != 1 {
		t.Fatalf("exit = %d after %d check(s), want 0 after 1; stdout=%s stderr=%s", exit, len(ran), stdout.String(), stderr.String())
	}
	want := fmt.Sprintf("left to CI, since the machine has 1.0 GB of memory free and a check starts only above %.1f GB:\n- tests of a (changed)\n- tests of b (imports a)\n", verify.Gigabytes(supervisor.MemoryFloor))
	if !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout %q lacks %q", stdout.String(), want)
	}
}

// A run the machine gives no turn runs nothing and says so in one line: it
// has checked nothing, which is no pass.
func TestGatePrepushThatGetsNoTurnRunsNothingAndSaysSo(t *testing.T) {
	// Arrange
	dir := testStepModule(t, map[string]string{"config/verify.json": quiet, "a/a_test.go": passing}, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	var ran []string
	runtime := recorded(&ran)
	runtime.availableMemory = func() (supervisor.Memory, error) { return supervisor.Memory{}, errors.New("no reading") }

	// Act
	var stdout, stderr bytes.Buffer
	exit := gatePrepushWith(runtime, &stdout, &stderr)

	// Assert
	want := "cfo gate prepush: nothing ran, since the machine gave this run no turn: verify: available memory cannot be read: no reading. CI is the only check of this push."
	if got := lastLine(stdout.String()); exit != 1 || len(ran) != 0 || got != want {
		t.Errorf("exit = %d after %d check(s), ending with %q; want 1 after none, ending with %q", exit, len(ran), got, want)
	}
}

// --plan prints the pick, each check with why and the command it would run,
// and what the pick leaves to CI, and runs nothing. A change to the board
// picks the board's checks, which run in its folder.
func TestGatePrepushPlanPrintsThePickAndRunsNothing(t *testing.T) {
	// Arrange
	dir := testStepModule(t,
		map[string]string{
			"config/verify.json":                  quiet,
			"frontend/node_modules/.keep":         "",
			"frontend/src/RunCard.tsx":            "",
			"frontend/tests/run-finishes.spec.ts": "",
			"frontend/tests/afk.spec.ts":          "",
		},
		map[string]string{"frontend/src/RunCard.tsx": "changed\n"})
	t.Chdir(dir)

	// Act
	var ran []string
	var plan, stderr bytes.Buffer
	planExit := gatePrepushWith(recorded(&ran), &plan, &stderr, "--plan")
	planned := len(ran)
	var stdout bytes.Buffer
	exit := gatePrepushWith(recorded(&ran), &stdout, &stderr)

	// Assert
	if planExit != 0 || planned != 0 {
		t.Fatalf("--plan exit = %d and it ran %d check(s), want 0 and none; stdout=%s stderr=%s", planExit, planned, plan.String(), stderr.String())
	}
	for _, want := range []string{
		"cfo gate prepush: 1 file changed since ",
		"1. the board's type check (a file under frontend changed)\n   would run in frontend: npm run typecheck\n",
		"4. 1 browser spec (the change touched or names them)\n   - tests/run-finishes.spec.ts (named like the changed src/RunCard.tsx)\n   would run in frontend: npx playwright test --reporter=line tests/run-finishes.spec.ts\n",
		"left to CI:\n- the 1 other browser spec (the change touched no file of theirs and names none of them)\n",
	} {
		if !strings.Contains(plan.String(), want) {
			t.Errorf("--plan printed %q, which lacks %q", plan.String(), want)
		}
	}
	wantRan := []string{"frontend: npm run typecheck", "frontend: npm run lint", "frontend: npm test", "frontend: npx playwright test --reporter=line tests/run-finishes.spec.ts"}
	if exit != 0 || !slices.Equal(ran, wantRan) {
		t.Errorf("exit = %d and the run ran %q, want 0 and %q\nstdout=%s stderr=%s", exit, ran, wantRan, stdout.String(), stderr.String())
	}
	if said := plan.String() + stdout.String() + stderr.String(); strings.ContainsAny(said, ";—–") {
		t.Errorf("the run's own lines hold a semicolon or a long dash: %q", said)
	}
}

// A check that is no go test fails the push by its exit code, and the last
// line says which check and that what it said is above.
func TestGatePrepushFailsOnACheckOfTheBoardThatExitsNonZero(t *testing.T) {
	// Arrange
	dir := testStepModule(t,
		map[string]string{"config/verify.json": quiet, "frontend/node_modules/.keep": "", "frontend/src/RunCard.tsx": ""},
		map[string]string{"frontend/src/RunCard.tsx": "changed\n"})
	t.Chdir(dir)
	var ran []string
	runtime := recorded(&ran)
	run := runtime.pushRun
	runtime.pushRun = func(ctx context.Context, command []string, dir string, env []string, stdout, stderr io.Writer) (int, error) {
		if exit, err := run(ctx, command, dir, env, stdout, stderr); command[len(command)-1] != "lint" {
			return exit, err
		}
		fmt.Fprintln(stdout, "src/RunCard.tsx: 1 problem")
		return 1, nil
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := gatePrepushWith(runtime, &stdout, &stderr)

	// Assert
	want := "cfo gate prepush: failed at check 2 of 3, the board's lint: it exited 1, and what it said is above. CI would fail on it too, so fix it before you push."
	if got := lastLine(stdout.String()); exit != 1 || len(ran) != 2 || got != want {
		t.Errorf("exit = %d after %d check(s), ending with %q; want 1 after 2, ending with %q\nstdout=%s", exit, len(ran), got, want, stdout.String())
	}
}

// A limit that is no time, and anything after the flags, is refused before
// anything is read.
func TestGatePrepushRefusesWhatItCannotRead(t *testing.T) {
	for name, args := range map[string][]string{
		"no time":         {"--limit", "0s"},
		"not a time":      {"--limit", "soon"},
		"a stray word":    {"now"},
		"an unknown flag": {"--level", "full"},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			var stdout, stderr bytes.Buffer
			exit := gatePrepush(&stdout, &stderr, args...)

			// Assert
			if want := "usage: cfo gate prepush [--plan] [--limit <duration>]"; exit != 2 || !strings.Contains(stderr.String(), want) {
				t.Errorf("exit = %d, stderr = %q; want 2 and %q", exit, stderr.String(), want)
			}
		})
	}
}

// A gate's test step keeps what it timed in the same record a push reads,
// so the runs a machine already makes are what teach it which tests of a
// slow package are slow.
func TestGateTestKeepsTheTimesAPushReads(t *testing.T) {
	// Arrange
	dir := testStepModule(t, map[string]string{"a/a_test.go": passing}, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTest(&stdout, &stderr, "--level", "affected")

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	times, err := verify.Times("m")
	if err != nil {
		t.Fatal(err)
	}
	_, isATimed := times["example.com/m/a"]["TestA"]
	_, isBTimed := times["example.com/m/b"]["TestB"]
	if !isATimed || !isBTimed {
		t.Errorf("the record holds %v, want TestA of a and TestB of b timed", times)
	}
}

// A test that fails is run once more by itself before it fails the push. On
// this machine a test that waits on a real terminal can miss its own deadline
// while another run holds the processors, which is no fault of the change
// and nothing CI would see: TestBusy fails the first time it runs and passes
// the second, so the run says so and goes on. TestA in the first test of
// this file fails both times, which fails the push.
func TestGatePrepushRunsAFailedTestAgainAndPassesOneThatFailedByChance(t *testing.T) {
	// Arrange
	busy := "package a\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestBusy(t *testing.T) {\n\tif _, err := os.Stat(\"ran-once\"); err != nil {\n\t\t_ = os.WriteFile(\"ran-once\", nil, 0o644)\n\t\tt.Fatal(\"the host did not answer in time\")\n\t}\n}\n"
	dir := testStepModule(t,
		map[string]string{"config/verify.json": strings.Replace(quiet, `"slow_packages": ["a"]`, `"slow_packages": []`, 1), "a/a_test.go": passing, "a/busy_test.go": busy},
		map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := gatePrepush(&stdout, &stderr)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"the host did not answer in time",
		"cfo gate prepush: TestBusy failed, so it runs again by itself",
		"cfo gate prepush: TestBusy passed by itself, so this machine was busy and the change did not break it",
		"cfo gate prepush: check 3 of 3: tests of b",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
	if got := lastLine(stdout.String()); !strings.HasPrefix(got, "cfo gate prepush: passed. All 3 checks ran in ") {
		t.Errorf("the run ends with %q, want it to say that all 3 checks passed", got)
	}
}
