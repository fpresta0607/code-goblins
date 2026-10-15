package cleanup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// makeJunction links link at target through cmd's mklink /J, as pnpm and
// provisioning make their links.
func makeJunction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ignore adds patterns to the project's info/exclude, which every worktree
// of it shares, as provisioning registers node_modules.
func ignore(t *testing.T, project string, patterns ...string) {
	t.Helper()
	exclude := filepath.Join(project, ".git", "info", "exclude")
	file, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(patterns, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

// layOutAsPnpm gives worktree a node_modules as a pnpm install lays one out
// on Windows: packages under .pnpm linked to each other by junctions, a file
// whose path passes 260 characters and a read-only file. It returns the
// longest path it made.
func layOutAsPnpm(t *testing.T, worktree string) string {
	t.Helper()
	modules := filepath.Join(worktree, "node_modules")
	pnpm := filepath.Join(modules, ".pnpm")
	packageDir := func(name string) string {
		return filepath.Join(pnpm, name+"@1.0.0", "node_modules", name)
	}
	for _, name := range []string{"a", "b"} {
		writeFile(t, filepath.Join(packageDir(name), "index.js"), "module.exports = '"+name+"'")
	}
	// b depends on a, and a@1.0.0 is removed first: git removed it through
	// b's junction too, and then could not remove the junction it left
	// dangling, which is how cleanup failed with "Directory not empty".
	makeJunction(t, filepath.Join(pnpm, "b@1.0.0", "node_modules", "a"), packageDir("a"))
	makeJunction(t, filepath.Join(pnpm, "a@1.0.0", "node_modules", "b"), packageDir("b"))
	makeJunction(t, filepath.Join(modules, "a"), packageDir("a"))
	makeJunction(t, filepath.Join(modules, "b"), packageDir("b"))
	readOnly := filepath.Join(packageDir("a"), "package.json")
	writeFile(t, readOnly, "{}")
	if err := os.Chmod(readOnly, 0o444); err != nil {
		t.Fatal(err)
	}
	long := filepath.Join(packageDir("c"), strings.Repeat("segment-of-a-deep-package-path"+string(filepath.Separator), 8), "index.js")
	writeFile(t, long, "deep")
	return long
}

func TestCleanupRemovesAWorktreeAsPnpmLaysItOutAndNothingALinkLeadsTo(t *testing.T) {
	// Arrange: the worktree holds pnpm's node_modules and a junction to a
	// store outside it, which every project on the machine shares.
	task := newHomeTask(t)
	ignore(t, task.project, "node_modules", "linked")
	long := layOutAsPnpm(t, task.worktree)
	if len(long) <= 260 {
		t.Fatalf("the long path is %d characters, want past 260", len(long))
	}
	store := filepath.Join(t.TempDir(), "store", "package")
	stored := filepath.Join(store, "index.js")
	writeFile(t, stored, "a file of the shared store")
	makeJunction(t, filepath.Join(task.worktree, "linked"), store)

	// Act
	_, err := task.service.Cleanup(context.Background(), "g1")

	// Assert
	if err != nil {
		t.Errorf("Cleanup: %v", err)
	}
	if exists(t, task.worktree) {
		t.Errorf("the worktree %s survived the cleanup", task.worktree)
	}
	if listed := realGit(t, task.project, "worktree", "list"); strings.Count(listed, "\n") != 0 {
		t.Errorf("the project still registers worktrees:\n%s", listed)
	}
	if content, err := os.ReadFile(stored); err != nil || string(content) != "a file of the shared store" {
		t.Errorf("the store's file behind the junction is %q, %v after the cleanup, want it untouched", content, err)
	}
}

// #915, 2026-10-14: a removal that a program's hold stops used to fail the
// cleanup and leave the task working until that program ended. The task
// retires, and the worktree, whole again, is left for the janitor, whose
// return of it finishes the removal once the program ends.
func TestCleanupThatCannotRemoveAFileRetiresTheTaskAndLeavesAWorktreeTheJanitorFinishes(t *testing.T) {
	// Arrange: a program still holds a build output open without sharing its
	// deletion, as Windows holds a running program's file. It sorts after
	// README.md, so a tracked file is gone before the removal stops.
	task := newHomeTask(t)
	ignore(t, task.project, "node_modules", "target")
	layOutAsPnpm(t, task.worktree)
	held := filepath.Join(task.worktree, "target", "debug", "app.exe")
	writeFile(t, held, "a running program")
	holder, err := os.Open(held)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()

	// Act
	result, err := task.service.Cleanup(context.Background(), "g1")

	// Assert: the task retired, the removal stopped, and the worktree is one
	// git still knows, whole again.
	if err != nil {
		t.Fatalf("Cleanup = %v; want the task retired and the held worktree left for the janitor", err)
	}
	if !strings.Contains(result.Output, "left the worktree "+task.worktree+" for the janitor") {
		t.Errorf("output = %q; want it to say the worktree is left for the janitor", result.Output)
	}
	if _, err := state.ReadTaskMeta(task.stateDir, "g1"); err == nil {
		t.Error("task metadata still present; want the task retired")
	}
	if !exists(t, filepath.Join(task.worktree, ".git")) {
		t.Fatal("the worktree's .git link is gone after a removal that stopped")
	}
	// The extras went first, so the project and this worktree are what git
	// lists.
	if listed := realGit(t, task.project, "worktree", "list"); strings.Count(listed, "\n") != 1 {
		t.Errorf("git does not list the project and the worktree after a removal that stopped:\n%s", listed)
	}
	if status := realGit(t, task.worktree, "status", "--porcelain=v1", "--untracked-files=all"); status != "" {
		t.Errorf("the worktree is not clean after a removal that stopped:\n%s", status)
	}

	// Act: the program ends, and the janitor returns the worktree no task
	// owns, as it returns every one.
	if err := holder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := task.service.Worktrees.Return(context.Background(), task.project, task.worktree); err != nil {
		t.Fatalf("the janitor's return: %v", err)
	}

	// Assert
	if exists(t, task.worktree) {
		t.Errorf("the worktree %s survived the janitor's return", task.worktree)
	}
	if listed := realGit(t, task.project, "worktree", "list"); strings.Count(listed, "\n") != 0 {
		t.Errorf("the project still registers worktrees:\n%s", listed)
	}
}
