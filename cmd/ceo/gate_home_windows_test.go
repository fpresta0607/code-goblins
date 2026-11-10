package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
	"github.com/fpresta0607/code-goblins/internal/gatehome"
	"github.com/fpresta0607/code-goblins/internal/installbase"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// gateDaemonScratch is a scratch gate home with the tables the daemon's hold
// reads, a scratch Dev Drive folder, a scratch user environment, and a
// stand-in no-mistakes.exe first on PATH that records each call's arguments.
type gateDaemonScratch struct {
	old, devDrive, log string
}

func newGateDaemonScratch(t *testing.T) gateDaemonScratch {
	t.Helper()
	root := t.TempDir()
	s := gateDaemonScratch{old: filepath.Join(root, "profile", ".no-mistakes"), devDrive: filepath.Join(root, "dev"), log: filepath.Join(root, "no-mistakes-calls.log")}
	t.Setenv("CEO_HOME", filepath.Join(root, "ceo-home"))
	t.Setenv("CEO_STATE_OVERRIDE", "")
	t.Setenv(installbase.UserEnvFileVariable, filepath.Join(root, "user-env.json"))
	t.Setenv(gatehome.Variable, "")
	bin := filepath.Join(root, "bin")
	for _, folder := range []string{bin, s.old, s.devDrive} {
		if err := os.MkdirAll(folder, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	standin.Put(t, filepath.Join(bin, "no-mistakes.exe"))
	for _, variable := range standin.Env(s.log) {
		name, value, _ := strings.Cut(variable, "=")
		t.Setenv(name, value)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(s.old, "state.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		"CREATE TABLE repos (id TEXT PRIMARY KEY, working_path TEXT NOT NULL)",
		"CREATE TABLE runs (id TEXT PRIMARY KEY, repo_id TEXT, branch TEXT, status TEXT, created_at INTEGER, worktree_dir TEXT)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("INSERT INTO runs VALUES ('r1', 'a1', 'main', 'completed', 1, ?)", filepath.Join(s.old, "worktrees", "a1", "r1")); err != nil {
		t.Fatal(err)
	}
	return s
}

// calls are the no-mistakes calls the stand-in recorded, in order.
func (s gateDaemonScratch) calls(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(s.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\r\n")
}

// The real carry asks no-mistakes to uninstall the daemon of the old home,
// never to stop it by force, then to start it once the home moved; the
// user's NM_HOME then names the moved home. Which home each call names, and
// how, TestGateDaemonRunsNoMistakesWithTheHomeAsNMHomeAndNoUpdateCheck proves.
func TestCarryGateHomeUninstallsTheOldDaemonAndStartsItOnTheMovedHome(t *testing.T) {
	// Arrange
	s := newGateDaemonScratch(t)
	target := gatehome.Target(s.devDrive)

	// Act
	var out bytes.Buffer
	line, isCarried, err := carryGateHome(context.Background(), s.old, s.devDrive, 1, 0, &out)

	// Assert
	if err != nil || !isCarried || !strings.HasPrefix(line, "moved: the gate's home is "+target) {
		t.Fatalf("carry = %q, %v, %v; want it moved to %s", line, isCarried, err, target)
	}
	if want := []string{"no-mistakes daemon uninstall", "no-mistakes daemon start"}; !slices.Equal(s.calls(t), want) {
		t.Errorf("no-mistakes calls = %q, want %q", s.calls(t), want)
	}
	if value, isSet, err := installbase.UserVariable(gatehome.Variable); err != nil || !isSet || value != target {
		t.Errorf("user NM_HOME = %q (set %v, %v), want %s", value, isSet, err, target)
	}
	if _, err := os.Stat(filepath.Join(target, "state.sqlite")); err != nil {
		t.Errorf("the moved home has no database: %v", err)
	}
}

// The real carry holds the daemon lock of the old home, the one a daemon of
// that home takes, before it copies: while something else holds that lock
// nothing is copied, the user's NM_HOME is not set, and the daemon starts on
// the old home again.
func TestCarryGateHomeHoldsTheOldHomesDaemonLockBeforeItCopies(t *testing.T) {
	// Arrange
	s := newGateDaemonScratch(t)
	release, err := pipeline.Reader{Root: s.old, Commands: gatedb.Runner{Next: execx.OSRunner{}}}.Idle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	// Act
	var out bytes.Buffer
	_, isCarried, err := carryGateHome(context.Background(), s.old, s.devDrive, 1, 0, &out)

	// Assert
	if !errors.Is(err, pipeline.ErrBusy) || isCarried {
		t.Fatalf("carry = %v, %v; want it refused while the old home's daemon lock is held", isCarried, err)
	}
	if want := []string{"no-mistakes daemon uninstall", "no-mistakes daemon start"}; !slices.Equal(s.calls(t), want) {
		t.Errorf("no-mistakes calls = %q, want %q", s.calls(t), want)
	}
	if _, err := os.Lstat(gatehome.Target(s.devDrive)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("something was copied to the Dev Drive: %v", err)
	}
	if _, isSet, err := installbase.UserVariable(gatehome.Variable); err != nil || isSet {
		t.Errorf("user NM_HOME set %v (%v), want it unset", isSet, err)
	}
}

// gateDaemonRunner records each command the gate daemon runs and answers it
// with exit 0.
type gateDaemonRunner struct{ requests []execx.Request }

func (r *gateDaemonRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	return execx.Result{}, nil
}

// The gate daemon runs no-mistakes for each home with that home as NM_HOME,
// any NM_HOME of its own process dropped, and without no-mistakes' update
// check.
func TestGateDaemonRunsNoMistakesWithTheHomeAsNMHomeAndNoUpdateCheck(t *testing.T) {
	// Arrange
	t.Setenv(gatehome.Variable, `C:\elsewhere\no-mistakes`)
	old, target := `C:\Users\someone\.no-mistakes`, `D:\CodeGoblins\no-mistakes`
	runner := &gateDaemonRunner{}
	daemon := gateDaemon{commands: runner}

	// Act
	uninstallErr := daemon.Uninstall(context.Background(), old)
	startErr := daemon.Start(context.Background(), target)

	// Assert
	if uninstallErr != nil || startErr != nil {
		t.Fatalf("uninstall %v, start %v", uninstallErr, startErr)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("requests = %+v, want an uninstall and a start", runner.requests)
	}
	for index, want := range []struct{ args, root string }{{"daemon uninstall", old}, {"daemon start", target}} {
		request := runner.requests[index]
		if request.Name != "no-mistakes" || strings.Join(request.Args, " ") != want.args {
			t.Errorf("request %d = %s %q, want no-mistakes %s", index, request.Name, request.Args, want.args)
		}
		var homes []string
		for _, variable := range request.Env {
			if name, value, _ := strings.Cut(variable, "="); strings.EqualFold(name, gatehome.Variable) {
				homes = append(homes, value)
			}
		}
		if !slices.Equal(homes, []string{want.root}) {
			t.Errorf("request %d NM_HOME = %q, want only %s", index, homes, want.root)
		}
		if !slices.Contains(request.Env, "NO_MISTAKES_NO_UPDATE_CHECK=1") {
			t.Errorf("request %d lacks NO_MISTAKES_NO_UPDATE_CHECK=1", index)
		}
	}
}
