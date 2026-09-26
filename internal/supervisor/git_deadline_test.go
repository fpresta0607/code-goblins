package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain runs this test binary as a git that never answers when it is
// started under that name, the stand-in for a git slowed past its deadline.
func TestMain(m *testing.M) {
	if name := filepath.Base(os.Args[0]); strings.EqualFold(strings.TrimSuffix(name, filepath.Ext(name)), "git") {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Under full-suite load a git read can outlast its deadline, and on Windows a
// git killed at its deadline exits with code 1, the code symbolic-ref -q
// answers "not a symbolic ref" with, so TestPreviewRefusesLinksOutOfTheWorktree
// failed saying the project had no origin/HEAD, which its fixture has. A
// deadline, whether it passed before git started or while it ran, is reported
// as the deadline; only symbolic-ref's own answer means there is no base.
func TestATaskDiffBaseCutOffByItsDeadlineSaysSo(t *testing.T) {
	dir := gitFixture(t)
	before, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	_, beforeErr := (Git{}).base(before, dir)

	if !errors.Is(beforeErr, context.DeadlineExceeded) || strings.Contains(fmt.Sprint(beforeErr), "establish origin/HEAD") {
		t.Errorf("base past its deadline = %v, want the deadline reported", beforeErr)
	}

	t.Run("killed at its deadline", func(t *testing.T) {
		program, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		bin := t.TempDir()
		copyExecutable(t, program, filepath.Join(bin, "git.exe"))
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if found, err := exec.LookPath("git"); err != nil || !strings.EqualFold(found, filepath.Join(bin, "git.exe")) {
			t.Fatalf("git resolves to %q, %v; want the stand-in", found, err)
		}
		during, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		_, err = (Git{}).base(during, dir)

		if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(fmt.Sprint(err), "establish origin/HEAD") {
			t.Errorf("base whose git was killed at its deadline = %v, want the deadline reported", err)
		}
	})

	remove := exec.Command("git", "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
	remove.Dir = dir
	if out, err := remove.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if _, err := (Git{}).base(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "establish origin/HEAD") {
		t.Errorf("base with no origin/HEAD = %v, want it named", err)
	}
}

// A retained base is checked with merge-base --is-ancestor, whose exit code 1
// means "not an ancestor" and is also what a git killed at its deadline exits
// with on Windows, so the check cut off by its deadline reports the deadline
// and only merge-base's own answer says the commit is not in the history.
func TestARetainedBaseCutOffByItsDeadlineSaysSo(t *testing.T) {
	dir := gitFixture(t)
	retained := gitOutput(t, dir, "rev-parse", "HEAD~1")
	before, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	_, beforeErr := (Git{Base: retained}).base(before, dir)

	if !errors.Is(beforeErr, context.DeadlineExceeded) || strings.Contains(fmt.Sprint(beforeErr), "not in this task's history") {
		t.Errorf("retained base past its deadline = %v, want the deadline reported", beforeErr)
	}

	t.Run("killed at its deadline", func(t *testing.T) {
		program, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		bin := t.TempDir()
		copyExecutable(t, program, filepath.Join(bin, "git.exe"))
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if found, err := exec.LookPath("git"); err != nil || !strings.EqualFold(found, filepath.Join(bin, "git.exe")) {
			t.Fatalf("git resolves to %q, %v; want the stand-in", found, err)
		}
		during, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		_, err = (Git{Base: retained}).base(during, dir)

		if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(fmt.Sprint(err), "not in this task's history") {
			t.Errorf("retained base whose git was killed at its deadline = %v, want the deadline reported", err)
		}
	})

	unrelated := gitOutput(t, dir, "commit-tree", "HEAD^{tree}", "-m", "Unrelated root")
	if _, err := (Git{Base: unrelated}).base(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "not in this task's history") {
		t.Errorf("retained base that is not an ancestor = %v, want it named", err)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func copyExecutable(t *testing.T, from, to string) {
	t.Helper()
	source, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
}
