package gatehome

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/installbase"
)

// fakeDaemon stands in for no-mistakes' daemon and records each call.
type fakeDaemon struct {
	calls []string
	// refusals is how many Stop calls of the old home refuse, as
	// no-mistakes does while a gate run is in flight.
	refusals int
	// busyHolds is how many Hold calls refuse, as the daemon's lock does
	// while a daemon holds it.
	busyHolds int
	// failStart fails Start on the named home.
	failStart string
	// onHold runs when the home is held.
	onHold func()
	// onStart runs when a daemon is asked to start, before it answers.
	onStart func(root string)
}

func (d *fakeDaemon) Stop(_ context.Context, root string) error {
	d.calls = append(d.calls, "stop "+root)
	if d.refusals > 0 {
		d.refusals--
		return errors.New("refusing daemon stop because 1 active pipeline runs are in progress")
	}
	return nil
}

func (d *fakeDaemon) Forget(_ context.Context, root string) error {
	d.calls = append(d.calls, "forget "+root)
	return nil
}

func (d *fakeDaemon) Hold(_ context.Context, root string) (func() error, error) {
	d.calls = append(d.calls, "hold "+root)
	if d.busyHolds > 0 {
		d.busyHolds--
		return nil, errors.New("pipeline busy: the no-mistakes daemon is running and no gate run is in flight")
	}
	if d.onHold != nil {
		d.onHold()
	}
	return func() error { d.calls = append(d.calls, "release "+root); return nil }, nil
}

func (d *fakeDaemon) Start(_ context.Context, root string) error {
	d.calls = append(d.calls, "start "+root)
	if d.onStart != nil {
		d.onStart(root)
	}
	if root == d.failStart {
		return errors.New("daemon did not answer")
	}
	return nil
}

// scratch is a machine for one move: an old gate home with a database, a
// config, one gated bare repository, the daemon's runtime files, and one
// checkout that pushes to it; and a Dev Drive folder.
type scratch struct {
	old, devDrive, target, checkout, repository string
	env                                         installbase.EnvStore
	daemon                                      *fakeDaemon
}

