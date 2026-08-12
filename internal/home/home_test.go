package home

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
	t.Chdir(dir)
	h, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
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

func TestIsPrimaryFalseOutsideGit(t *testing.T) {
	dir := t.TempDir()
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
