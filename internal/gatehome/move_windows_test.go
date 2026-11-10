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

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/installbase"
)

// fakeDaemon stands in for no-mistakes' daemon and records each call.
type fakeDaemon struct {
	calls []string
	// refusals is how many Uninstall calls of the old home refuse, as
	// no-mistakes does while a gate run is in flight.
	refusals int
	// failStart fails Start on the named home.
	failStart string
	// onHold runs when the home is held.
	onHold func()
	// onStart runs when a daemon is asked to start, before it answers.
	onStart func(root string)
}

func (d *fakeDaemon) Uninstall(_ context.Context, root string) error {
	d.calls = append(d.calls, "uninstall "+root)
	if d.refusals > 0 {
		d.refusals--
		return errors.New("refusing daemon uninstall: 1 pipeline run is active")
	}
	return nil
}

func (d *fakeDaemon) Hold(_ context.Context, root string) (func() error, error) {
	d.calls = append(d.calls, "hold "+root)
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
	if want := []string{"uninstall " + s.old, "hold " + s.old, "release " + s.old, "start " + s.target}; !slices.Equal(s.daemon.calls, want) {
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
	if err == nil || !strings.Contains(err.Error(), "while gate runs stay in flight") {
		t.Fatalf("err = %v, want a refusal while runs are in flight", err)
	}
	if want := slices.Repeat([]string{"uninstall " + s.old}, 3); !slices.Equal(s.daemon.calls, want) {
		t.Errorf("daemon calls = %q, want %q", s.daemon.calls, want)
	}
	if waits != 3 {
		t.Errorf("waited %d times, want 3", waits)
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
	if want := []string{"uninstall " + s.old, "hold " + s.old, "release " + s.old, "start " + s.old}; !slices.Equal(s.daemon.calls, want) {
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
	if want := []string{"uninstall " + s.old, "hold " + s.old, "release " + s.old, "start " + s.target, "uninstall " + s.target, "start " + s.old}; !slices.Equal(s.daemon.calls, want) {
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
			name: "a link that leads elsewhere",
			arrange: func(t *testing.T, s scratch) {
				elsewhere := filepath.Join(s.devDrive, "elsewhere")
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
	if want := []string{"uninstall " + s.old, "hold " + s.old, "release " + s.old, "start " + s.target}; !slices.Equal(s.daemon.calls, want) {
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
	if want := []string{"uninstall " + s.old, "hold " + s.old, "release " + s.old, "start " + s.target, "uninstall " + s.target, "start " + s.old}; !slices.Equal(s.daemon.calls, want) {
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