func newScratch(t *testing.T) scratch {
	t.Helper()
	root := t.TempDir()
	s := scratch{old: filepath.Join(root, "profile", ".no-mistakes"), devDrive: filepath.Join(root, "dev"), checkout: filepath.Join(root, "project")}
	s.target = filepath.Join(s.devDrive, "no-mistakes")
	s.repository = filepath.Join(s.old, "repos", "a1.git")
	t.Setenv(installbase.UserEnvFileVariable, filepath.Join(root, "user-env.json"))
	s.env = installbase.NewEnvStore(nil)
	s.daemon = &fakeDaemon{}
	for _, folder := range []string{s.checkout, filepath.Join(s.old, "logs", "r1"), filepath.Join(s.old, "worktrees", "a1", "r1"), s.devDrive} {
		mustMkdir(t, folder)
	}
	runGit(t, "", "init", "--bare", "-q", s.repository)
	runGit(t, s.repository, "config", "extensions.worktreeConfig", "true")
	runGit(t, s.repository, "config", "--worktree", "core.hooksPath", filepath.Join(s.repository, "hooks"))
	runGit(t, "", "init", "-q", s.checkout)
	runGit(t, s.checkout, "remote", "add", "no-mistakes", s.repository)
	writeFile(t, filepath.Join(s.old, "config.yaml"), "worktree_roots: {}\nforge:\n  gh_config_dir: '"+filepath.ToSlash(filepath.Join(s.old, "forge-profiles", "owner"))+"'\n")
	writeFile(t, filepath.Join(s.old, "logs", "r1", "test.log"), "a log line\n")
	writeFile(t, filepath.Join(s.old, "worktrees", "a1", "r1", "file.txt"), "a run's file\n")
	for _, runtime := range []string{"socket", "daemon.pid", "daemon.lock"} {
		writeFile(t, filepath.Join(s.old, runtime), "the old daemon's\n")
	}
	db := openDB(t, filepath.Join(s.old, "state.sqlite"))
	defer db.Close()
	for _, statement := range []string{
		"CREATE TABLE repos (id TEXT PRIMARY KEY, working_path TEXT NOT NULL)",
		"CREATE TABLE runs (id TEXT PRIMARY KEY, worktree_dir TEXT, attempts INTEGER)",
		"CREATE TABLE validation_admissions (id INTEGER PRIMARY KEY, event_json TEXT)",
	} {
		mustExec(t, db, statement)
	}
	mustExec(t, db, "INSERT INTO repos VALUES ('a1', ?)", s.checkout)
	mustExec(t, db, "INSERT INTO runs VALUES ('r1', ?, 1), ('r2', ?, 2), ('r3', ?, 3)",
		filepath.Join(s.old, "worktrees", "a1", "r1"),
		strings.ToUpper(filepath.Join(s.old, "worktrees", "a1", "r2")),
		s.old+"-backup\\r3")
	mustExec(t, db, "INSERT INTO validation_admissions (event_json) VALUES (?)", `{"log":"`+strings.ReplaceAll(filepath.Join(s.old, "logs", "r1", "test.log"), `\`, `\\`)+`"}`)
	return s
}

func (s scratch) mover() Mover {
	return Mover{Daemon: s.daemon, Env: s.env, Git: execx.OSRunner{}, Tries: 3, Now: func() time.Time { return time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC) }}
}

func TestMoveCarriesTheHomeAndPointsEveryRecordAtIt(t *testing.T) {
	// Arrange
	s := newScratch(t)

	// Act
	line, isCarried, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err != nil || !isCarried {
		t.Fatalf("carried %v, %v", isCarried, err)
	}
	if !strings.HasPrefix(line, "moved: the gate's home is "+s.target) {
		t.Errorf("line = %q", line)
	}
	if want := []string{"stop " + s.old, "hold " + s.old, "release " + s.old, "start " + s.target, "forget " + s.old}; !slices.Equal(s.daemon.calls, want) {
		t.Errorf("daemon calls = %q, want %q", s.daemon.calls, want)
	}
	if value, isSet, _ := s.env.Get(Variable); !isSet || value != s.target {
		t.Errorf("user NM_HOME = %q (set %v), want %s", value, isSet, s.target)
	}
	if _, err := os.Lstat(s.old); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old home is still there: %v", err)
	}
	for _, runtime := range []string{"socket", "daemon.pid", "daemon.lock"} {
		if _, err := os.Lstat(filepath.Join(s.target, runtime)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s was carried: %v", runtime, err)
		}
	}
	assertFile(t, filepath.Join(s.target, "worktrees", "a1", "r1", "file.txt"), "a run's file\n")
	assertFile(t, filepath.Join(s.target, "config.yaml"), "worktree_roots: {}\nforge:\n  gh_config_dir: '"+filepath.ToSlash(filepath.Join(s.target, "forge-profiles", "owner"))+"'\n")
	moved := filepath.Join(s.target, "repos", "a1.git")
	if got := gitOutput(t, moved, "config", "--worktree", "core.hooksPath"); got != filepath.Join(moved, "hooks") {
		t.Errorf("hooks path = %q, want %q", got, filepath.Join(moved, "hooks"))
	}
	if got := gitOutput(t, s.checkout, "remote", "get-url", "no-mistakes"); got != moved {
		t.Errorf("no-mistakes remote = %q, want %q", got, moved)
	}
	db := openDB(t, filepath.Join(s.target, "state.sqlite"))
	defer db.Close()
	wantRuns := map[string]string{
		"r1": filepath.Join(s.target, "worktrees", "a1", "r1"),
		"r2": filepath.Join(s.target, "WORKTREES", "A1", "R2"),
		"r3": s.old + "-backup\\r3",
	}
	for id, want := range wantRuns {
		var got string
		if err := db.QueryRow("SELECT worktree_dir FROM runs WHERE id = ?", id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("run %s worktree_dir = %q, want %q", id, got, want)
		}
	}
	var event string
	if err := db.QueryRow("SELECT event_json FROM validation_admissions").Scan(&event); err != nil {
		t.Fatal(err)
	}
	if want := `{"log":"` + strings.ReplaceAll(filepath.Join(s.target, "logs", "r1", "test.log"), `\`, `\\`) + `"}`; event != want {
		t.Errorf("event_json = %q, want %q", event, want)
	}
}

// A move whose gate runs stay in flight through every try changes nothing
// and says the one true thing about the daemon: it was never stopped.
func TestMoveIsRefusedWhileAGateRunIsInFlight(t *testing.T) {
	// Arrange
	s := newScratch(t)
	s.daemon.refusals = 99
	var waits int

	// Act
	mover := s.mover()
	mover.Waiting = func(error) { waits++ }
	_, _, err := mover.Move(context.Background(), s.old, s.devDrive)

	// Assert
	if !errors.Is(err, ErrGateBusy) || !strings.Contains(err.Error(), "nothing moved, and the daemon was not stopped and runs as before") {
		t.Fatalf("err = %v, want a busy gate whose daemon was never stopped", err)
	}
	if want := slices.Repeat([]string{"stop " + s.old}, 3); !slices.Equal(s.daemon.calls, want) {
		t.Errorf("daemon calls = %q, want %q", s.daemon.calls, want)
	}
	if waits != 3 {
		t.Errorf("waited %d times, want 3", waits)
	}
	assertUntouched(t, s)
}

