package supervisor

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/readcache"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// gateStandIn is sqlite3 for the gate's database, which answers every query
// with no runs, as it does for a branch no gate ran, and counts the queries;
// git runs as it does on the machine.
type gateStandIn struct{ queries int }

func (g *gateStandIn) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	if req.Name == "sqlite3" {
		g.queries++
		return execx.Result{Stdout: []byte("[]")}, nil
	}
	return execx.OSRunner{}.Run(ctx, req)
}

// boardView is what one board refresh read of a goblin's worktree.
type boardView struct {
	evaluated, progress, branch, overlapBranch string
	changed, worktrees                         []string
}

// refreshBoard reads a goblin's worktree as one board refresh does: its
// evaluation, its progress, its branches for the CI wakes, the files its
// branch changes for the overlap wakes, its origin and its project's
// worktrees.
func refreshBoard(t *testing.T, s *Service, meta state.TaskMeta) boardView {
	t.Helper()
	ctx := context.Background()
	var view boardView
	evaluation, err := s.execute(ctx, Action{Kind: "evaluate", TaskID: meta.ID, Generation: meta.SpawnGen})
	if err != nil {
		t.Fatal(err)
	}
	view.evaluated = evaluation.Head
	if view.progress, _, _, err = s.observeWork(ctx, meta, ""); err != nil {
		t.Fatal(err)
	}
	goblins := ciGoblins(ctx, s.Options.CI, s.Store.Home.State)
	if len(goblins) != 1 {
		t.Fatalf("the CI wakes read %d goblins, want 1", len(goblins))
	}
	view.branch = goblins[0].branch
	if view.changed, view.overlapBranch, err = branchPaths(ctx, s.Options.CI, goblins[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Options.CI.Run(ctx, execx.Request{Dir: meta.Project, Name: "git", Args: []string{"config", "--get", "remote.origin.url"}}); err != nil {
		t.Fatal(err)
	}
	view.worktrees = projectWorktrees(ctx, s.Options.CI, meta.Project)
	return view
}

// gitStarts counts the git processes this test process started.
func gitStarts() int {
	starts := 0
	for _, start := range execx.Starts() {
		if start.Program == "git" {
			starts += start.Count
		}
	}
	return starts
}

// ageFiles moves every file under root an hour into the past, as a goblin's
// worktree looks once its work settled.
func ageFiles(t *testing.T, root string) {
	t.Helper()
	past := time.Now().Add(-time.Hour)
	if err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, past, past)
	}); err != nil {
		t.Fatal(err)
	}
}

func boardGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// readingBoard is a board over task-1, whose worktree is a repository with
// main two commits on and origin's main one behind it, reading git and its
// gate's database through one read cache as cfo serve wires it.
func readingBoard(t *testing.T) (*Service, state.TaskMeta, *gateStandIn) {
	t.Helper()
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	gitFixture(t, meta.Worktree)
	boardGit(t, meta.Worktree, "update-ref", "refs/remotes/origin/main", "HEAD~1")
	gate := filepath.Join(t.TempDir(), "no-mistakes")
	if err := os.MkdirAll(gate, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gate, "state.sqlite"), []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	ageFiles(t, meta.Worktree)
	ageFiles(t, gate)
	sqlite := &gateStandIn{}
	reads := readcache.New()
	cached := readcache.Runner{Cache: reads, Next: sqlite}
	s := &Service{Store: store, Options: Options{CI: cached, Progress: cached, Reads: reads, Gate: pipeline.Reader{Root: gate, Commands: cached}}, Git: Git{Reads: reads}, work: make(chan struct{}, 1), subscribers: map[chan struct{}]struct{}{}}
	return s, meta, sqlite
}

// A board refresh reads every goblin's head, branches, refs, reflog,
// worktrees and status, and its gate run, every minute. Once it has read
// them, a refresh over a repository and a gate database nobody changed
// starts no git and no sqlite3: on 2026-10-19 the board started git about
// 5,000 times an hour and sqlite3 326 times to read the same answers.
func TestABoardRefreshOverUnchangedWorkStartsNoGitAndNoSqlite(t *testing.T) {
	// Arrange
	s, meta, sqlite := readingBoard(t)
	// A status knows what git ignores from its first answer.
	first := refreshBoard(t, s, meta)
	refreshBoard(t, s, meta)
	gitBefore, queriesBefore := gitStarts(), sqlite.queries

	// Act
	again := refreshBoard(t, s, meta)

	// Assert
	if started := gitStarts() - gitBefore; started != 0 {
		t.Errorf("a refresh over unchanged work started git %d times, want none", started)
	}
	if queried := sqlite.queries - queriesBefore; queried != 0 {
		t.Errorf("a refresh over an unchanged gate database ran sqlite3 %d times, want none", queried)
	}
	if again.progress != first.progress || again.evaluated != first.evaluated || again.branch != first.branch || !slices.Equal(again.changed, first.changed) || !slices.Equal(again.worktrees, first.worktrees) {
		t.Errorf("the refresh read %+v, the first read %+v", again, first)
	}
}

// A goblin's commit, a branch it checks out, a ref a fetch moves, a worktree
// it adds and a write to the gate's database are each seen on the next
// refresh.
func TestABoardRefreshSeesEachChangeOnTheNextRefresh(t *testing.T) {
	tests := []struct {
		name   string
		change func(t *testing.T, dir, gate string)
		isSeen func(before, after boardView, dir string, queried int) bool
	}{
		{"head", func(t *testing.T, dir, _ string) { boardGit(t, dir, "commit", "-q", "--allow-empty", "-m", "third") },
			func(before, after boardView, dir string, _ int) bool {
				head := boardGitHead(dir)
				return after.progress == head && after.evaluated == head && before.progress != head
			}},
		{"branch", func(t *testing.T, dir, _ string) { boardGit(t, dir, "checkout", "-q", "-b", "fix/other") },
			func(_, after boardView, _ string, _ int) bool {
				return after.branch == "fix/other" && after.overlapBranch == "fix/other"
			}},
		{"ref", func(t *testing.T, dir, _ string) { boardGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD") },
			func(before, after boardView, _ string, _ int) bool {
				return len(before.changed) > 0 && len(after.changed) == 0
			}},
		{"worktree", func(t *testing.T, dir, _ string) {
			boardGit(t, dir, "worktree", "add", "-q", "-b", "fix/other", filepath.Join(filepath.Dir(dir), "other"))
		}, func(before, after boardView, _ string, _ int) bool {
			return len(after.worktrees) == len(before.worktrees)+1
		}},
		{"gate run", func(t *testing.T, _, gate string) {
			if err := os.WriteFile(filepath.Join(gate, "state.sqlite"), []byte("SQLite format 3\x00 and a run"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, func(_, _ boardView, _ string, queried int) bool { return queried > 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s, meta, sqlite := readingBoard(t)
			refreshBoard(t, s, meta)
			before := refreshBoard(t, s, meta)
			queriesBefore := sqlite.queries
			test.change(t, meta.Worktree, s.Options.Gate.(pipeline.Reader).Root)

			// Act
			after := refreshBoard(t, s, meta)

			// Assert
			if !test.isSeen(before, after, meta.Worktree, sqlite.queries-queriesBefore) {
				t.Errorf("the refresh after the change read %+v, before it %+v", after, before)
			}
		})
	}
}

func boardGitHead(dir string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}
