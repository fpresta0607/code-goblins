package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// makeJunction links link at target through cmd's mklink /J, the same call
// provisioning uses.
func makeJunction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, out)
	}
}

func TestRunnerGitReturnRemovesEveryLinkAsALink(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	worktreePath := filepath.Join(root, "worktree")
	shared := filepath.Join(project, "node_modules")
	outside := filepath.Join(root, "elsewhere")
	for _, dir := range []string{shared, worktreePath, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(shared, "package.txt")
	outsideMarker := filepath.Join(outside, "package.txt")
	for _, path := range []string{marker, outsideMarker} {
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	makeJunction(t, filepath.Join(worktreePath, "node_modules"), shared)
	makeJunction(t, filepath.Join(worktreePath, "elsewhere"), outside)

	runner := &scriptedRunner{results: []scriptedResult{{}, {}, {}}}
	if err := (RunnerGit{Commands: runner}).Return(context.Background(), project, worktreePath); err != nil {
		t.Fatalf("Return: %v", err)
	}
	if _, err := os.Lstat(worktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the worktree survived Return: %v", err)
	}
	for _, path := range []string{marker, outsideMarker} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("Return deleted %s through a junction: %v", path, err)
		}
	}
}

func TestRunnerGitReturnRefusedForUncommittedWorkLeavesJunctionsInPlace(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	worktreePath := filepath.Join(root, "worktree")
	shared := filepath.Join(project, "node_modules")
	for _, dir := range []string{shared, worktreePath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(worktreePath, "node_modules")
	makeJunction(t, link, shared)
	if err := os.WriteFile(filepath.Join(shared, "package.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{results: []scriptedResult{
		{result: execx.Result{Stdout: []byte(" M main.go\n")}},
	}}

	err := (RunnerGit{Commands: runner}).Return(context.Background(), project, worktreePath)
	if err == nil || !strings.Contains(err.Error(), "uncommitted work") {
		t.Fatalf("Return error = %v, want an uncommitted-work refusal", err)
	}
	// The refusal tells the operator to commit their work. That is only
	// actionable in a worktree that still builds and tests, so the junction
	// carrying its dependencies has to survive a Return that changed nothing.
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("refused Return destroyed the worktree's junction: %v", err)
	}
	entries, err := os.ReadDir(link)
	if err != nil || len(entries) != 1 {
		t.Fatalf("junction no longer resolves to the shared directory: %v %v", entries, err)
	}
}
