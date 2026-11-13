package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A host command walks the worktree for a .git the container made unless a
// walk since the walk token last changed found it clean: a .git made after
// such a walk is not seen, which the container's end walk and the path
// boundary are for, and one made after the token changed is.
func TestNestedGitCheckWalksOnlyWhenNoWalkFoundTheWorktreeClean(t *testing.T) {
	const plant = "node_modules/pkg/.git"
	for _, test := range []struct {
		name    string
		arrange func(t *testing.T, taskTmp, worktree string)
		want    string
	}{
		{name: "no walk yet", want: plant},
		{name: "after a clean walk", arrange: func(t *testing.T, taskTmp, worktree string) {
			walkClean(t, taskTmp, worktree)
		}},
		{name: "after a clean walk was forgotten", want: plant, arrange: func(t *testing.T, taskTmp, worktree string) {
			walkClean(t, taskTmp, worktree)
			if err := ForgetNestedGitWalk(taskTmp); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "after a walk that found one", want: plant, arrange: func(t *testing.T, taskTmp, worktree string) {
			makeRepository(t, worktree, plant)
			if err := WalkForNestedGit(taskTmp, worktree); err == nil {
				t.Fatal("WalkForNestedGit found no .git the worktree holds")
			}
		}},
		{name: "after a clean walk's mark was lost", want: plant, arrange: func(t *testing.T, taskTmp, worktree string) {
			walkClean(t, taskTmp, worktree)
			if err := os.Remove(filepath.Join(taskTmp, cleanMarkFile)); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			worktree, _ := worktreeWithLink(t)
			taskTmp := filepath.Join(t.TempDir(), "tasktmp", "task")
			if test.arrange != nil {
				test.arrange(t, taskTmp, worktree)
			}
			makeRepository(t, worktree, plant)

			// Act
			err := NestedGitCheck(taskTmp, worktree)

			// Assert
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), filepath.FromSlash(test.want))) {
				t.Fatalf("NestedGitCheck = %v, want %q", err, test.want)
			}
		})
	}
}

// A walk that finds the worktree clean marks it so, and the check after it
// walks no more, until a .git it finds is removed and a walk finds it clean.
func TestNestedGitCheckMarksTheWorktreeCleanOnceItHoldsNoRepository(t *testing.T) {
	// Arrange
	worktree, _ := worktreeWithLink(t)
	taskTmp := filepath.Join(t.TempDir(), "tasktmp", "task")
	makeRepository(t, worktree, "vendor/x/.git")
	if err := NestedGitCheck(taskTmp, worktree); err == nil {
		t.Fatal("NestedGitCheck found no .git the worktree holds")
	}
	if err := os.RemoveAll(filepath.Join(worktree, "vendor", "x", ".git")); err != nil {
		t.Fatal(err)
	}

	// Act
	err := NestedGitCheck(taskTmp, worktree)

	// Assert
	if err != nil {
		t.Fatalf("NestedGitCheck = %v, want the clean worktree let be", err)
	}
	token, _ := os.ReadFile(filepath.Join(taskTmp, walkTokenFile))
	mark, _ := os.ReadFile(filepath.Join(taskTmp, cleanMarkFile))
	if len(token) == 0 || string(mark) != string(token) {
		t.Fatalf("walk token %q and clean mark %q, want the mark to hold the token", token, mark)
	}
}

// A task's record that names no tasktmp folder leaves the walk nowhere to be
// recorded, so it is refused rather than trusted.
func TestNestedGitCheckRefusesATaskWithNoTaskTmpFolder(t *testing.T) {
	// Arrange
	worktree, _ := worktreeWithLink(t)

	// Act
	err := NestedGitCheck("", worktree)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "names no tasktmp folder") {
		t.Fatalf("NestedGitCheck = %v, want it refused for its missing tasktmp folder", err)
	}
}

func walkClean(t *testing.T, taskTmp, worktree string) {
	t.Helper()
	if err := WalkForNestedGit(taskTmp, worktree); err != nil {
		t.Fatalf("WalkForNestedGit of a clean worktree = %v", err)
	}
}

func makeRepository(t *testing.T, worktree, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(worktree, filepath.FromSlash(path)), 0o755); err != nil {
		t.Fatal(err)
	}
}
