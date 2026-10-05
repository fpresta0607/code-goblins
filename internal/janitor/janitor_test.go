package janitor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// sweepFixture is a home and a project cloned from a bare origin, as a
// project the fleet works in is.
type sweepFixture struct {
	home    home.Home
	project string
	now     time.Time
}

func newSweepFixture(t *testing.T) *sweepFixture {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitIn(t, root, "init", "-q", "--bare", "--initial-branch=main", origin)
	seed := filepath.Join(root, "seed")
	gitIn(t, root, "clone", "-q", origin, seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, seed, "add", ".")
	gitIn(t, seed, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "seed")
	gitIn(t, seed, "push", "-q", "origin", "HEAD:main")
	project := filepath.Join(root, "app")
	gitIn(t, root, "clone", "-q", origin, project)
	gitIn(t, project, "config", "user.email", "t@t")
	gitIn(t, project, "config", "user.name", "t")
	gitIn(t, project, "remote", "set-head", "origin", "--auto")
	homeRoot := filepath.Join(root, "home")
	h := home.Home{Root: homeRoot, State: filepath.Join(homeRoot, "state"), Data: filepath.Join(homeRoot, "data")}
	for _, dir := range []string{h.State, h.Worktrees(), h.Scratch(), h.Bin(), h.Caches()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &sweepFixture{home: h, project: project, now: time.Now()}
}

// worktree makes a worktree of the project under the home, detached at
// origin/main, as a goblin's extra worktree is.
func (f *sweepFixture) worktree(t *testing.T, name string) reap.WorktreeDir {
	t.Helper()
	path := filepath.Join(f.home.Worktrees(), "app", name)
	gitIn(t, f.project, "worktree", "add", "-q", "--detach", path, "origin/main")
	return reap.WorktreeDir{Path: path, Project: f.project, TaskID: name, Registration: reap.RegistrationListed, Created: f.now.Add(-2 * time.Hour)}
}

func (f *sweepFixture) config(inv reap.Inventory) Config {
	return Config{Home: f.home, Commands: execx.OSRunner{}, Settings: fleetconfig.Defaults(), Inventory: inv, Now: f.now, SelfPID: os.Getpid()}
}

func has(items []Item, path string) (Item, bool) {
	for _, item := range items {
		if strings.EqualFold(item.Path, path) {
			return item, true
		}
	}
	return Item{}, false
}

func TestSweepRemovesACleanExtraWorktreeWhoseWorkIsOnTheDefaultBranch(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	merged := f.worktree(t, "gone-task-proof")

	// Act
	record := Sweep(context.Background(), f.config(reap.Inventory{Worktrees: []reap.WorktreeDir{merged}}))

	// Assert
	if _, err := os.Stat(merged.Path); !os.IsNotExist(err) {
		t.Fatalf("the merged worktree survived: %v; kept %+v notes %v", err, record.Kept, record.Notes)
	}
	if item, ok := has(record.Removed, merged.Path); !ok || !strings.Contains(item.Detail, "on the default branch") {
		t.Errorf("removed = %+v, want the worktree named with its work on the default branch", record.Removed)
	}
	if tags := gitIn(t, f.project, "tag", "--list", "archive/*"); tags != "" {
		t.Errorf("work already on main was archived as %s", tags)
	}
	if listed := gitIn(t, f.project, "worktree", "list"); strings.Contains(listed, "gone-task-proof") {
		t.Errorf("the project still registers it:\n%s", listed)
	}
}

func TestSweepTagsThenRemovesACleanExtraWorktreeWithWorkOfItsOwn(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	unmerged := f.worktree(t, "gone-task-ci")
	gitIn(t, unmerged.Path, "commit", "-q", "--allow-empty", "-m", "work of its own")
	head := gitIn(t, unmerged.Path, "rev-parse", "HEAD")

	// Act
	record := Sweep(context.Background(), f.config(reap.Inventory{Worktrees: []reap.WorktreeDir{unmerged}}))

	// Assert
	if _, err := os.Stat(unmerged.Path); !os.IsNotExist(err) {
		t.Fatalf("the unmerged worktree survived: %v; kept %+v", err, record.Kept)
	}
	if got := gitIn(t, f.project, "rev-parse", "refs/tags/archive/gone-task-ci^{commit}"); got != head {
		t.Errorf("archive/gone-task-ci = %s, want the worktree's own commit %s", got, head)
	}
	if item, ok := has(record.Removed, unmerged.Path); !ok || !strings.Contains(item.Detail, "archive/gone-task-ci") {
		t.Errorf("removed = %+v, want the worktree named with its archive tag", record.Removed)
	}
}

func TestSweepLeavesAWorktreeWithUncommittedWorkAndReportsIt(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	dirty := f.worktree(t, "gone-task-draft")
	if err := os.WriteFile(filepath.Join(dirty.Path, "draft.txt"), []byte("unsaved"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	record := Sweep(context.Background(), f.config(reap.Inventory{Worktrees: []reap.WorktreeDir{dirty}}))

	// Assert
	if got, err := os.ReadFile(filepath.Join(dirty.Path, "draft.txt")); err != nil || string(got) != "unsaved" {
		t.Fatalf("the uncommitted work = %q, %v; want it untouched", got, err)
	}
	if item, ok := has(record.Kept, dirty.Path); !ok || !strings.Contains(item.Detail, "uncommitted") {
		t.Errorf("kept = %+v, want the worktree reported for its uncommitted work", record.Kept)
	}
	if _, ok := has(record.Strays, dirty.Path); !ok {
		t.Errorf("strays = %+v, want the unrecorded worktree reported", record.Strays)
	}
}

func TestSweepLeavesEveryWorktreeATaskOwns(t *testing.T) {
	// Arrange: one worktree a task recorded as its extra, one made only
	// minutes ago, one a task's name owns though its record does not list it.
	f := newSweepFixture(t)
	recorded := f.worktree(t, "live-proof")
	fresh := f.worktree(t, "just-made")
	fresh.Created = f.now.Add(-time.Minute)
	named := f.worktree(t, "live-extra")
	own := f.worktree(t, "live")
	meta := state.TaskMeta{ID: "live", Window: "native", Worktree: own.Path, Project: f.project, Harness: "claude", Backend: "native", Extras: []string{recorded.Path}}
	if err := state.WriteTaskMeta(f.home.State, meta); err != nil {
		t.Fatal(err)
	}
	inv := reap.Inventory{Tasks: []reap.Task{{ID: "live", Meta: meta}}, Worktrees: []reap.WorktreeDir{recorded, fresh, named, own}}

	// Act
	record := Sweep(context.Background(), f.config(inv))

	// Assert
	for _, dir := range []reap.WorktreeDir{recorded, fresh, named, own} {
		if _, err := os.Stat(dir.Path); err != nil {
			t.Errorf("%s was removed: %v", dir.Path, err)
		}
	}
	if _, ok := has(record.Strays, named.Path); !ok {
		t.Errorf("strays = %+v, want the extra no record names reported", record.Strays)
	}
	for _, dir := range []reap.WorktreeDir{recorded, own} {
		if _, ok := has(record.Strays, dir.Path); ok {
			t.Errorf("%s is recorded, but was reported a stray", dir.Path)
		}
	}
}

// makeOld sets every modification time under path to two days ago.
func makeOld(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-48 * time.Hour)
	if err := filepath.Walk(path, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, old, old)
	}); err != nil {
		t.Fatal(err)
	}
}

