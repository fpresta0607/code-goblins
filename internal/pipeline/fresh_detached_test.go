package pipeline

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Typing into a new task's goblin before it makes a branch is proved, never
// assumed: only a worktree whose HEAD has not moved since git made it, and
// that no gate run works in, takes it. Every other answer refuses, and only a
// gate run working there reads as the gate's hold (#1987). HEAD's history is
// found from the worktree's .git entry, so no check asks git where it is: the
// board's custody ticks run it every few seconds, and a refresh over work
// nobody changed starts no git.
func TestCanTypeFreshDetachedAllowsOnlyAnUntouchedWorktreeNoRunWorksIn(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}
	const (
		allowed = iota
		held
		refused
	)
	for _, test := range []struct {
		name string
		want int
	}{
		{"fresh", allowed},
		{"a clone at a tag, with its own .git folder", allowed},
		{"a .git file that names its git folder relatively", allowed},
		{"a .git file that names no git folder", refused},
		{"a run records it", held},
		{"a run records it in another spelling", held},
		{"a run records a link to it", held},
		{"it is one of the gate's own worktrees", held},
		{"a run records a worktree elsewhere", allowed},
		{"a run records a relative worktree", refused},
		{"on a branch", refused},
		{"detached again after a branch", refused},
		{"a commit on the detached HEAD", refused},
		{"no HEAD history", refused},
		{"an empty HEAD history", refused},
		{"a record that is not whole", refused},
		{"a record from another commit", refused},
		{"no gate records", refused},
		{"unreadable gate records", refused},
		{"HEAD moves while the records are read", refused},
		{"a branch is made while the records are read", refused},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			project, root := t.TempDir(), t.TempDir()
			worktree := filepath.Join(t.TempDir(), "task")
			if test.name == "it is one of the gate's own worktrees" {
				worktree = filepath.Join(root, "worktrees", "repo-one", "run-one")
			}
			git := func(dir string, args ...string) {
				t.Helper()
				command := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@example.invalid"}, args...)...)
				if out, err := command.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			git(project, "init", "-q", "-b", "main")
			git(project, "commit", "-q", "--allow-empty", "-m", "root")
			if test.name == "a clone at a tag, with its own .git folder" {
				git(project, "tag", "v1")
				git(project, "-c", "advice.detachedHead=false", "clone", "-q", "--branch", "v1", project, worktree)
			} else {
				git(project, "worktree", "add", "-q", "--detach", worktree, "HEAD")
			}
			switch test.name {
			case "on a branch":
				git(worktree, "switch", "-q", "--create", "feat/work")
			case "detached again after a branch":
				git(worktree, "switch", "-q", "--create", "feat/work")
				git(worktree, "switch", "-q", "--detach")
			case "a commit on the detached HEAD":
				git(worktree, "commit", "-q", "--allow-empty", "-m", "work")
			}
			out, err := exec.Command("git", "-C", worktree, "rev-parse", "--path-format=absolute", "--git-path", "logs/HEAD").Output()
			if err != nil {
				t.Fatal(err)
			}
			history := strings.TrimSpace(string(out))
			record, err := os.ReadFile(history)
			if err != nil {
				t.Fatal(err)
			}
			rewrite := func(text string) {
				t.Helper()
				if err := os.WriteFile(history, []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			switch test.name {
			case "no HEAD history":
				if err := os.Remove(history); err != nil {
					t.Fatal(err)
				}
			case "an empty HEAD history":
				rewrite("")
			case "a record that is not whole":
				rewrite(strings.TrimSuffix(string(record), "\n"))
			case "a record from another commit":
				fields := strings.Fields(string(record))
				rewrite(strings.Replace(string(record), fields[1], strings.Repeat("1", len(fields[1])), 1))
			case "a .git file that names its git folder relatively":
				from, err := filepath.EvalSymlinks(worktree)
				if err != nil {
					t.Fatal(err)
				}
				gitDir, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(history)))
				if err != nil {
					t.Fatal(err)
				}
				relative, err := filepath.Rel(from, gitDir)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+filepath.ToSlash(relative)+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "a .git file that names no git folder":
				if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("not a link\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			recorded := ""
			switch test.name {
			case "a run records it":
				recorded = worktree
			case "a run records it in another spelling":
				recorded = filepath.ToSlash(worktree) + "/"
				if runtime.GOOS == "windows" {
					recorded = strings.ToUpper(recorded)
				}
			case "a run records a link to it":
				recorded = filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(worktree, recorded); err != nil {
					// Windows makes a link to a folder only for an account
					// allowed to; a junction needs no such right.
					if out, err := exec.Command("cmd", "/c", "mklink", "/J", recorded, worktree).CombinedOutput(); err != nil {
						t.Skipf("no link to the worktree can be made here: %v %s", err, out)
					}
				}
			case "a run records a worktree elsewhere":
				recorded = t.TempDir()
			case "a run records a relative worktree":
				recorded = filepath.Join("relative", "task")
			}
			database := filepath.Join(root, "state.sqlite")
			runs := "CREATE TABLE runs(id TEXT, repo_id TEXT, worktree_dir TEXT); INSERT INTO runs VALUES('run-zero','repo-one',NULL);"
			if recorded != "" {
				runs += "INSERT INTO runs VALUES('run-one','repo-one'," + sqlString(recorded) + ");"
			}
			switch test.name {
			case "unreadable gate records":
				if err := os.WriteFile(database, []byte("not a database"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "no gate records":
			default:
				if out, err := exec.Command("sqlite3", database, runs).CombinedOutput(); err != nil {
					t.Fatalf("disposable gate records: %v\n%s", err, out)
				}
			}
			isHistoryAsked := false
			runner := runnerFunc(func(ctx context.Context, request execx.Request) (execx.Result, error) {
				if request.Name == "git" && slices.Contains(request.Args, "--git-path") {
					isHistoryAsked = true
				}
				result, err := (execx.OSRunner{}).Run(ctx, request)
				if request.Name == "sqlite3" {
					switch test.name {
					case "HEAD moves while the records are read":
						rewrite(string(record) + string(record))
					case "a branch is made while the records are read":
						git(worktree, "switch", "-q", "--create", "feat/moved")
					}
				}
				return result, err
			})

			// Act
			err = (Reader{Root: root, Commands: runner}).CanTypeFreshDetached(t.Context(), worktree)

			// Assert
			if isHistoryAsked {
				t.Error("CanTypeFreshDetached asked git where HEAD's history is")
			}
			switch {
			case test.want == allowed && err != nil:
				t.Fatalf("CanTypeFreshDetached = %v, want typing allowed", err)
			case test.want == held && !errors.Is(err, ErrCustodyHeld):
				t.Fatalf("CanTypeFreshDetached = %v, want the gate's hold", err)
			case test.want == refused && (err == nil || errors.Is(err, ErrCustodyHeld)):
				t.Fatalf("CanTypeFreshDetached = %v, want a refusal that is not the gate's hold", err)
			}
		})
	}
}
