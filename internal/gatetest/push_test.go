package gatetest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// checkout is a branch in a folder of the test's own that holds files, in a
// module of the packages given, whose default branch has policy as its
// verification policy. The branch changed the files named.
func checkout(t *testing.T, policy string, files map[string]string, packages []string, changed ...string) findings {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	found := findings{
		base:      "1111111111111111111111111111111111111111",
		commit:    "2222222222222222222222222222222222222222",
		root:      dir,
		module:    "example.com/repo",
		changed:   changed,
		policy:    policy,
		hasPolicy: policy != "",
	}
	for _, spec := range packages {
		name, imports, _ := strings.Cut(spec, " imports ")
		p := Package{ImportPath: "example.com/repo/" + name, Dir: filepath.Join(dir, filepath.FromSlash(name))}
		if name == "." {
			p = Package{ImportPath: "example.com/repo", Dir: dir}
		}
		for _, imported := range strings.Fields(imports) {
			if imported == "." {
				p.Imports = append(p.Imports, "example.com/repo")
				continue
			}
			p.Imports = append(p.Imports, "example.com/repo/"+imported)
		}
		found.packages = append(found.packages, p)
	}
	return found
}

// fleet is a policy shaped as this repository's: cmd/cfo and
// internal/supervisor are slow, the root package and one test of cmd/cfo
// guard the whole tree, and docs and the board are outside the Go checks.
const fleet = `{
	"version": 1,
	"slow_packages": ["cmd/cfo", "internal/supervisor"],
	"guards": [
		{"package": ".", "why": "its tests read every link of the contract"},
		{"package": "cmd/cfo", "tests": ["TestEveryCommandSaysWhetherItActsAsTheCFO"], "why": "it reads every command"}
	],
	"contracts": [],
	"outside": [{"paths": ["docs/**", "frontend/**"], "why": "no Go check reads them"}]
}`

var fleetPackages = []string{".", "internal/auth", "internal/state imports internal/auth", "internal/supervisor imports internal/state", "cmd/cfo imports internal/auth internal/supervisor"}

// whats are the checks a pick runs, each as the plan names it with why.
func whats(pick Push) []string {
	var names []string
	for _, step := range pick.Steps {
		names = append(names, step.String())
	}
	return names
}

func lefts(pick Push) []string {
	var names []string
	for _, left := range pick.Left {
		names = append(names, left.String())
	}
	return names
}