func folder(t *testing.T, path string, old bool) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if old {
		makeOld(t, path)
	}
	return path
}

func selfProcess(t *testing.T) reap.Process {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return reap.Process{PID: os.Getpid(), CommandLine: `"` + self + `" -test.run=Sweep`}
}

func TestSweepRemovesOldFleetTempFoldersNothingUses(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	temp := t.TempDir()
	leak := folder(t, filepath.Join(temp, "go-build123"), true)
	used := folder(t, filepath.Join(temp, "Test4567890"), true)
	prefix := folder(t, filepath.Join(temp, "Test456789"), true)
	recent := folder(t, filepath.Join(temp, "pd-recent"), false)
	foreign := folder(t, filepath.Join(temp, "his-own-folder"), true)
	deadScratch := folder(t, filepath.Join(f.home.Scratch(), "gone-task"), true)
	liveScratch := folder(t, filepath.Join(f.home.Scratch(), "live"), true)
	cfg := f.config(reap.Inventory{
		Tasks:     []reap.Task{{ID: "live", Meta: state.TaskMeta{ID: "live"}}},
		Processes: []reap.Process{selfProcess(t), {PID: 77, CommandLine: `go test -work ` + used + `\b001`}},
	})
	cfg.TempDir = temp

	// Act
	record := Sweep(context.Background(), cfg)

	// Assert
	for _, gone := range []string{leak, prefix, deadScratch} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived: %v; notes %v", gone, err, record.Notes)
		}
	}
	for _, kept := range []string{used, recent, foreign, liveScratch} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
}

// The process list is the only evidence nothing uses a folder, so a list
// that does not show this process's own command line removes nothing.
func TestSweepRemovesNoTempFolderOnAProcessListItCannotProveRead(t *testing.T) {
	f := newSweepFixture(t)
	temp := t.TempDir()
	leak := folder(t, filepath.Join(temp, "go-build123"), true)
	cfg := f.config(reap.Inventory{Processes: []reap.Process{{PID: 77, CommandLine: "unrelated"}}})
	cfg.TempDir = temp

	record := Sweep(context.Background(), cfg)

	if _, err := os.Stat(leak); err != nil {
		t.Errorf("the leak was removed on an unproven process list: %v", err)
	}
	if !strings.Contains(strings.Join(record.Notes, "\n"), "own command line") {
		t.Errorf("notes = %v, want the unproven list named", record.Notes)
	}
}

