package home

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestResolveDefaultsToThePerUserHome(t *testing.T) {
	// Arrange: a goblin pane exports CFO_STATE_OVERRIDE, so the defaults this
	// test asserts only hold once that inherited value is cleared too.
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("CFO_HOME", "")
	t.Setenv("CFO_STATE_OVERRIDE", "")
	t.Chdir(t.TempDir())

	// Act
	h, err := Resolve()

	// Assert: the working directory names no home; the per-user one does.
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := filepath.Join(local, "CodeGoblins")
	if !strings.EqualFold(h.Root, want) {
		t.Errorf("Root = %q, want the per-user home %q", h.Root, want)
	}
	if h.State != filepath.Join(h.Root, "state") || h.Data != filepath.Join(h.Root, "data") {
		t.Errorf("derived dirs wrong: %+v", h)
	}
	for got, want := range map[string]string{h.Bin(): "bin", h.Worktrees(): "worktrees", h.Scratch(): "scratch", h.Caches(): "caches"} {
		if got != filepath.Join(h.Root, want) {
			t.Errorf("layout folder %q, want %q under the root", got, want)
		}
	}
}

func TestResolveWithoutLocalAppDataOrCFOHomeIsRefused(t *testing.T) {
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("CFO_HOME", "")
	t.Setenv("CFO_STATE_OVERRIDE", "")
	if h, err := Resolve(); err == nil {
		t.Fatalf("Resolve = %+v, want a refusal naming CFO_HOME", h)
	}
}

func TestResolveHonorsEnvOverrides(t *testing.T) {
	root := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("CFO_HOME", root)
	t.Setenv("CFO_STATE_OVERRIDE", stateDir)
	h, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if h.Root != root || h.State != stateDir {
		t.Errorf("overrides ignored: %+v", h)
	}
}