// A move started while a gate run is in flight waits, and carries the home
// once the run ended: a daemon that still runs, which a stop that changed
// nothing leaves, is waited for as a refused stop is (#2525).
func TestMoveWaitsForTheGateToRestAndThenCarriesTheHome(t *testing.T) {
	// Arrange
	s := newScratch(t)
	s.daemon.refusals, s.daemon.busyHolds = 1, 1
	var waits []string

	// Act
	mover := s.mover()
	mover.Waiting = func(err error) { waits = append(waits, err.Error()) }
	line, isCarried, err := mover.Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err != nil || !isCarried || !strings.HasPrefix(line, "moved: the gate's home is "+s.target) {
		t.Fatalf("move = %q, %v, %v; want the home carried once the gate rested", line, isCarried, err)
	}
	if want := []string{"stop " + s.old, "stop " + s.old, "hold " + s.old, "stop " + s.old, "hold " + s.old, "release " + s.old, "start " + s.target, "forget " + s.old}; !slices.Equal(s.daemon.calls, want) {
		t.Errorf("daemon calls = %q, want %q", s.daemon.calls, want)
	}
	if len(waits) != 2 {
		t.Errorf("waited on %q, want the refused stop and the busy hold", waits)
	}
	if _, err := os.Lstat(s.old); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old home is still there: %v", err)
	}
}

// A move that stopped the daemon but never held it off starts it again on
// the old home, which it never changed, and says so.
func TestAMoveThatStoppedTheDaemonAndGaveUpStartsItAgain(t *testing.T) {
	// Arrange
	s := newScratch(t)
	s.daemon.busyHolds = 99

	// Act
	_, _, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if !errors.Is(err, ErrGateBusy) || !strings.Contains(err.Error(), "nothing moved, and the daemon was stopped and runs again on "+s.old) {
		t.Fatalf("err = %v, want a busy gate whose daemon runs again", err)
	}
	if want := append(slices.Repeat([]string{"stop " + s.old, "hold " + s.old}, 3), "start "+s.old); !slices.Equal(s.daemon.calls, want) {
		t.Errorf("daemon calls = %q, want %q", s.daemon.calls, want)
	}
	assertUntouched(t, s)
}

func TestAnInterruptedCopyUndoesTheMove(t *testing.T) {
	// Arrange
	s := newScratch(t)
	ctx := &endsAfter{Context: context.Background(), checks: 4}

	// Act
	_, _, err := s.mover().Move(ctx, s.old, s.devDrive)

	// Assert
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the copy's interruption", err)
	}
	if want := []string{"stop " + s.old, "hold " + s.old, "release " + s.old, "start " + s.old}; !slices.Equal(s.daemon.calls, want) {
		t.Errorf("daemon calls = %q, want %q", s.daemon.calls, want)
	}
	assertUntouched(t, s)
}

func TestAFailedStartOnTheCopyUndoesTheSwitch(t *testing.T) {
	// Arrange
	s := newScratch(t)
	s.daemon.failStart = s.target
	if err := s.env.Set(Variable, s.old); err != nil {
		t.Fatal(err)
	}

	// Act
	_, _, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "did not start on "+s.target) {
		t.Fatalf("err = %v, want the failed start", err)
	}
	if want := []string{"stop " + s.old, "hold " + s.old, "release " + s.old, "start " + s.target, "stop " + s.target, "forget " + s.target, "start " + s.old}; !slices.Equal(s.daemon.calls, want) {
		t.Errorf("daemon calls = %q, want %q", s.daemon.calls, want)
	}
	if value, _, _ := s.env.Get(Variable); value != s.old {
		t.Errorf("user NM_HOME = %q, want it back at %s", value, s.old)
	}
	if _, err := os.Lstat(s.target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the copy is still there: %v", err)
	}
	if got := gitOutput(t, s.checkout, "remote", "get-url", "no-mistakes"); got != s.repository {
		t.Errorf("no-mistakes remote = %q, want it back at %q", got, s.repository)
	}
}

