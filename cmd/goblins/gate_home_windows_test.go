package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"html"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	t.Setenv("GOBLINS_HOME", filepath.Join(root, "ceo-home"))
	t.Setenv("GOBLINS_STATE_OVERRIDE", "")
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

// gateTaskRunner answers schtasks itself, as a machine whose only scheduled
// tasks are tasks, each name to its definition, and records each schtasks
// call; every other program runs for real.
type gateTaskRunner struct {
	tasks map[string]string
	calls []string
}

func (r *gateTaskRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "schtasks" {
		return execx.OSRunner{}.Run(ctx, request)
	}
	r.calls = append(r.calls, strings.Join(request.Args, " "))
	switch {
	case slices.Equal(request.Args, []string{"/query", "/fo", "csv", "/nh"}):
		var listed strings.Builder
		for _, name := range slices.Sorted(maps.Keys(r.tasks)) {
			listed.WriteString(`"` + name + `","N/A","Ready"` + "\r\n")
		}
		return execx.Result{Stdout: []byte(listed.String())}, nil
	case len(request.Args) == 4 && request.Args[0] == "/query" && request.Args[3] == "/xml":
		return execx.Result{Stdout: []byte(r.tasks[request.Args[2]])}, nil
	case len(request.Args) == 4 && request.Args[0] == "/delete":
		delete(r.tasks, request.Args[2])
		return execx.Result{}, nil
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("ERROR: Invalid argument/option")}, nil
}

// gateDaemonTask is the definition of the scheduled task no-mistakes
// registers to start the daemon of the home at root at sign-in, as schtasks
// prints it.
func gateDaemonTask(root string) string {
	if strings.ContainsAny(root, " \t\"") {
		root = strconv.Quote(root)
	}
	return "<Task>\r\n  <Actions Context=\"Author\">\r\n    <Exec>\r\n      <Command>C:\\Programs\\no-mistakes\\no-mistakes.exe</Command>\r\n      <Arguments>daemon run --root " + html.EscapeString(root) + "</Arguments>\r\n    </Exec>\r\n  </Actions>\r\n</Task>\r\n"
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

// The real carry asks no-mistakes to stop the daemon of the old home, never
// by force and never with daemon uninstall, which changes nothing on Windows
// and so never waited for a gate run (#2525), then to start it once the home
// moved; the user's NM_HOME then names the moved home, and the old home's
// sign-in task goes while every other task stays. Which home each call
// names, and how, TestGateDaemonRunsNoMistakesWithTheHomeAsNMHomeAndNoUpdateCheck
// proves.
func TestCarryGateHomeStopsTheOldDaemonAndStartsItOnTheMovedHome(t *testing.T) {
	// Arrange
	s := newGateDaemonScratch(t)
	target := gatehome.Target(s.devDrive)
	elsewhere := filepath.Join(filepath.Dir(s.devDrive), "another home", ".no-mistakes")
	tasks := &gateTaskRunner{tasks: map[string]string{
		`\no-mistakes-daemon-0a0a0a0a`: gateDaemonTask(s.old),
		`\no-mistakes-daemon-0b0b0b0b`: gateDaemonTask(elsewhere),
		`\Another Program\Updater`:     "<Task><Actions><Exec><Arguments>--root " + html.EscapeString(s.old) + "</Arguments></Exec></Actions></Task>",
	}}

	// Act
	var out bytes.Buffer
	line, isCarried, err := carryGateHomeWith(context.Background(), tasks, s.old, s.devDrive, 1, 0, &out)

	// Assert
	if err != nil || !isCarried || !strings.HasPrefix(line, "moved: the gate's home is "+target) {
		t.Fatalf("carry = %q, %v, %v; want it moved to %s", line, isCarried, err, target)
	}
	if want := []string{"no-mistakes daemon stop", "no-mistakes daemon start"}; !slices.Equal(s.calls(t), want) {
		t.Errorf("no-mistakes calls = %q, want %q", s.calls(t), want)
	}
	if want := []string{`\Another Program\Updater`, `\no-mistakes-daemon-0b0b0b0b`}; !slices.Equal(slices.Sorted(maps.Keys(tasks.tasks)), want) {
		t.Errorf("scheduled tasks left = %q, want only the old home's gone: %q", slices.Sorted(maps.Keys(tasks.tasks)), want)
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
// through every try nothing is copied, the user's NM_HOME is not set, no
// scheduled task is read, and the daemon it stopped starts on the old home
// again.
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
	tasks := &gateTaskRunner{tasks: map[string]string{`\no-mistakes-daemon-0a0a0a0a`: gateDaemonTask(s.old)}}
	_, isCarried, err := carryGateHomeWith(context.Background(), tasks, s.old, s.devDrive, 2, 0, &out)

	// Assert
	if !errors.Is(err, pipeline.ErrBusy) || !errors.Is(err, gatehome.ErrGateBusy) || isCarried {
		t.Fatalf("carry = %v, %v; want it refused as busy while the old home's daemon lock is held", isCarried, err)
	}
	if !strings.Contains(err.Error(), "nothing moved, and the daemon was stopped and runs again on "+s.old) {
		t.Errorf("err = %v, want it to say the daemon was stopped and runs again", err)
	}
	if want := slices.Repeat([]string{"no-mistakes daemon stop", "no-mistakes daemon start"}, 2); !slices.Equal(s.calls(t), want) {
		t.Errorf("no-mistakes calls = %q, want %q", s.calls(t), want)
	}
	if strings.Count(out.String(), "gate home: waiting for the gate runs in flight to finish before it moves") != 2 {
		t.Errorf("out = %q, want it to say it waits at each try", out.String())
	}
	if len(tasks.calls) != 0 || len(tasks.tasks) != 1 {
		t.Errorf("schtasks calls = %q with %d task(s) left, want the old home's task untouched", tasks.calls, len(tasks.tasks))
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
	stopErr := daemon.Stop(context.Background(), old)
	startErr := daemon.Start(context.Background(), target)

	// Assert
	if stopErr != nil || startErr != nil {
		t.Fatalf("stop %v, start %v", stopErr, startErr)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("requests = %+v, want a stop and a start", runner.requests)
	}
	for index, want := range []struct{ args, root string }{{"daemon stop", old}, {"daemon start", target}} {
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
