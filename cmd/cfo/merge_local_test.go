package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestMergeLocalWithoutOriginUsesPrimaryMain(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	stateDir := filepath.Join(root, "state")
	for _, dir := range []string{project, stateDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CFO_HOME", root)
	t.Setenv("CFO_STATE_OVERRIDE", stateDir)
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git(project, "init", "-b", "main")
	git(project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "Initial")
	wt := filepath.Join(root, "worktree")
	git(project, "worktree", "add", "-b", "work", wt, "HEAD")
	if err := os.WriteFile(filepath.Join(wt, "landed.txt"), []byte("landed locally"), 0600); err != nil {
		t.Fatal(err)
	}
	git(wt, "add", "landed.txt")
	git(wt, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "Local work")
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "local", Project: project, Worktree: wt, Mode: "local-only"}); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := runMergeLocal([]string{"local"}, &out, &errs); code != 0 {
		t.Fatalf("code=%d err=%s", code, &errs)
	}
	if head := git(project, "rev-parse", "HEAD"); head != git(wt, "rev-parse", "HEAD") {
		t.Fatal("primary content was not landed")
	}
	if remotes := git(project, "remote"); remotes != "" {
		t.Fatalf("invented remote: %s", remotes)
	}
}

func TestMergeLocalWithoutOriginRefusesNonPrimaryBranchWithoutMutation(t *testing.T) {
	for _, branch := range []string{"feature", "HEAD"} {
		t.Run(branch, func(t *testing.T) {
			root := t.TempDir()
			project := filepath.Join(root, "project")
			stateDir := filepath.Join(root, "state")
			if err := os.MkdirAll(stateDir, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CFO_HOME", root)
			t.Setenv("CFO_STATE_OVERRIDE", stateDir)
			git := func(dir string, args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %s %v", args, out, err)
				}
				return strings.TrimSpace(string(out))
			}
			git(root, "init", "-b", "main", project)
			git(project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "Initial")
			wt := filepath.Join(root, "worktree")
			git(project, "worktree", "add", "-b", "work", wt, "HEAD")
			if err := os.WriteFile(filepath.Join(wt, "landed.txt"), []byte("must not land here"), 0600); err != nil {
				t.Fatal(err)
			}
			git(wt, "add", "landed.txt")
			git(wt, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "Local work")
			if branch == "feature" {
				git(project, "checkout", "-b", branch)
			} else {
				git(project, "checkout", "--detach")
			}
			before := git(project, "rev-parse", "HEAD")
			if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "local", Project: project, Worktree: wt, Mode: "local-only"}); err != nil {
				t.Fatal(err)
			}
			var out, errs bytes.Buffer
			if code := runMergeLocal([]string{"local"}, &out, &errs); code == 0 || !strings.Contains(errs.String(), "main or master") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errs.String())
			}
			if after := git(project, "rev-parse", "HEAD"); after != before {
				t.Fatalf("primary HEAD mutated from %s to %s", before, after)
			}
		})
	}
}