func TestMoveCarriesAHomeAMoveByHandLinkedIntoTheDevDrive(t *testing.T) {
	// Arrange: the move by hand of 2026-11-09 copied the whole home onto the
	// Dev Drive, then put junctions at repos and each repository's worktrees
	// in place of the home's own, so the copy's database and config are
	// stale while its repositories are live.
	s := newScratch(t)
	mustMkdir(t, s.target)
	for _, folder := range []string{"repos", filepath.Join("worktrees", "a1")} {
		mustMkdir(t, filepath.Dir(filepath.Join(s.target, folder)))
		if err := os.Rename(filepath.Join(s.old, folder), filepath.Join(s.target, folder)); err != nil {
			t.Fatal(err)
		}
		junction(t, filepath.Join(s.old, folder), filepath.Join(s.target, folder))
	}
	writeFile(t, filepath.Join(s.target, "state.sqlite"), "a stale copy")
	writeFile(t, filepath.Join(s.target, "state.sqlite-wal"), "a stale log of it")
	writeFile(t, filepath.Join(s.target, "logs", "r0", "old.log"), "stale\n")

	// Act
	_, _, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(s.target, "state.sqlite-wal")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the stale log of the database stayed beside the moved one: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(s.target, "logs", "r0")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the stale logs stayed: %v", err)
	}
	assertFile(t, filepath.Join(s.target, "logs", "r1", "test.log"), "a log line\n")
	assertFile(t, filepath.Join(s.target, "worktrees", "a1", "r1", "file.txt"), "a run's file\n")
	moved := filepath.Join(s.target, "repos", "a1.git")
	if got := gitOutput(t, moved, "config", "--worktree", "core.hooksPath"); got != filepath.Join(moved, "hooks") {
		t.Errorf("hooks path = %q, want %q", got, filepath.Join(moved, "hooks"))
	}
	if got := gitOutput(t, s.checkout, "remote", "get-url", "no-mistakes"); got != moved {
		t.Errorf("no-mistakes remote = %q, want %q", got, moved)
	}
	db := openDB(t, filepath.Join(s.target, "state.sqlite"))
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM runs").Scan(&count); err != nil || count != 3 {
		t.Errorf("runs = %d (%v), want the home's 3", count, err)
	}
	if _, err := os.Lstat(s.old); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old home is still there: %v", err)
	}
	entries, err := os.ReadDir(s.devDrive)
	if err != nil || len(entries) != 1 {
		t.Errorf("the Dev Drive folder holds %v (%v), want only the moved home", entries, err)
	}
}

func TestAFailedMoveOfAHomeLinkedByHandPutsTheDevDriveFolderBack(t *testing.T) {
	// Arrange
	s := newScratch(t)
	mustMkdir(t, s.target)
	if err := os.Rename(filepath.Join(s.old, "repos"), filepath.Join(s.target, "repos")); err != nil {
		t.Fatal(err)
	}
	junction(t, filepath.Join(s.old, "repos"), filepath.Join(s.target, "repos"))
	hooks := gitOutput(t, s.repository, "config", "--worktree", "core.hooksPath")
	writeFile(t, filepath.Join(s.target, "state.sqlite"), "a stale copy")
	s.daemon.failStart = s.target

	// Act
	_, _, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err == nil {
		t.Fatal("the move succeeded")
	}
	assertFile(t, filepath.Join(s.target, "state.sqlite"), "a stale copy")
	if got := gitOutput(t, s.repository, "config", "--worktree", "core.hooksPath"); got != hooks {
		t.Errorf("hooks path = %q, want it back at %q", got, hooks)
	}
	if entries, _ := os.ReadDir(s.devDrive); len(entries) != 1 {
		t.Errorf("the Dev Drive folder holds %v, want the hand move's folder alone", entries)
	}
	if _, err := os.Stat(filepath.Join(s.old, "state.sqlite")); err != nil {
		t.Errorf("the old home lost its database: %v", err)
	}
}

func TestMoveReplacesTheCopyOfAMoveEndedOutright(t *testing.T) {
	// Arrange: a move whose terminal was closed part way through its copy.
	s := newScratch(t)
	writeFile(t, filepath.Join(s.target, moveMarker), "")
	writeFile(t, filepath.Join(s.target, "state.sqlite"), "half a copy")
	writeFile(t, filepath.Join(s.target, "logs", "r1", "test.log"), "half")

	// Act
	_, _, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(s.target, "logs", "r1", "test.log"), "a log line\n")
	db := openDB(t, filepath.Join(s.target, "state.sqlite"))
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM runs").Scan(&count); err != nil || count != 3 {
		t.Errorf("runs = %d (%v), want the home's 3", count, err)
	}
	if _, err := os.Lstat(filepath.Join(s.target, moveMarker)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the move's marker stayed: %v", err)
	}
	if entries, _ := os.ReadDir(s.devDrive); len(entries) != 1 {
		t.Errorf("the Dev Drive folder holds %v, want only the moved home", entries)
	}
}

