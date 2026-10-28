package reap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// A worktree's last change is what a session working in it leaves between
// commits: a file git sees, tracked or untracked. An ignored file is a build's
// output, written by a process the working-directory check finds, and is not
// read.
func TestAWorktreeIsInUseWhileAFileGitSeesChangedWithinTheHour(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		isInUse bool
	}{
		{name: "a tracked file changed ten minutes ago", file: "README.md", isInUse: true},
		{name: "an untracked file changed ten minutes ago", file: "draft.txt", isInUse: true},
		{name: "an ignored file changed ten minutes ago", file: filepath.Join("node_modules", "cache.bin"), isInUse: false},
		{name: "nothing changed within the hour", isInUse: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			checkout := filepath.Join(t.TempDir(), "repo")
			gitInit(t, checkout)
			worktree := filepath.Join(checkout, ".claude", "worktrees", "session")
			git(t, checkout, "worktree", "add", "--detach", worktree)
			if err := os.WriteFile(filepath.Join(worktree, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(worktree, "node_modules"), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"draft.txt", filepath.Join("node_modules", "cache.bin")} {
				if err := os.WriteFile(filepath.Join(worktree, name), []byte("work"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			age(t, worktree, time.Now().Add(-2*time.Hour))
			age(t, git(t, worktree, "rev-parse", "--absolute-git-dir"), time.Now().Add(-2*time.Hour))
			if test.file != "" {
				tenMinutesAgo := time.Now().Add(-10 * time.Minute)
				if err := os.Chtimes(filepath.Join(worktree, test.file), tenMinutesAgo, tenMinutesAgo); err != nil {
					t.Fatal(err)
				}
			}
			collector := Collector{Commands: execx.OSRunner{}, Processes: stubProcesses{}, WorkingDirectory: func(int) (string, error) { return "", os.ErrNotExist }}
			inv := Inventory{Worktrees: []WorktreeDir{{Path: worktree, Project: checkout, Source: SourceCheckout, Registration: RegistrationListed}}}

			// Act
			collector.markWorktreesInUse(context.Background(), &inv)

			// Assert
			inUse := inv.Worktrees[0].InUse
			if test.isInUse != (inUse != "") {
				t.Fatalf("in use = %q, want in use %t", inUse, test.isInUse)
			}
			if test.isInUse && !strings.Contains(inUse, "changed at") {
				t.Errorf("in use = %q, want it naming when it changed", inUse)
			}
		})
	}
}

// What the sweep could not read is no evidence that nothing works in a
// worktree, so it keeps it rather than reading the silence as an orphan.
func TestAWorktreeIsInUseWhenTheProcessesWereNotRead(t *testing.T) {
	// Arrange
	inv := Inventory{Worktrees: []WorktreeDir{{Path: t.TempDir(), Project: t.TempDir(), Source: SourceCheckout, Registration: RegistrationListed}}}

	// Act
	Collector{Commands: execx.OSRunner{}}.markWorktreesInUse(context.Background(), &inv)

	// Assert
	if inUse := inv.Worktrees[0].InUse; !strings.Contains(inUse, "unknown") {
		t.Fatalf("in use = %q, want it held as unknown", inUse)
	}
}

// The orphan sweep holds a worktree a live session uses for the janitor's own
// reason, and no --force clears it, so the CFO is never told to remove it.
func TestTheOrphanSweepHoldsAWorktreeInUseForItsReason(t *testing.T) {
	// Arrange
	checkout := filepath.Join(t.TempDir(), "repo")
	reason := "pid 4242 node.exe works in " + checkout + ", so a live session may still be using it"
	worktree := WorktreeDir{Path: filepath.Join(checkout, ".claude", "worktrees", "session"), Project: checkout, Source: SourceCheckout, Registration: RegistrationListed, InUse: reason}

	// Act
	findings := Classify(Inventory{Worktrees: []WorktreeDir{worktree}})

	// Assert
	if len(findings) != 1 || findings[0].Class != OrphanWorktree {
		t.Fatalf("findings = %+v, want the worktree reported once", findings)
	}
	if hold := findings[0].Hold(); hold != reason+". No --force clears this" {
		t.Errorf("hold = %q, want the janitor's reason, which no --force clears", hold)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func age(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := filepath.Walk(path, func(file string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(file, when, when)
	}); err != nil {
		t.Fatal(err)
	}
}
