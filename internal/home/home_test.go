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

func TestResolveDefaultsToCwd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CFO_HOME", "")
	// A goblin pane exports CFO_STATE_OVERRIDE, so the defaults this test
	// asserts only hold once that inherited value is cleared too.
	t.Setenv("CFO_STATE_OVERRIDE", "")
	t.Chdir(dir)
	h, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.EqualFold(filepath.Clean(h.Root), filepath.Clean(dir)) {
		t.Errorf("Root = %q, want the cwd %q", h.Root, dir)
	}
	if h.State != filepath.Join(h.Root, "state") || h.Data != filepath.Join(h.Root, "data") {
		t.Errorf("derived dirs wrong: %+v", h)
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
	gitInit(t, dir)
	h := Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	if IsPrimary(h) {
		t.Error("primary without AGENTS.md or state/")
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
	if !IsPrimary(h) {
		t.Error("not primary with AGENTS.md + state/ + plain checkout")
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
	if IsPrimary(h) {
		t.Error("a linked worktree must never be primary")
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

func TestIsPrimaryFalseOutsideGit(t *testing.T) {
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
	if IsPrimary(Home{Root: dir, State: filepath.Join(dir, "state")}) {
		t.Error("primary outside a git checkout")
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