func TestMoveRefusesBeforeTouchingTheDaemon(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(t *testing.T, s scratch)
		want    string
	}{
		{
			name: "a Dev Drive folder that holds something no link explains",
			arrange: func(t *testing.T, s scratch) {
				writeFile(t, filepath.Join(s.target, "state.sqlite"), "someone else's")
			},
			want: "does not copy over it",
		},
		{
			name: "a home that is itself a junction to another folder",
			arrange: func(t *testing.T, s scratch) {
				elsewhere := filepath.Join(s.devDrive, "elsewhere")
				if err := os.Rename(s.old, elsewhere); err != nil {
					t.Fatal(err)
				}
				junction(t, s.old, elsewhere)
			},
			want: "and the move carries only a junction to ",
		},
		{
			name: "a link into the Dev Drive's gate home that leads to another place there",
			arrange: func(t *testing.T, s scratch) {
				elsewhere := filepath.Join(s.target, "elsewhere")
				mustMkdir(t, s.target)
				if err := os.Rename(filepath.Join(s.old, "logs"), elsewhere); err != nil {
					t.Fatal(err)
				}
				junction(t, filepath.Join(s.old, "logs"), elsewhere)
			},
			want: "cannot carry it",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s := newScratch(t)
			test.arrange(t, s)

			// Act
			_, _, err := s.mover().Move(context.Background(), s.old, s.devDrive)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
			if len(s.daemon.calls) != 0 {
				t.Errorf("daemon calls = %q, want none", s.daemon.calls)
			}
		})
	}
}

// sentinel is a file written under the Dev Drive folder before a move.
const sentinel = "written under the Dev Drive before the move"

// asJunctionToTheDevDrive puts the whole home in the Dev Drive folder and a
// junction to it at the old home, as a move by hand of the whole home does.
func asJunctionToTheDevDrive(t *testing.T, s scratch) {
	t.Helper()
	if err := os.Rename(s.old, s.target); err != nil {
		t.Fatal(err)
	}
	junction(t, s.old, s.target)
	writeFile(t, filepath.Join(s.target, "sentinel.txt"), sentinel)
}