// A changed file picks its package and, after it, the packages that import
// it, nearest first: a break in a package the goblin never opened is what CI
// found first until now.
func TestPushPicksTheChangedPackageThenItsImportersNearestFirst(t *testing.T) {
	// Arrange
	found := checkout(t, fleet, nil, []string{".", "internal/auth", "internal/state imports internal/auth", "internal/wake imports internal/state"}, "internal/auth/need.go")

	// Act
	pick := push(found, nil)

	// Assert
	want := []string{
		"go vet of 3 packages (they build against what changed)",
		"guard tests of the root package (its tests read every link of the contract)",
		"tests of internal/auth (changed)",
		"tests of internal/state (imports internal/auth)",
		"tests of internal/wake (imports internal/state)",
	}
	if got := whats(pick); !slices.Equal(got, want) {
		t.Fatalf("push picks\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got, want := pick.Steps[2].Command, []string{"go", "test", "-json", "-count=1", "-p", "2", "-timeout", "0", "./internal/auth"}; !slices.Equal(got, want) {
		t.Errorf("the changed package runs as %q, want %q", got, want)
	}
}

// A package that imports the root package says so by the name a pick gives
// the root package, not by the module's path.
func TestPushNamesTheRootPackageAnImporterBuildsAgainst(t *testing.T) {
	// Arrange
	found := checkout(t, fleet, nil, []string{".", "internal/install imports ."}, "home_files.go")

	// Act
	pick := push(found, nil)

	// Assert
	if got, want := whats(pick), "tests of internal/install (imports the root package)"; !slices.Contains(got, want) {
		t.Errorf("push picks\n%s\nwant among them %s", strings.Join(got, "\n"), want)
	}
}

// A new command is a change to cmd/cfo, a slow package whose own tests the
// pick narrows: the guard that reads every command runs whatever the change
// touched, since a new command that says nothing of whose it is fails it.
func TestPushPicksTheGuardThatReadsEveryCommandForANewCommand(t *testing.T) {
	// Arrange
	found := checkout(t, fleet, map[string]string{
		"cmd/cfo/main.go":          "package main\n",
		"cmd/cfo/cfo_acts_test.go": "package main\n\nfunc TestEveryCommandSaysWhetherItActsAsTheCFO(t *testing.T) {}\n",
	}, fleetPackages, "cmd/cfo/main.go", "cmd/cfo/process_plan.go")

	// Act
	pick := push(found, nil)

	// Assert
	want := Step{
		What:    "guard tests of cmd/cfo",
		Why:     "it reads every command",
		Detail:  []string{"TestEveryCommandSaysWhetherItActsAsTheCFO"},
		Command: []string{"go", "test", "-json", "-count=1", "-p", "2", "-timeout", "0", "-run", "^(TestEveryCommandSaysWhetherItActsAsTheCFO)$", "./cmd/cfo"},
	}
	if !slices.ContainsFunc(pick.Steps, func(step Step) bool {
		return step.What == want.What && step.Why == want.Why && slices.Equal(step.Detail, want.Detail) && slices.Equal(step.Command, want.Command)
	}) {
		t.Errorf("push picks\n%s\nwant among them %s, naming %q, as %q", strings.Join(whats(pick), "\n"), want, want.Detail, want.Command)
	}
}

// A doc is outside the Go checks, so a change to one reaches no package: the
// guards still run, since the root package's tests read every link the
// contract holds and a doc that moved breaks one.
func TestPushPicksTheGuardThatReadsEveryLinkForADocChange(t *testing.T) {
	// Arrange
	found := checkout(t, fleet, nil, fleetPackages, "docs/native-board.md")

	// Act
	pick := push(found, nil)

	// Assert
	want := []string{
		"guard tests of the root package (its tests read every link of the contract)",
		"guard tests of cmd/cfo (it reads every command)",
	}
	if got := whats(pick); !slices.Equal(got, want) {
		t.Fatalf("push picks\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got, want := pick.Steps[0].Command, []string{"go", "test", "-json", "-count=1", "-p", "2", "-timeout", "0", "."}; !slices.Equal(got, want) {
		t.Errorf("the root package's guard runs as %q, want %q", got, want)
	}
}

// A package the change reaches whole needs no guard run of its own: its
// tests, the guard among them, run once.
func TestPushRunsAGuardOnceWhenItsPackageRunsWhole(t *testing.T) {
	// Arrange
	found := checkout(t, fleet, nil, fleetPackages, "home_files.go")

	// Act
	pick := push(found, nil)

	// Assert
	if got := whats(pick); !slices.Contains(got, "tests of the root package (changed)") || slices.ContainsFunc(got, func(what string) bool { return strings.HasPrefix(what, "guard tests of the root package") }) {
		t.Errorf("push picks\n%s\nwant the root package's tests once, as changed", strings.Join(got, "\n"))
	}
}

// In a slow package this machine has timed, the pick runs every test in a
// test file the change touched and every other test that took under
// SlowTest here. The slower ones it leaves to CI, and says how many and how
// long they take. A test file beside a changed source is not touched: on
// 2026-10-10 that rule ran 37 slow tests for a change to one file of
// cmd/cfo, seven minutes of a fifteen-minute limit, where the tests that
// fail a pull request are in the test files it changed.
func TestPushRunsASlowPackageWithoutTheSlowTestsTheChangeDidNotTouch(t *testing.T) {
	// Arrange
	found := checkout(t, fleet, map[string]string{
		"internal/supervisor/afk.go":                 "package supervisor\n",
		"internal/supervisor/afk_windows_test.go":    "package supervisor\n\nfunc TestTheSwitchWaits(t *testing.T) {}\n",
		"internal/supervisor/reviews_test.go":        "package supervisor\n\nfunc TestAReviewIsKept(t *testing.T) {}\nfunc TestAReviewIsShown(t *testing.T) {}\n",
		"internal/supervisor/serve_windows_test.go":  "package supervisor\n\nfunc TestServeTakesTheLock(t *testing.T) {}\nfunc TestServeEndsAWatcher(t *testing.T) {}\nfunc TestServeAnswers(t *testing.T) {}\n",
		"internal/supervisor/overlap-wakes_test.go":  "package supervisor\n\nfunc TestOverlapReadThatKeepsFailingWakesTheCFO(t *testing.T) {}\n",
		"internal/supervisor/helpers_for_test.go":    "package supervisor\n\nfunc TestMain(m *testing.M) {}\n",
		"internal/supervisor/reviews_helper_test.go": "package supervisor\n\nfunc testReview() {}\n",
	}, fleetPackages, "internal/supervisor/afk.go", "internal/supervisor/reviews_test.go")
	times := Times{"example.com/repo/internal/supervisor": {
		"TestTheSwitchWaits":     30,
		"TestAReviewIsKept":      12,
		"TestAReviewIsShown":     0.1,
		"TestServeTakesTheLock":  45,
		"TestServeEndsAWatcher":  34.6,
		"TestServeAnswers":       1.9,
		"TestATestThatIsGoneNow": 99,
	}}

	// Act
	pick := push(found, times)

	// Assert
	want := Step{
		What:    "tests of internal/supervisor but for 3 slower ones",
		Why:     "changed",
		Command: []string{"go", "test", "-json", "-count=1", "-p", "2", "-timeout", "0", "-skip", "^(TestServeEndsAWatcher|TestServeTakesTheLock|TestTheSwitchWaits)$", "./internal/supervisor"},
	}
	if !slices.ContainsFunc(pick.Steps, func(step Step) bool {
		return step.What == want.What && step.Why == want.Why && slices.Equal(step.Command, want.Command)
	}) {
		t.Errorf("push picks\n%s\nwant among them %s as %q", strings.Join(whats(pick), "\n"), want, want.Command)
	}
	if wantLeft := "3 tests of internal/supervisor (they take 2s or longer each here, 1m50s in all, and are in files the change did not touch)"; !slices.Contains(lefts(pick), wantLeft) {
		t.Errorf("push leaves to CI\n%s\nwant among it %s", strings.Join(lefts(pick), "\n"), wantLeft)
	}
}

// A slow package this machine has never timed runs whole when it changed,
// which is what times it, and is left to CI when it only imports what
// changed: a first run cannot tell its quick tests from its slow ones.
func TestPushRunsAnUntimedSlowPackageWholeOnlyWhenItChanged(t *testing.T) {
	// Arrange
	found := checkout(t, fleet, nil, fleetPackages, "internal/supervisor/afk.go")

	// Act
	pick := push(found, nil)

	// Assert
	if got := whats(pick); !slices.Contains(got, "tests of internal/supervisor (changed)") || slices.ContainsFunc(got, func(what string) bool { return strings.HasPrefix(what, "tests of cmd/cfo") }) {
		t.Errorf("push picks\n%s\nwant internal/supervisor whole and no test of cmd/cfo but its guard", strings.Join(got, "\n"))
	}
	if wantLeft := "tests of cmd/cfo (imports internal/supervisor, and it is a slow package this machine has not timed)"; !slices.Contains(lefts(pick), wantLeft) {
		t.Errorf("push leaves to CI\n%s\nwant among it %s", strings.Join(lefts(pick), "\n"), wantLeft)
	}
}

// A slow package that only imports what changed runs its quick tests once
// this machine has timed it, after the quick packages nearer the change.
func TestPushRunsATimedSlowImporterWithoutItsSlowTests(t *testing.T) {
	// Arrange
	found := checkout(t, fleet, map[string]string{
		"cmd/cfo/spawn_test.go": "package main\n\nfunc TestSpawnStarts(t *testing.T) {}\nfunc TestSpawnWaits(t *testing.T) {}\n",
	}, fleetPackages, "internal/auth/need.go")
	times := Times{"example.com/repo/cmd/cfo": {"TestSpawnStarts": 0.4, "TestSpawnWaits": 61}}

	// Act
	pick := push(found, times)

	// Assert
	got := whats(pick)
	state, cfo := slices.Index(got, "tests of internal/state (imports internal/auth)"), slices.Index(got, "tests of cmd/cfo but for 1 slower one (imports internal/auth)")
	if state < 0 || cfo < state {
		t.Errorf("push picks\n%s\nwant internal/state and after it cmd/cfo but for its slow test", strings.Join(got, "\n"))
	}
	if wantLeft := "1 test of cmd/cfo (it takes 1m1s here)"; !slices.Contains(lefts(pick), wantLeft) {
		t.Errorf("push leaves to CI\n%s\nwant among it %s", strings.Join(lefts(pick), "\n"), wantLeft)
	}
}

// A changed module file reaches every package: the pick vets them all and
// runs each, and says what it cannot bound.
func TestPushReachesEveryPackageWhenAModuleFileChanged(t *testing.T) {
	// Arrange
	found := checkout(t, fleet, nil, []string{".", "internal/auth", "internal/state imports internal/auth"}, "go.mod")

	// Act
	pick := push(found, nil)

	// Assert
	want := []string{
		"go vet of every package (go.mod or go.sum changed)",
		"tests of the root package (go.mod or go.sum changed)",
		"tests of internal/auth (go.mod or go.sum changed)",
		"tests of internal/state (go.mod or go.sum changed)",
	}
	if got := whats(pick); !slices.Equal(got, want) {
		t.Errorf("push picks\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A changed file the policy does not account for could reach anything, so
// the pick says that CI's run of every package is its check.
func TestPushSaysWhatAFileThePolicyDoesNotKnowLeavesToCI(t *testing.T) {
	// Arrange
	found := checkout(t, fleet, nil, fleetPackages, "internal/auth/need.go", "scripts/new.ps1")

	// Act
	pick := push(found, nil)

	// Assert
	if wantLeft := "tests of every other package (scripts/new.ps1 is in no package, under no contract and not listed as outside the Go checks)"; !slices.Contains(lefts(pick), wantLeft) {
		t.Errorf("push leaves to CI\n%s\nwant among it %s", strings.Join(lefts(pick), "\n"), wantLeft)
	}
}

// Without a policy on the default branch the pick has no guard and no slow
// package to go by, and says so in what it names as its policy.
func TestPushWithoutAPolicyPicksTheChangedPackagesAndSaysSo(t *testing.T) {
	// Arrange
	found := checkout(t, "", nil, fleetPackages, "internal/auth/need.go")

	// Act
	pick := push(found, nil)

	// Assert
	if got := whats(pick); !slices.Contains(got, "tests of internal/auth (changed)") || slices.ContainsFunc(got, func(what string) bool { return strings.HasPrefix(what, "guard") }) {
		t.Errorf("push picks\n%s\nwant the changed package and no guard", strings.Join(got, "\n"))
	}
	if want := "built-in defaults, as 11111111 has no config/verify.json"; pick.Policy != want {
		t.Errorf("policy = %q, want %q", pick.Policy, want)
	}
}

// Nothing a pick says of itself holds a semicolon or a long dash: its lines
// are read by a person as they are.
func TestPushNamesItsChecksInPlainWords(t *testing.T) {
	// Arrange
	found := checkout(t, fleet, map[string]string{
		"cmd/cfo/spawn_test.go":           "package main\n\nfunc TestSpawnWaits(t *testing.T) {}\n",
		"frontend/node_modules/.keep":     "",
		"frontend/tests/afk.spec.ts":      "",
		"frontend/tests/run-card.spec.ts": "",
		"frontend/src/RunCard.tsx":        "",
	}, fleetPackages, "internal/auth/need.go", "scripts/new.ps1", "frontend/src/RunCard.tsx", "cmd/cfo/main.go")

	// Act
	pick := push(found, Times{"example.com/repo/cmd/cfo": {"TestSpawnWaits": 61}})

	// Assert
	said := append(whats(pick), lefts(pick)...)
	for _, step := range pick.Steps {
		said = append(said, step.Detail...)
	}
	if len(pick.Steps) < 8 || len(pick.Left) < 3 {
		t.Fatalf("push picks %d checks and leaves %d, so this test reads too little:\n%s", len(pick.Steps), len(pick.Left), strings.Join(said, "\n"))
	}
	for _, line := range said {
		if strings.ContainsAny(line, ";—–") {
			t.Errorf("%q holds a semicolon or a long dash", line)
		}
	}
}

// A check of a package's tests can be run again for some of them alone,
// which is how a run tells a test its change broke from one a busy machine
// failed.
func TestStepAgainRunsTheNamedTestsOfItsPackageByThemselves(t *testing.T) {
	// Arrange
	step := Step{What: "tests of cmd/cfo but for 2 slower ones", Command: []string{"go", "test", "-json", "-count=1", "-p", "2", "-timeout", "0", "-skip", "^(TestSlow|TestSlower)$", "./cmd/cfo"}}

	// Act
	again := step.Again([]string{"TestRestart", "TestSpawn"})

	// Assert
	if want := []string{"go", "test", "-json", "-count=1", "-p", "2", "-timeout", "0", "-run", "^(TestRestart|TestSpawn)$", "./cmd/cfo"}; !slices.Equal(again, want) {
		t.Errorf("Again = %q, want %q", again, want)
	}
}