func TestIsPrimaryRequiresAllThree(t *testing.T) {
	dir := t.TempDir()
	h := Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	if IsPrimary(h) {
		t.Error("primary without AGENTS.md, state/ or the marker")
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsPrimary(h) {
		t.Error("primary without state/")
	}
	if err := os.Mkdir(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	if IsPrimary(h) {
		t.Error("primary without the marker cfo install writes")
	}
	if err := os.WriteFile(filepath.Join(dir, InstalledMarker), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsPrimary(h) {
		t.Error("not primary with AGENTS.md + state/ + the marker")
	}
}

// A source checkout is never a home: one that holds AGENTS.md and a state
// folder, which is what a checkout an older install wired looks like, is
// refused until a home's marker says otherwise.
func TestIsPrimaryRefusesACheckoutWithoutTheMarker(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if IsPrimary(Home{Root: dir, State: filepath.Join(dir, "state")}) {
		t.Error("a plain checkout with AGENTS.md and state/ but no marker is primary")
	}
}

func TestIsPrimaryFalseInLinkedWorktree(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "c"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", wt).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(wt, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(wt, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := Home{Root: wt, State: filepath.Join(wt, "state")}
	if err := os.WriteFile(filepath.Join(wt, InstalledMarker), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsPrimary(h) {
		t.Error("a linked worktree carrying the installed marker must never be primary")
	}
}

func TestGitPathsUseTheSameCanonicalIdentityAsPrimaryCheck(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)

	gitDir, commonDir, err := gitPaths(dir)
	if err != nil {
		t.Fatalf("gitPaths: %v", err)
	}
	if !fsx.SamePath(gitDir, commonDir) {
		t.Fatalf("plain checkout git paths differ: git=%q common=%q", gitDir, commonDir)
	}
}

func TestIsPrimaryOutsideGitNeedsTheInstalledMarker(t *testing.T) {
	// GOTMPDIR can put the test's temp directory inside a git checkout (an
	// operator's own TMP is wherever they put it), so "outside git"
	// has to be established rather than assumed: git stops its upward search
	// below a ceiling directory, which makes the root genuinely repo-less.
	ceiling := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", ceiling)
	dir := filepath.Join(ceiling, "outside")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := Home{Root: dir, State: filepath.Join(dir, "state")}
	if IsPrimary(h) {
		t.Error("primary outside a git checkout")
	}
	// A home cfo install set up outside a checkout carries the marker, and is
	// primary by it alone.
	if err := os.WriteFile(filepath.Join(dir, InstalledMarker), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsPrimary(h) {
		t.Error("not primary outside git with AGENTS.md, state/ and the installed marker")
	}
}

// IsPrimary runs in the CFO's hooks on every tool call they select, so it
// reads the checkout from its files and starts no git process: with nothing
// on PATH it tells a plain checkout, a folder inside one, a linked worktree
// and a home outside any repository apart exactly as git does.
func TestIsPrimaryReadsTheCheckoutWithoutStartingGit(t *testing.T) {
	// Arrange: every repository is made with git before PATH is emptied.
	ceiling := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", ceiling)
	home := func(root string) Home {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, "state"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"AGENTS.md", InstalledMarker} {
			if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return Home{Root: root, State: filepath.Join(root, "state")}
	}
	checkout := filepath.Join(ceiling, "checkout")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, checkout)
	plain := home(checkout)
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "c"}} {
		if out, err := exec.Command("git", append([]string{"-C", checkout}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	worktreeRoot := filepath.Join(ceiling, "wt")
	if out, err := exec.Command("git", "-C", checkout, "worktree", "add", worktreeRoot).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	worktree := home(worktreeRoot)
	inside := home(filepath.Join(checkout, "nested", "home"))
	installed := home(filepath.Join(ceiling, "installed"))
	t.Setenv("PATH", t.TempDir())

	cases := []struct {
		name string
		home Home
		want bool
	}{
		{"plain checkout", plain, true},
		{"folder inside a plain checkout", inside, true},
		{"linked worktree", worktree, false},
		{"installed home outside any repository", installed, true},
	}
	for _, c := range cases {
		// Act
		got := IsPrimary(c.home)

		// Assert
		if got != c.want {
			t.Errorf("%s: IsPrimary = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsPrimaryNeverCreates(t *testing.T) {
	dir := t.TempDir()
	IsPrimary(Home{Root: dir, State: filepath.Join(dir, "state")})
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("IsPrimary created entries: %v", entries)
	}
}

// The guard that keeps a test suite off the machine's own fleet. A goblin
// pane exports CFO_HOME and CFO_STATE_OVERRIDE, go test inherits both, and a
// test that resolves either writes into the running fleet. This has happened
// twice; the second time a test's PR notification reached the CFO as a real
// goblin report.
func TestResolveRefusesTheInheritedFleetHome(t *testing.T) {
	fleet := t.TempDir()
	fleetState := filepath.Join(fleet, "state")
	if err := os.Mkdir(fleetState, 0o755); err != nil {
		t.Fatal(err)
	}
	// Stand in for the pane's exported values, which are captured at process
	// start and so cannot be set by this test through the environment.
	defer restoreInherited(inheritedRoot, inheritedState)
	inheritedRoot, inheritedState = fleet, fleetState

	t.Run("a test that inherits CFO_HOME", func(t *testing.T) {
		t.Setenv("CFO_HOME", fleet)
		t.Setenv("CFO_STATE_OVERRIDE", "")
		if h, err := Resolve(); err == nil {
			t.Fatalf("Resolve = %+v, want a refusal rather than the inherited fleet home", h)
		}
	})

	// The dangerous one: CFO_STATE_OVERRIDE wins over CFO_HOME, so a test that
	// carefully points CFO_HOME at its own directory still writes its status
	// and wake records into the fleet unless the override is cleared too.
	t.Run("a test that sets CFO_HOME but inherits the state override", func(t *testing.T) {
		t.Setenv("CFO_HOME", t.TempDir())
		t.Setenv("CFO_STATE_OVERRIDE", fleetState)
		if h, err := Resolve(); err == nil {
			t.Fatalf("Resolve = %+v, want a refusal rather than the inherited fleet state", h)
		}
	})

	// With CFO_HOME cleared a home resolves to the per-user one, which on an
	// installed machine is the running fleet.
	t.Run("a test that clears CFO_HOME and keeps the machine's LOCALAPPDATA", func(t *testing.T) {
		defer restoreInheritedDefault(inheritedDefault)
		local := t.TempDir()
		t.Setenv("LOCALAPPDATA", local)
		inheritedDefault = filepath.Join(local, "CodeGoblins")
		t.Setenv("CFO_HOME", "")
		t.Setenv("CFO_STATE_OVERRIDE", "")
		if h, err := Resolve(); err == nil {
			t.Fatalf("Resolve = %+v, want a refusal rather than the inherited per-user home", h)
		}
	})

	// A test may legitimately build a home that looks primary - the hook
	// guards are tested against exactly that - and GOTMPDIR can place it
	// inside the checkout, so neither primaryness nor location may condemn it.
	t.Run("a test's own primary-looking home still resolves", func(t *testing.T) {
		own := t.TempDir()
		gitInit(t, own)
		if err := os.WriteFile(filepath.Join(own, "AGENTS.md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(own, "state"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CFO_HOME", own)
		t.Setenv("CFO_STATE_OVERRIDE", "")
		if _, err := Resolve(); err != nil {
			t.Fatalf("Resolve on the test's own home: %v, want it to work", err)
		}
	})
}

func restoreInherited(root, state string) {
	inheritedRoot, inheritedState = root, state
}

func restoreInheritedDefault(root string) {
	inheritedDefault = root
}

func TestLocateWorktreeReadsBothLayouts(t *testing.T) {
	root := `C:\Users\op\AppData\Local\CodeGoblins\worktrees`
	cases := map[string]WorktreePlace{
		root + `\app\task-1`:                              {Root: root + `\app\task-1`, Project: "app", Name: "task-1"},
		root + `\app\task-1\web\src`:                      {Root: root + `\app\task-1`, Project: "app", Name: "task-1"},
		root + `\app\task-1-proof`:                        {Root: root + `\app\task-1-proof`, Project: "app", Name: "task-1-proof"},
		`C:\dev\app\.worktrees\gb-task-2`:                 {Root: `C:\dev\app\.worktrees\gb-task-2`, Project: "app", Name: "task-2"},
		`c:\DEV\app\.WORKTREES\GB-Task-3\internal`:        {Root: `c:\DEV\app\.WORKTREES\GB-Task-3`, Project: "app", Name: "Task-3"},
		`C:\dev\app\.worktrees\gb-outer\x\.worktrees\gb-inner`: {Root: `C:\dev\app\.worktrees\gb-outer\x\.worktrees\gb-inner`, Project: "x", Name: "inner"},
	}
	for dir, want := range cases {
		got, ok := LocateWorktree(root, dir)
		if !ok || got != want {
			t.Errorf("LocateWorktree(%q) = %+v, %v; want %+v", dir, got, ok, want)
		}
	}
	for _, dir := range []string{root, root + `\app`, `C:\dev\app`, `C:\dev\app\.worktrees\feature`, `C:\dev\app\.worktrees\gb-`, `C:\Users\op\AppData\Local\CodeGoblins\scratch\task-1`} {
		if got, ok := LocateWorktree(root, dir); ok {
			t.Errorf("LocateWorktree(%q) = %+v, want no fleet worktree", dir, got)
		}
	}
}