// filesIn are the bytes of every file below folder, by its path there.
func filesIn(t *testing.T, folder string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(folder, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(folder, path)
		files[relative] = string(raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestMoveCarriesAHomeThatIsItselfAJunctionToTheDevDriveInPlace(t *testing.T) {
	// Arrange
	s := newScratch(t)
	asJunctionToTheDevDrive(t, s)
	var atStart map[string]string
	s.daemon.onStart = func(root string) {
		if root == s.target {
			atStart = filesIn(t, s.target)
		}
	}

	// Act
	line, isCarried, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err != nil || !isCarried || !strings.Contains(line, "the junction "+s.old+" is gone, removed as a link") {
		t.Fatalf("line = %q, carried %v, err = %v", line, isCarried, err)
	}
	if want := []string{"stop " + s.old, "hold " + s.old, "release " + s.old, "start " + s.target, "forget " + s.old}; !slices.Equal(s.daemon.calls, want) {
		t.Errorf("daemon calls = %q, want %q", s.daemon.calls, want)
	}
	if _, err := os.Lstat(s.old); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the junction is still at the old home: %v", err)
	}
	if value, isSet, _ := s.env.Get(Variable); !isSet || value != s.target {
		t.Errorf("user NM_HOME = %q (set %v), want %s", value, isSet, s.target)
	}
	after := filesIn(t, s.target)
	for _, path := range []string{"state.sqlite", filepath.Join("repos", "a1.git", "HEAD"), "sentinel.txt"} {
		if _, ok := after[path]; !ok {
			t.Errorf("the Dev Drive folder lost %s", path)
		}
	}
	if !maps.Equal(after, atStart) {
		t.Errorf("the Dev Drive folder changed as the junction went: %d files after, %d at the daemon's start", len(after), len(atStart))
	}
	assertFile(t, filepath.Join(s.target, "sentinel.txt"), sentinel)
	moved := filepath.Join(s.target, "repos", "a1.git")
	if got := gitOutput(t, moved, "config", "--worktree", "core.hooksPath"); got != filepath.Join(moved, "hooks") {
		t.Errorf("hooks path = %q, want %q", got, filepath.Join(moved, "hooks"))
	}
	if got := gitOutput(t, s.checkout, "remote", "get-url", "no-mistakes"); got != moved {
		t.Errorf("no-mistakes remote = %q, want %q", got, moved)
	}
	db := openDB(t, filepath.Join(s.target, "state.sqlite"))
	defer db.Close()
	var worktree string
	if err := db.QueryRow("SELECT worktree_dir FROM runs WHERE id = 'r1'").Scan(&worktree); err != nil || worktree != filepath.Join(s.target, "worktrees", "a1", "r1") {
		t.Errorf("run r1 worktree_dir = %q (%v), want it in %s", worktree, err, s.target)
	}
}

func TestAFailedStartOfAHomeThatIsAJunctionLeavesItAsItWas(t *testing.T) {
	// Arrange
	s := newScratch(t)
	asJunctionToTheDevDrive(t, s)
	s.daemon.failStart = s.target
	before := filesIn(t, s.target)

	// Act
	_, isCarried, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err == nil || isCarried || !strings.Contains(err.Error(), "did not start on "+s.target) {
		t.Fatalf("carried %v, err = %v; want the failed start", isCarried, err)
	}
	if want := []string{"stop " + s.old, "hold " + s.old, "release " + s.old, "start " + s.target, "stop " + s.target, "forget " + s.target, "start " + s.old}; !slices.Equal(s.daemon.calls, want) {
		t.Errorf("daemon calls = %q, want %q", s.daemon.calls, want)
	}
	if linked, isLink := linkTarget(s.old); !isLink || !sameFolder(linked, s.target) {
		t.Errorf("the old home is %q (a link %v), want the junction to %s", linked, isLink, s.target)
	}
	if after := filesIn(t, s.target); !maps.Equal(after, before) {
		t.Errorf("the Dev Drive folder changed: %d files after, %d before", len(after), len(before))
	}
	if _, isSet, _ := s.env.Get(Variable); isSet {
		t.Error("user NM_HOME was set")
	}
	if got := gitOutput(t, s.checkout, "remote", "get-url", "no-mistakes"); got != s.repository {
		t.Errorf("no-mistakes remote = %q, want it back at %q", got, s.repository)
	}
}

// runLinks is a finished run's checkout in the scratch home, as pnpm and
// Next.js leave one: a folder inside it that its links lead to, and a folder
// beside the home that others lead out to.
type runLinks struct {
	checkout, inside, outside string
}

func newRunLinks(t *testing.T, s scratch) runLinks {
	t.Helper()
	checkout := filepath.Join(s.old, "worktrees", "a1", "r1")
	links := runLinks{checkout: checkout, inside: filepath.Join(checkout, "node_modules", ".pnpm", "pkg"), outside: filepath.Join(filepath.Dir(s.checkout), "outside")}
	writeFile(t, filepath.Join(links.inside, "index.js"), "inside\n")
	writeFile(t, filepath.Join(links.outside, "package.json"), "outside\n")
	return links
}

// withJunctions gives the checkout a junction to the folder inside it and
// one to the folder outside the home.
func (l runLinks) withJunctions(t *testing.T) {
	t.Helper()
	junction(t, filepath.Join(l.checkout, "node_modules", "pkg"), l.inside)
	junction(t, filepath.Join(l.checkout, "linked-outside"), l.outside)
}

// symlinkOrSkip makes a folder's symbolic link, or skips the test on an
// account without the privilege to make one, outside CI alone.
func symlinkOrSkip(t *testing.T, link, target string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(link))
	if err := os.Symlink(target, link); errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
		if os.Getenv("CI") == "true" {
			t.Fatalf("CI must be able to make symbolic links, so the move's symbolic-link cases are proven there: %v", err)
		}
		t.Skipf("this account may not make symbolic links: %v", err)
	} else if err != nil {
		t.Fatal(err)
	}
}

// assertLink fails unless path is a link of the kind given that leads to
// target.
func assertLink(t *testing.T, path, target string, isJunction bool) {
	t.Helper()
	linked, isLink := linkTarget(path)
	if !isLink || linked != target {
		t.Errorf("%s leads to %q (a link %v), want %q", path, linked, isLink, target)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if isSymlink := info.Mode()&os.ModeSymlink != 0; isSymlink == isJunction {
		t.Errorf("%s is a symbolic link %v, want a junction %v", path, isSymlink, isJunction)
	}
}

func TestMoveCarriesTheJunctionsOfARunCheckoutAsJunctions(t *testing.T) {
	// Arrange
	s := newScratch(t)
	links := newRunLinks(t, s)
	links.withJunctions(t)

	// Act
	_, _, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(s.target, "worktrees", "a1", "r1")
	assertLink(t, filepath.Join(moved, "node_modules", "pkg"), filepath.Join(moved, "node_modules", ".pnpm", "pkg"), true)
	assertLink(t, filepath.Join(moved, "linked-outside"), links.outside, true)
	assertFile(t, filepath.Join(moved, "node_modules", "pkg", "index.js"), "inside\n")
	assertFile(t, filepath.Join(links.outside, "package.json"), "outside\n")
	if _, err := os.Lstat(s.old); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old home is still there: %v", err)
	}
}

