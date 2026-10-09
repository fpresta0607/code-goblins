package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// installGate answers provisioning's git questions as the fixture's runner
// does and holds an npm install until finish closes, closing started when it
// begins one.
type installGate struct {
	started chan struct{}
	finish  chan struct{}
	once    sync.Once
}

func (g *installGate) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	switch {
	case req.Name == "git" && len(req.Args) > 0 && req.Args[0] == "check-ignore":
		return execx.Result{}, nil
	case req.Name == "git" && len(req.Args) > 0 && req.Args[0] == "ls-files":
		return execx.Result{ExitCode: 1}, nil
	case req.Name == "npm":
		g.once.Do(func() { close(g.started) })
		<-g.finish
		return execx.Result{}, nil
	}
	return execx.Result{}, fmt.Errorf("unexpected command: %#v", req)
}

// One start's dependency install holds up no other start. A spawn into a
// project with a package-lock.json ran npm ci under the home's spawn lock,
// which on 2026-10-07 held three starts behind one install for over 30
// minutes with 9.6 GB free.
func TestASpawnWhoseDependencyStepBlocksDoesNotBlockAnotherStart(t *testing.T) {
	// Arrange: the second start is a real native spawn; the first goes into
	// a project whose npm install never ends until the test lets it, and
	// stops at its terminal's launch, which comes after its dependency step.
	// Both run the fixture's stand-in codex, which a launch starts through
	// cmd without looking for any harness on PATH, so the first stops there
	// for want of a host command on a machine with no harness installed too.
	second := newQuickFixture(t)
	closeTerminalAtEnd(t, second.stateDir, second.request.ID)
	firstWorktree := makeDir(t, filepath.Join(filepath.Dir(second.worktree), "first-worktree"))
	writeFile(t, filepath.Join(firstWorktree, "package-lock.json"), "{}\n")
	gate := &installGate{started: make(chan struct{}), finish: make(chan struct{})}
	var firstEvents []string
	first := second.service
	first.Worktrees.Git = &worktreeGit{events: &firstEvents, top: firstWorktree}
	first.Worktrees.Commands = gate
	first.HostCommand = nil
	firstRequest := second.request
	firstRequest.ID = "task-8"
	firstDone := make(chan error, 1)
	go func() {
		_, err := first.Spawn(context.Background(), firstRequest)
		firstDone <- err
	}()
	var firstErr error
	isFirstDone := false
	select {
	case <-gate.started:
	case firstErr = <-firstDone:
		isFirstDone = true
	case <-time.After(2 * time.Minute):
		t.Fatal("the first start neither began its install nor ended")
	}
	t.Cleanup(func() {
		close(gate.finish)
		if !isFirstDone {
			<-firstDone
		}
	})

	// Act
	result, err := second.service.Spawn(context.Background(), second.request)

	// Assert
	if err != nil {
		t.Fatalf("second start = %v; want it started while the first start's dependency step runs", err)
	}
	if result.Meta.ID != second.request.ID || !hasTerminal(second.stateDir, second.request.ID) {
		t.Fatalf("second start = %+v; want its terminal running", result.Meta)
	}
	// The premise: the first start had a dependency step, which it left to
	// its goblin, and got past it to its terminal's launch.
	if !isFirstDone || firstErr == nil || !strings.Contains(firstErr.Error(), "the command that runs a native terminal's host is required") {
		t.Fatalf("first start ended %v with %v; want it past its dependency step and stopped at its terminal's launch", isFirstDone, firstErr)
	}
	status, statusErr := state.TailStatus(second.stateDir, firstRequest.ID, 5)
	if statusErr != nil || len(status) == 0 || !strings.Contains(strings.Join(status, "\n"), "working: installing its dependencies first: npm ci") {
		t.Fatalf("first start's status = %q, %v; want its npm ci left to its goblin", status, statusErr)
	}
}

// lineWriter hands each write to the test as one line.
type lineWriter struct {
	lines chan string
}

func (w lineWriter) Write(p []byte) (int, error) {
	w.lines <- string(p)
	return len(p), nil
}

// A start that finds another start's turn waits for it, says what it waits
// on, and starts once the turn ends.
func TestSpawnWaitsForAnotherStartsTurnAndSaysWhatItWaitsOn(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	closeTerminalAtEnd(t, f.stateDir, f.request.ID)
	if _, err := lock.AcquireExclusiveNamedFor(f.stateDir, spawnLockName, "the start of pp-open-work"); err != nil {
		t.Fatal(err)
	}
	release := sync.OnceValue(func() error { return lock.ReleaseExclusiveNamed(f.stateDir, spawnLockName) })
	t.Cleanup(func() { _ = release() })
	progress := lineWriter{lines: make(chan string, 4)}
	f.service.Progress = progress
	done := make(chan error, 1)

	// Act
	go func() {
		_, err := f.service.Spawn(context.Background(), f.request)
		done <- err
	}()

	// Assert
	select {
	case line := <-progress.lines:
		if !strings.HasPrefix(line, "spawn: the start of task-7 waits for its turn: the start of pp-open-work has held the home's spawn lock since ") {
			t.Fatalf("progress = %q; want what it waits on", line)
		}
	case err := <-done:
		t.Fatalf("Spawn ended with %v while another start held its turn", err)
	case <-time.After(time.Minute):
		t.Fatal("Spawn said nothing about what it waits on")
	}
	if _, err := state.ReadTaskMeta(f.stateDir, f.request.ID); !errors.Is(err, os.ErrNotExist) || hasTerminal(f.stateDir, f.request.ID) {
		t.Fatalf("task record %v, terminal %v before its turn; want neither", err, hasTerminal(f.stateDir, f.request.ID))
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Spawn after the other start's turn ended: %v", err)
	}
}

