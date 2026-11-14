package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
	"github.com/fpresta0607/code-goblins/internal/gatehome"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/installbase"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// gateDaemonScratch is a scratch gate home with the tables the daemon's hold
// reads, a scratch Dev Drive folder, a scratch user environment, and a
// stand-in no-mistakes.exe first on PATH that records each call's arguments.
type gateDaemonScratch struct {
	old, devDrive, log, lock string
}

func newGateDaemonScratch(t *testing.T) gateDaemonScratch {
	t.Helper()
	root := t.TempDir()
	s := gateDaemonScratch{old: filepath.Join(root, "profile", ".no-mistakes"), devDrive: filepath.Join(root, "dev"), log: filepath.Join(root, "no-mistakes-calls.log"), lock: filepath.Join(root, gateHomeMoveLock)}
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
	s.answers(t)
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

// answers makes the stand-in no-mistakes do what rules say from now on, each
// call still recorded; with no rule every call exits 0.
func (s gateDaemonScratch) answers(t *testing.T, rules ...standin.Rule) {
	t.Helper()
	for _, variable := range standin.Env(s.log, rules...) {
		name, value, _ := strings.Cut(variable, "=")
		t.Setenv(name, value)
	}
}

// putRun puts a gate run with this status in the scratch home's database, in
// place of a run with the same id.
func (s gateDaemonScratch) putRun(t *testing.T, id, status string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(s.old, "state.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT OR REPLACE INTO runs VALUES (?, 'a1', 'main', ?, 2, ?)", id, status, filepath.Join(s.old, "worktrees", "a1", id)); err != nil {
		t.Fatal(err)
	}
}

// assertNothingCarried fails unless nothing was copied to the Dev Drive and
// the user's NM_HOME is unset.
func (s gateDaemonScratch) assertNothingCarried(t *testing.T) {
	t.Helper()
	if _, err := os.Lstat(gatehome.Target(s.devDrive)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("something was copied to the Dev Drive: %v", err)
	}
	if _, isSet, err := installbase.UserVariable(gatehome.Variable); err != nil || isSet {
		t.Errorf("user NM_HOME set %v (%v), want it unset", isSet, err)
	}
}

// noGateRunner is a machine no-mistakes cannot be started on, as where it is
// not on the PATH of a hidden process; every other program runs for real.
type noGateRunner struct{}

func (noGateRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "no-mistakes" {
		return execx.Result{}, &exec.Error{Name: request.Name, Err: exec.ErrNotFound}
	}
	return execx.OSRunner{}.Run(ctx, request)
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
	line, isCarried, err := carryGateHomeWith(context.Background(), tasks, s.old, s.devDrive, s.lock, 1, 0, &out)

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
	_, isCarried, err := carryGateHomeWith(context.Background(), tasks, s.old, s.devDrive, s.lock, 2, 0, &out)

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
	if strings.Count(out.String(), "gate home: waiting for the gate to rest before it moves") != 2 {
		t.Errorf("out = %q, want it to say it waits at each try", out.String())
	}
	if len(tasks.calls) != 0 || len(tasks.tasks) != 1 {
		t.Errorf("schtasks calls = %q with %d task(s) left, want the old home's task untouched", tasks.calls, len(tasks.tasks))
	}
	s.assertNothingCarried(t)
}

// The real carry looks for gate runs in flight in the old home's own database
// before it asks the daemon anything (#2525): with a run in an agent step, or
// one parked on an answer, it never asks no-mistakes to stop the daemon, names
// the run it waits for, and gives up as a busy gate whose daemon was never
// stopped.
func TestCarryGateHomeNeverAsksTheDaemonToStopWhileAGateRunIsInFlight(t *testing.T) {
	for _, status := range []string{"running", "awaiting_approval"} {
		t.Run(status, func(t *testing.T) {
			// Arrange
			s := newGateDaemonScratch(t)
			s.putRun(t, "r2", status)

			// Act
			var out bytes.Buffer
			_, isCarried, err := carryGateHomeWith(context.Background(), &gateTaskRunner{}, s.old, s.devDrive, s.lock, 1, 0, &out)

			// Assert
			if !errors.Is(err, gatehome.ErrGateBusy) || isCarried || !strings.Contains(err.Error(), "nothing moved, and the daemon was never stopped") {
				t.Fatalf("carry = %v, %v; want a busy gate whose daemon was never stopped", isCarried, err)
			}
			if calls := s.calls(t); len(calls) != 0 {
				t.Errorf("no-mistakes calls = %q, want none while a gate run is in flight", calls)
			}
			if !strings.Contains(out.String(), "it waits for 1 gate run in flight, r2 ") {
				t.Errorf("out = %q, want it to name the run it waits for", out.String())
			}
			s.assertNothingCarried(t)
		})
	}
}

// The real carry waits on no-mistakes' own refusal to stop the daemon, a
// daemon stop that ran and exited nonzero, as a busy gate. A no-mistakes that
// could not be run is no busy gate: the carry fails on it at once, so the
// board says why in its row and nothing runs the carry again for ever.
func TestCarryGateHomeWaitsOnARefusedStopAndFailsOnOneThatCouldNotRun(t *testing.T) {
	for _, test := range []struct {
		name      string
		commands  execx.Runner
		wantBusy  bool
		wantCalls []string
	}{
		{name: "no-mistakes refuses the stop", commands: &gateTaskRunner{}, wantBusy: true, wantCalls: []string{"no-mistakes daemon stop", "no-mistakes daemon stop"}},
		{name: "no-mistakes cannot be run", commands: noGateRunner{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s := newGateDaemonScratch(t)
			s.answers(t, standin.Rule{Args: "daemon stop", Stderr: "refusing daemon stop because 1 active pipeline runs are in progress\r\n", Exit: 1})

			// Act
			var out bytes.Buffer
			_, isCarried, err := carryGateHomeWith(context.Background(), test.commands, s.old, s.devDrive, s.lock, 2, 0, &out)

			// Assert
			if err == nil || isCarried || errors.Is(err, gatehome.ErrGateBusy) != test.wantBusy || !strings.Contains(err.Error(), "nothing moved, and the daemon was never stopped") {
				t.Fatalf("carry = %v, %v; want nothing moved, the daemon never stopped, and a busy gate %v", isCarried, err, test.wantBusy)
			}
			if !test.wantBusy && !errors.Is(err, exec.ErrNotFound) {
				t.Errorf("err = %v, want why no-mistakes could not be run", err)
			}
			if !slices.Equal(s.calls(t), test.wantCalls) {
				t.Errorf("no-mistakes calls = %q, want %q", s.calls(t), test.wantCalls)
			}
			if waits := strings.Count(out.String(), "gate home: waiting for the gate to rest before it moves"); waits != len(test.wantCalls) {
				t.Errorf("out = %q, want it to wait %d time(s)", out.String(), len(test.wantCalls))
			}
			s.assertNothingCarried(t)
		})
	}
}

// no-mistakes refuses to start a daemon that runs already, and the gate
// daemon takes that for started only while the home's daemon does run: a
// start refused with no daemon running is a daemon that did not start.
func TestGateDaemonTakesARefusedStartForStartedOnlyWhileTheDaemonRuns(t *testing.T) {
	for _, test := range []struct {
		name      string
		isRunning bool
	}{
		{name: "the daemon runs", isRunning: true},
		{name: "no daemon runs"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s := newGateDaemonScratch(t)
			s.answers(t, standin.Rule{Args: "daemon start", Stderr: "the daemon did not start\r\n", Exit: 1})
			if test.isRunning {
				if err := os.WriteFile(filepath.Join(s.old, "daemon.pid"), []byte(fmt.Sprintf(`{"pid":%d}`, os.Getpid())), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			err := gateDaemon{commands: execx.OSRunner{}}.Start(context.Background(), s.old)

			// Assert
			if test.isRunning && err != nil {
				t.Errorf("start = %v, want a daemon that runs already taken for started", err)
			}
			if !test.isRunning && (err == nil || !strings.Contains(err.Error(), "no-mistakes daemon start exited 1: the daemon did not start")) {
				t.Errorf("start = %v, want no-mistakes' refusal in its own words", err)
			}
			if want := []string{"no-mistakes daemon start"}; !slices.Equal(s.calls(t), want) {
				t.Errorf("no-mistakes calls = %q, want %q", s.calls(t), want)
			}
		})
	}
}

// The Dev Drive setting the board serves with counts the gate runs in flight
// in the database of the gate home it is asked about, a run that ended no
// longer among them, and reads the machine and where the gate's home is as
// the commands do.
func TestTheBoardsDevDriveSettingCountsTheGateRunsInFlightInTheGateHome(t *testing.T) {
	// Arrange
	s := newGateDaemonScratch(t)
	noCarry := func(string, string, string, ...string) (*exec.Cmd, error) {
		return nil, errors.New("this test carries nothing")
	}
	setting := boardDevDrive(home.Home{Root: s.devDrive, State: filepath.Join(s.devDrive, "state")}, runtimeOn(t, machineWith()), noCarry)
	s.putRun(t, "r2", "running")

	// Act
	inFlight, inFlightErr := setting.GateRuns(context.Background(), s.old)
	s.putRun(t, "r2", "completed")
	ended, endedErr := setting.GateRuns(context.Background(), s.old)
	gateHome, gateHomeErr := setting.GateHome()
	machine, machineErr := setting.Read(context.Background())

	// Assert
	if inFlightErr != nil || inFlight != 1 {
		t.Errorf("gate runs in flight = %d, %v; want the 1 running", inFlight, inFlightErr)
	}
	if endedErr != nil || ended != 0 {
		t.Errorf("gate runs in flight = %d, %v; want none once it completed", ended, endedErr)
	}
	if gateHomeErr != nil || gateHome != systemGateHome {
		t.Errorf("gate home = %q, %v; want the runtime's %s", gateHome, gateHomeErr, systemGateHome)
	}
	if machineErr != nil || machine.SystemDrive != "C:" {
		t.Errorf("machine = %+v, %v; want the runtime's", machine, machineErr)
	}
}

// A gate home no-mistakes has not made yet, or made without its database,
// holds no gate run: the board's setting counts none in it, and the real
// carry then places the home on the Dev Drive, or moves it there, rather than
// failing on a database that is not there for ever.
func TestTheBoardsCarryPlacesOrMovesAGateHomeWithNoDatabase(t *testing.T) {
	for _, test := range []struct {
		name       string
		isRootMade bool
		wantLine   string
		wantCalls  []string
	}{
		{name: "no home", wantLine: "placed: the gate's home is "},
		{name: "a home with no database", isRootMade: true, wantLine: "moved: the gate's home is ", wantCalls: []string{"no-mistakes daemon stop", "no-mistakes daemon start"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s := newGateDaemonScratch(t)
			if err := os.RemoveAll(s.old); err != nil {
				t.Fatal(err)
			}
			if test.isRootMade {
				if err := os.MkdirAll(s.old, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			noCarry := func(string, string, string, ...string) (*exec.Cmd, error) {
				return nil, errors.New("this test carries nothing")
			}
			setting := boardDevDrive(home.Home{Root: t.TempDir()}, runtimeOn(t, machineWith()), noCarry)
			target := gatehome.Target(s.devDrive)

			// Act
			runs, runsErr := setting.GateRuns(context.Background(), s.old)
			var out bytes.Buffer
			line, isCarried, err := carryGateHomeWith(context.Background(), &gateTaskRunner{}, s.old, s.devDrive, s.lock, 1, 0, &out)

			// Assert
			if runsErr != nil || runs != 0 {
				t.Errorf("gate runs in flight = %d, %v; want none", runs, runsErr)
			}
			if err != nil || !isCarried || !strings.HasPrefix(line, test.wantLine+target) {
				t.Fatalf("carry = %q, %v, %v; want %q%s", line, isCarried, err, test.wantLine, target)
			}
			if value, isSet, err := installbase.UserVariable(gatehome.Variable); err != nil || !isSet || value != target {
				t.Errorf("user NM_HOME = %q (set %v, %v), want %s", value, isSet, err, target)
			}
			if !slices.Equal(s.calls(t), test.wantCalls) {
				t.Errorf("no-mistakes calls = %q, want %q", s.calls(t), test.wantCalls)
			}
		})
	}
}

// The real carry takes the move's lock at the path it is given: while another
// move holds that lock through every try, it gives up as a busy gate that
// asked no-mistakes nothing, so a carry the board starts and a move from a
// terminal never run at once.
func TestCarryGateHomeWaitsOnTheMoveLockItIsGiven(t *testing.T) {
	// Arrange
	s := newGateDaemonScratch(t)
	held, err := os.OpenFile(s.lock, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := windows.LockFileEx(windows.Handle(held.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped)); err != nil {
		t.Fatal(err)
	}

	// Act
	var out bytes.Buffer
	_, isCarried, err := carryGateHomeWith(context.Background(), &gateTaskRunner{}, s.old, s.devDrive, s.lock, 1, 0, &out)

	// Assert
	if !errors.Is(err, gatehome.ErrGateBusy) || isCarried || !strings.Contains(err.Error(), "the daemon was never stopped") {
		t.Fatalf("carry = %v, %v; want a busy gate whose daemon was never stopped", isCarried, err)
	}
	if !strings.Contains(out.String(), "another move of the gate's home runs") {
		t.Errorf("out = %q, want it to wait on the other move", out.String())
	}
	if calls := s.calls(t); len(calls) != 0 {
		t.Errorf("no-mistakes calls = %q, want none while another move holds the lock", calls)
	}
	s.assertNothingCarried(t)
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