func TestMoveCarriesTheSymbolicLinksOfARunCheckoutAsSymbolicLinks(t *testing.T) {
	// Arrange
	s := newScratch(t)
	links := newRunLinks(t, s)
	relative := filepath.Join("..", "..", "node_modules", ".pnpm", "pkg")
	symlinkOrSkip(t, filepath.Join(links.checkout, ".next", "node_modules", "pkg"), relative)
	symlinkOrSkip(t, filepath.Join(links.checkout, "outside-link"), links.outside)

	// Act
	_, _, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(s.target, "worktrees", "a1", "r1")
	assertLink(t, filepath.Join(moved, ".next", "node_modules", "pkg"), relative, false)
	assertLink(t, filepath.Join(moved, "outside-link"), links.outside, false)
	assertFile(t, filepath.Join(moved, ".next", "node_modules", "pkg", "index.js"), "inside\n")
	assertFile(t, filepath.Join(moved, "outside-link", "package.json"), "outside\n")
	assertFile(t, filepath.Join(links.outside, "package.json"), "outside\n")
	if _, err := os.Lstat(s.old); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old home is still there: %v", err)
	}
}

func TestALinkThatCannotBeMadeAgainUndoesTheMove(t *testing.T) {
	// Arrange
	s := newScratch(t)
	links := newRunLinks(t, s)
	links.withJunctions(t)
	refused := filepath.Join(links.checkout, "linked-outside")
	makeLink = func(path, target string, isJunction, isDirectory bool) error {
		if filepath.Base(path) == "linked-outside" {
			return errors.New("refused for the test")
		}
		return createLink(path, target, isJunction, isDirectory)
	}
	t.Cleanup(func() { makeLink = createLink })

	// Act
	_, isCarried, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err == nil || isCarried || !strings.Contains(err.Error(), "the link "+refused) || !strings.Contains(err.Error(), "refused for the test") {
		t.Fatalf("carried %v, err = %v; want the link %s named", isCarried, err, refused)
	}
	if want := []string{"stop " + s.old, "hold " + s.old, "release " + s.old, "start " + s.old}; !slices.Equal(s.daemon.calls, want) {
		t.Errorf("daemon calls = %q, want %q", s.daemon.calls, want)
	}
	assertUntouched(t, s)
	assertLink(t, refused, links.outside, true)
	assertFile(t, filepath.Join(links.outside, "package.json"), "outside\n")
}

func TestAFailedStartRemovesTheLinksMadeAgainAsLinks(t *testing.T) {
	// Arrange
	s := newScratch(t)
	links := newRunLinks(t, s)
	links.withJunctions(t)
	s.daemon.failStart = s.target

	// Act
	_, _, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "did not start on "+s.target) {
		t.Fatalf("err = %v, want the failed start", err)
	}
	if _, err := os.Lstat(s.target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the copy is still there: %v", err)
	}
	assertFile(t, filepath.Join(links.outside, "package.json"), "outside\n")
	assertFile(t, filepath.Join(links.inside, "index.js"), "inside\n")
	assertLink(t, filepath.Join(links.checkout, "linked-outside"), links.outside, true)
}

// throughTheOldHome gives the run checkout a junction to the folder inside it
// spelled through the old home's path, as a gate run that used that path
// made it, and returns where the junction is below the Dev Drive folder.
func (l runLinks) throughTheOldHome(t *testing.T, s scratch) string {
	t.Helper()
	relative, err := filepath.Rel(s.old, l.checkout)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(s.target, relative, "node_modules", "pkg")
	junction(t, link, l.inside)
	return link
}

// assertRebased fails unless link is a junction to the folder inside the run
// checkout at its place in the moved home, and the file in it reads whole.
func assertRebased(t *testing.T, s scratch, l runLinks, link string) {
	t.Helper()
	relative, err := filepath.Rel(s.old, l.inside)
	if err != nil {
		t.Fatal(err)
	}
	assertLink(t, link, filepath.Join(s.target, relative), true)
	assertFile(t, filepath.Join(link, "index.js"), "inside\n")
}

func TestMoveInPlaceRebasesTheLinksSpelledThroughTheOldHome(t *testing.T) {
	// Arrange: the whole home is a junction to the Dev Drive folder, and a run
	// that used the old path left a junction spelled through it.
	s := newScratch(t)
	links := newRunLinks(t, s)
	asJunctionToTheDevDrive(t, s)
	link := links.throughTheOldHome(t, s)

	// Act
	_, _, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	assertRebased(t, s, links, link)
}