// A turn held past the wait is a stuck start: the one waiting on it stops
// and names it, and builds nothing.
func TestSpawnGivesUpOnATurnHeldPastItsWaitAndNamesTheStartHoldingIt(t *testing.T) {
	// Arrange
	f := newFixture(t)
	previous := launchTurnWait
	launchTurnWait = 2 * launchTurnPoll
	t.Cleanup(func() { launchTurnWait = previous })
	if _, err := lock.AcquireExclusiveNamedFor(f.stateDir, spawnLockName, "the start of pp-open-work"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(f.stateDir, spawnLockName) })

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if !errors.Is(err, lock.ErrHeld) || !strings.Contains(err.Error(), "spawn: the start of task-7 waited 1s for its turn and stopped: the start of pp-open-work has held the home's spawn lock since ") {
		t.Fatalf("Spawn = %v; want it stopped, naming the start that holds its turn", err)
	}
	if slices.Contains(f.events, "worktree-acquire") {
		t.Fatalf("events = %v; want no worktree cut without a turn", f.events)
	}
}

// A start's turn ends once its terminal's host runs: the harness's startup
// and the brief's delivery, which can take minutes, hold up no other start.
func TestSpawnEndsItsTurnOnceItsTerminalRuns(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	closeTerminalAtEnd(t, f.stateDir, f.request.ID)
	var turnAtFirstRead error
	isRead := false
	f.service.ReadScreen = func(record host.Record) ([]string, error) {
		if !isRead {
			isRead = true
			_, turnAtFirstRead = lock.ReadNamed(f.stateDir, spawnLockName)
		}
		return host.ReadScreen(record)
	}

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !isRead || !errors.Is(turnAtFirstRead, os.ErrNotExist) {
		t.Fatalf("spawn lock at the first screen read = %v (read %v); want the turn ended before the harness's startup", turnAtFirstRead, isRead)
	}
}

// A goblin installs its own dependencies as its first step, told so in its
// first instruction and shown so on its card; the spawn runs no installer.
func TestSpawnLeavesTheDependencyInstallToTheGoblinsFirstStep(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	closeTerminalAtEnd(t, f.stateDir, f.request.ID)
	writeFile(t, filepath.Join(f.worktree, "package-lock.json"), "{}\n")
	f.runner.installer = "npm"

	// Act
	result, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if slices.Contains(f.fixture.events, "install") {
		t.Fatalf("events = %v; want no installer run by the spawn", f.fixture.events)
	}
	submitted := named(f.events(t), "submitted")
	if len(submitted) != 1 || !strings.Contains(delivered(t, submitted[0].Text), "before you build or test anything, run \"npm ci\" in "+f.worktree+", stopping at the first that fails.") {
		t.Fatalf("submitted = %+v; want the goblin told to run npm ci in its worktree first", submitted)
	}
	if !strings.Contains(result.Output, "\ndependencies: the goblin installs them as its first step, in its own terminal: npm ci") {
		t.Errorf("output = %q; want the install named as the goblin's", result.Output)
	}
	status, err := state.TailStatus(f.stateDir, f.request.ID, 1)
	if err != nil || len(status) != 1 {
		t.Fatalf("status = %q, %v", status, err)
	}
	if _, event := state.SplitStatus(status[0]); event != "working: installing its dependencies first: npm ci" {
		t.Errorf("status = %q; want the card to show the dependency step", status[0])
	}
}

// endedGoblin is task-7 spawned in the fake codex, whose terminal then
// ended, as a pause or a reboot leaves it: what Resume and the comeback
// relaunch.
func endedGoblin(t *testing.T) *nativeFixture {
	t.Helper()
	f := newNativeFixture(t, harness.Codex, "turns")
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/exit", ResumeArgs: []string{"resume", "--last"}}}}}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	closeCurrentTerminal(t, f)
	first, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Close(f.stateDir, first, nativeCloseWait); err != nil {
		t.Fatal(err)
	}
	return f
}