func TestSweepReportsWhatAppearsInTheProjectsRoot(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	projects := t.TempDir()
	folder(t, filepath.Join(projects, "his-checkout", ".git"), false)
	folder(t, filepath.Join(projects, "his-notes"), false)
	cfg := f.config(reap.Inventory{})
	cfg.ProjectsRoot = projects

	// Act
	first := Sweep(context.Background(), cfg)
	folder(t, filepath.Join(projects, "cg-vm-20261005"), false)
	folder(t, filepath.Join(projects, "new-checkout", ".git"), false)
	if err := os.WriteFile(filepath.Join(projects, "null"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := Sweep(context.Background(), cfg)
	third := Sweep(context.Background(), cfg)

	// Assert
	if _, ok := has(first.Strays, filepath.Join(projects, "his-notes")); !ok || len(first.Strays) != 1 {
		t.Errorf("first sweep strays = %+v, want his-notes reported once", first.Strays)
	}
	for _, record := range []Record{second, third} {
		if len(record.Strays) != 2 {
			t.Errorf("strays = %+v, want the new folder and the new file, and neither the checkouts nor his-notes again", record.Strays)
		}
		for _, name := range []string{"cg-vm-20261005", "null"} {
			if _, ok := has(record.Strays, filepath.Join(projects, name)); !ok {
				t.Errorf("strays = %+v, want %s", record.Strays, name)
			}
		}
	}
	for _, name := range []string{"cg-vm-20261005", "null", "his-notes"} {
		if _, err := os.Stat(filepath.Join(projects, name)); err != nil {
			t.Errorf("the projects root's %s was touched: %v", name, err)
		}
	}
}

// pruneRunner stands in for each cache tool's own prune: it records the
// command and empties the cache the request's last entry names, as the
// janitor appends it last and the real tool reads the last value of a name.
// It empties nothing outside the test's own caches folder: the environment
// it is handed is the test process's own, and in a goblin's pane that names
// the fleet's live caches.
type pruneRunner struct {
	caches string
	ran    []string
}

func (r *pruneRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.ran = append(r.ran, request.Name+" "+strings.Join(request.Args, " "))
	if len(request.Env) == 0 {
		return execx.Result{}, nil
	}
	_, dir, _ := strings.Cut(request.Env[len(request.Env)-1], "=")
	if rel, err := filepath.Rel(r.caches, dir); err != nil || !filepath.IsLocal(rel) {
		return execx.Result{}, fmt.Errorf("the stand-in prune was asked to empty %s, outside the test's caches %s", dir, r.caches)
	}
	for _, entry := range mustReadDir(dir) {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return execx.Result{}, err
		}
	}
	return execx.Result{}, nil
}

func mustReadDir(dir string) []os.DirEntry {
	entries, _ := os.ReadDir(dir)
	return entries
}

func TestSweepTrimsTheCachesWithTheLeastRecentlyUsedToolFirst(t *testing.T) {
	// Arrange: uv last used two days ago, Go's build cache now; each holds
	// a megabyte, and the cap is under two megabytes but over one.
	f := newSweepFixture(t)
	for _, name := range []string{"uv", "go-build"} {
		dir := filepath.Join(f.home.Caches(), name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "blob"), make([]byte, 1<<20), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	makeOld(t, filepath.Join(f.home.Caches(), "uv"))
	runner := &pruneRunner{caches: f.home.Caches()}
	cfg := f.config(reap.Inventory{})
	cfg.Commands = runner
	cfg.Settings.CachesCapGB = 1.5 / 1024

	// Act
	record := Sweep(context.Background(), cfg)

	// Assert
	if len(runner.ran) != 1 || runner.ran[0] != "uv cache prune" {
		t.Errorf("prunes run = %v, want uv's alone, the cache used longest ago, which brings the folder under its cap", runner.ran)
	}
	if item, ok := has(record.Removed, filepath.Join(f.home.Caches(), "uv")); !ok || item.Bytes != 1<<20 {
		t.Errorf("removed = %+v, want uv's megabyte", record.Removed)
	}
}

func TestRecordRoundTripsAndKeysItsStrays(t *testing.T) {
	stateDir := t.TempDir()
	record := Record{Time: time.Now().UTC().Truncate(time.Second), Strays: []Item{{Kind: "worktree", Path: `C:\B`}, {Kind: "worktree", Path: `C:\a`}}, Removed: []Item{{Bytes: 3}, {Bytes: 4}}}
	if err := WriteRecord(stateDir, record); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRecord(stateDir)
	if err != nil || !got.Time.Equal(record.Time) || got.Freed() != 7 || got.StrayKey() != `c:\a`+"\n"+`c:\b` {
		t.Errorf("ReadRecord = %+v, %v (freed %d, key %q)", got, err, got.Freed(), got.StrayKey())
	}
}