func TestMoveOfAHomeLinkedByHandRebasesTheLinksSpelledThroughTheOldHome(t *testing.T) {
	// Arrange: a move by hand put the repository's worktrees on the Dev Drive
	// behind a junction, and a run that used the old path left a junction in
	// them spelled through it.
	s := newScratch(t)
	links := newRunLinks(t, s)
	kept := filepath.Join("worktrees", "a1")
	mustMkdir(t, filepath.Dir(filepath.Join(s.target, kept)))
	if err := os.Rename(filepath.Join(s.old, kept), filepath.Join(s.target, kept)); err != nil {
		t.Fatal(err)
	}
	junction(t, filepath.Join(s.old, kept), filepath.Join(s.target, kept))
	link := links.throughTheOldHome(t, s)

	// Act
	_, _, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(s.old); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old home is still there: %v", err)
	}
	assertRebased(t, s, links, link)
}

func TestAFailedStartPutsTheRebasedLinksBack(t *testing.T) {
	// Arrange
	s := newScratch(t)
	links := newRunLinks(t, s)
	asJunctionToTheDevDrive(t, s)
	link := links.throughTheOldHome(t, s)
	s.daemon.failStart = s.target

	// Act
	_, _, err := s.mover().Move(context.Background(), s.old, s.devDrive)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "did not start on "+s.target) {
		t.Fatalf("err = %v, want the failed start", err)
	}
	assertLink(t, link, links.inside, true)
	assertFile(t, filepath.Join(link, "index.js"), "inside\n")
	assertFile(t, filepath.Join(links.inside, "index.js"), "inside\n")
}

func TestMoveLeavesAHomeAlreadyOnTheDevDrive(t *testing.T) {
	// Arrange
	s := newScratch(t)
	mustMkdir(t, s.target)

	// Act
	line, isCarried, err := s.mover().Move(context.Background(), s.target, s.devDrive)

	// Assert
	if err != nil || isCarried || !strings.HasPrefix(line, "already on the Dev Drive") {
		t.Fatalf("line = %q, carried %v, err = %v", line, isCarried, err)
	}
	if len(s.daemon.calls) != 0 {
		t.Errorf("daemon calls = %q, want none", s.daemon.calls)
	}
}

func TestMovePlacesAHomeNotMadeYetOnTheDevDrive(t *testing.T) {
	// Arrange
	s := newScratch(t)
	fresh := filepath.Join(filepath.Dir(s.old), ".no-mistakes-fresh")

	// Act
	line, isCarried, err := s.mover().Move(context.Background(), fresh, s.devDrive)

	// Assert
	if err != nil || !isCarried || !strings.HasPrefix(line, "placed:") {
		t.Fatalf("line = %q, carried %v, err = %v", line, isCarried, err)
	}
	if info, err := os.Stat(s.target); err != nil || !info.IsDir() {
		t.Errorf("the home on the Dev Drive was not made: %v", err)
	}
	if value, _, _ := s.env.Get(Variable); value != s.target {
		t.Errorf("user NM_HOME = %q, want %s", value, s.target)
	}
	if len(s.daemon.calls) != 0 {
		t.Errorf("daemon calls = %q, want none", s.daemon.calls)
	}
}

// endsAfter is a context that ends after checks reads of its error, so a
// copy stops part way through.
type endsAfter struct {
	context.Context
	checks int
}

func (c *endsAfter) Err() error {
	if c.checks <= 0 {
		return context.Canceled
	}
	c.checks--
	return nil
}

// assertUntouched fails unless the old home is whole, nothing is on the Dev
// Drive and nothing names the Dev Drive.
func assertUntouched(t *testing.T, s scratch) {
	t.Helper()
	if _, err := os.Lstat(s.target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the Dev Drive folder holds a copy: %v", err)
	}
	if _, isSet, _ := s.env.Get(Variable); isSet {
		t.Error("user NM_HOME was set")
	}
	if got := gitOutput(t, s.checkout, "remote", "get-url", "no-mistakes"); got != s.repository {
		t.Errorf("no-mistakes remote = %q, want %q", got, s.repository)
	}
	for _, path := range []string{"state.sqlite", "config.yaml", "socket", filepath.Join("worktrees", "a1", "r1", "file.txt")} {
		if _, err := os.Stat(filepath.Join(s.old, path)); err != nil {
			t.Errorf("the old home lost %s: %v", path, err)
		}
	}
}

func junction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, out)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return strings.TrimSpace(string(out))
}

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", databaseURL(path, ""))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func mustExec(t *testing.T, db *sql.DB, statement string, arguments ...any) {
	t.Helper()
	if _, err := db.Exec(statement, arguments...); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

func mustMkdir(t *testing.T, folder string) {
	t.Helper()
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}