// A relaunch that adds a running goblin, as Resume and the comeback make,
// is admitted in its turn and ends the turn once its terminal runs, so its
// harness's startup holds up no other start.
func TestARelaunchIsAdmittedInItsTurnAndEndsItOnceItsTerminalRuns(t *testing.T) {
	// Arrange
	f := endedGoblin(t)
	var admittedIn *lock.Info
	var admittedErr error
	admit := func() error {
		admittedIn, admittedErr = lock.ReadNamed(f.stateDir, spawnLockName)
		return nil
	}
	var turnAtFirstRead error
	isRead := false
	f.service.ReadScreen = func(record host.Record) ([]string, error) {
		if !isRead {
			isRead = true
			_, turnAtFirstRead = lock.ReadNamed(f.stateDir, spawnLockName)
		}
		return host.ReadScreen(record)
	}

	// Act
	result, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", ResumeSession: "owned-task-7-session", Admit: admit})

	// Assert
	if err != nil || !result.Resumed {
		t.Fatalf("Switch = %+v, %v; want the goblin resumed in place", result, err)
	}
	if admittedErr != nil || admittedIn.Purpose != "the relaunch of task-7" {
		t.Fatalf("admitted under %+v, %v; want admission in the relaunch's turn", admittedIn, admittedErr)
	}
	if !isRead || !errors.Is(turnAtFirstRead, os.ErrNotExist) {
		t.Fatalf("spawn lock at the first screen read = %v (read %v); want the turn ended before the harness's startup", turnAtFirstRead, isRead)
	}
}

// A relaunch the machine has no room for starts nothing and says it waits
// for room, which the comeback reads as a goblin to try again.
func TestARelaunchTheMachineHasNoRoomForStartsNothing(t *testing.T) {
	// Arrange
	f := endedGoblin(t)
	before, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	_, err = f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", ResumeSession: "owned-task-7-session", Admit: func() error {
		return errors.New("3.1 GB of memory is free")
	}})

	// Assert
	if !errors.Is(err, state.ErrNoRoom) || err.Error() != "waits for room: 3.1 GB of memory is free" {
		t.Fatalf("Switch = %v; want it waiting for room, with the reason", err)
	}
	if launches := named(f.events(t), "env"); len(launches) != 1 || nativeTerminalRuns(f.stateDir, "task-7") {
		t.Fatalf("launches = %d, running %v; want no relaunch", len(launches), nativeTerminalRuns(f.stateDir, "task-7"))
	}
	after, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil || after.SpawnGen != before.SpawnGen {
		t.Fatalf("generation %q, %v after the refusal; want %q unchanged", after.SpawnGen, err, before.SpawnGen)
	}
	if _, err := lock.ReadNamed(f.stateDir, spawnLockName); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("spawn lock after the refusal = %v; want the turn ended", err)
	}
}

// The adversary a relaunch's check under its turn answers: a start that
// published the task's record and holds the turn until its terminal runs.
// A relaunch that waited for that turn, as cfo goblins resume does for
// every recorded task whose terminal is not running, must leave the
// terminal the start launched alone.
func TestARelaunchThatWaitedForItsTurnLeavesATerminalAnotherStartLaunched(t *testing.T) {
	// Arrange
	f := endedGoblin(t)
	if _, err := lock.AcquireExclusiveNamedFor(f.stateDir, spawnLockName, "the start of task-7"); err != nil {
		t.Fatal(err)
	}
	release := sync.OnceValue(func() error { return lock.ReleaseExclusiveNamed(f.stateDir, spawnLockName) })
	t.Cleanup(func() { _ = release() })
	progress := lineWriter{lines: make(chan string, 4)}
	f.service.Progress = progress
	done := make(chan error, 1)
	go func() {
		_, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", ResumeSession: "owned-task-7-session", Admit: func() error { return nil }})
		done <- err
	}()
	select {
	case <-progress.lines:
	case err := <-done:
		t.Fatalf("Switch ended with %v before its turn", err)
	case <-time.After(time.Minute):
		t.Fatal("the relaunch never waited for its turn")
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	launch, err := nativeAdapter{kind: harness.Codex}.Build(harness.LaunchSpec{BriefPath: f.brief, TaskTmp: meta.TaskTmp, Scratch: meta.Scratch})
	if err != nil {
		t.Fatal(err)
	}
	launch.Dir = f.worktree
	nativeEnvironment(launch.Env, meta)
	started, err := f.service.launchNativeHost("task-7", harness.Codex, launch, f.userEnv, nil)
	if err != nil {
		t.Fatalf("the start's own launch: %v", err)
	}

	// Act
	if err := release(); err != nil {
		t.Fatal(err)
	}
	err = <-done

	// Assert
	if err == nil || !strings.Contains(err.Error(), "switch: task task-7 already runs; another start launched it while this relaunch waited for its turn") {
		t.Fatalf("Switch = %v; want it to leave the started terminal alone", err)
	}
	current, readErr := host.ReadRecord(f.stateDir, "task-7")
	if readErr != nil || current.HostPID != started.HostPID || !host.Running(current) {
		t.Fatalf("terminal %+v, %v; want the start's own, still running", current, readErr)
	}
	if after, err := state.ReadTaskMeta(f.stateDir, "task-7"); err != nil || after.SpawnGen != meta.SpawnGen {
		t.Fatalf("generation %q, %v; want the start's %q", after.SpawnGen, err, meta.SpawnGen)
	}
}
