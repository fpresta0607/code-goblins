package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cutover"
	"github.com/fpresta0607/code-goblins/internal/cutover/cutovertest"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/update"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// runningPrograms is the machine's programs as a test names them.
type runningPrograms []cutover.Process

func (p runningPrograms) Running() ([]cutover.Process, error) { return p, nil }

// crossedHome is a test home that an update moved to goblins' names now,
// keeping one old program in bin for what still runs it.
func crossedHome(t *testing.T) (*Service, string) {
	t.Helper()
	store, h := testStore(t)
	bridge := filepath.Join(h.Root, "bin", "bridge.exe")
	writeFile(t, bridge, "this build")
	crossing := cutover.Crossing{Folder: filepath.Join(h.State, "backups", "cutover-1"), At: time.Now().UTC(), Bridges: []string{bridge}}
	if err := cutover.WriteCrossing(h.State, crossing); err != nil {
		t.Fatal(err)
	}
	return &Service{Store: store, Instance: "crossing", Options: Options{Programs: runningPrograms{}}}, bridge
}

// goblinFromBefore records a live native goblin whose terminal is this test
// process, which started before the home moved.
func goblinFromBefore(t *testing.T, stateDir, id, name string) {
	t.Helper()
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: id, GoblinName: name, Project: t.TempDir(), Worktree: t.TempDir(), Harness: "claude", Model: "opus", Effort: "xhigh", Mode: "direct-PR", Kind: "ship", Backend: "native", SpawnGen: "s1"}); err != nil {
		t.Fatal(err)
	}
	processes, err := fleettree.Processes()
	if err != nil {
		t.Fatal(err)
	}
	var self fleettree.Process
	for _, process := range processes {
		if process.PID == os.Getpid() {
			self = process
		}
	}
	record, err := json.Marshal(host.Record{ID: id, Pipe: `\\.\pipe\code-goblins-host-crossing-` + id, Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: os.Getpid(), ChildStart: self.Started, Started: self.Started})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(stateDir, "hosts", id+".json"), string(record))
}

// The board's notice and the CEO's wake name each terminal from before the
// move before any restarts: the first look restarts nothing, and a later one
// sets the goblin to restart under the new names at its turn's end. The old
// program stays while a terminal from before it runs.
func TestTheCrossingNamesTheTerminalsFromBeforeItBeforeItRestartsThem(t *testing.T) {
	// Arrange
	s, bridge := crossedHome(t)
	stateDir := s.Store.Home.State
	goblinFromBefore(t, stateDir, "cg-before", "Huey")

	// Act: the supervisor's first look.
	s.finishCrossing(t.Context(), false)

	// Assert
	view := s.crossingView()
	if view == nil || strings.Join(view.Waiting, ",") != "Huey" || !strings.Contains(view.Undo, "goblins cutover --undo ") || view.Problem != "" {
		t.Fatalf("the notice is %+v, want Huey waiting and the way back", view)
	}
	if _, err := state.ReadEngineChoice(stateDir, "cg-before"); err == nil {
		t.Fatal("the first look set the goblin to restart before the notice said so")
	}
	pending, err := wake.Pending(stateDir)
	if err != nil || len(pending) != 1 || pending[0].Key != "goblins" || !strings.Contains(pending[0].Detail, "Huey") {
		t.Fatalf("the CEO's wakes are %+v, %v; want one that names Huey", pending, err)
	}

	// Act: a later look.
	s.finishCrossing(t.Context(), true)

	// Assert
	choice, err := state.ReadEngineChoice(stateDir, "cg-before")
	if err != nil || choice.When != "renamed" || choice.Generation != "s1" || choice.Harness != "claude" || choice.Model != "opus" || choice.Effort != "xhigh" {
		t.Fatalf("the goblin's restart is %+v, %v; want one under the new names on its own engine", choice, err)
	}
	if pending, err := wake.Pending(stateDir); err != nil || len(pending) != 1 {
		t.Fatalf("the CEO's wakes are %+v, %v; want the one wake only", pending, err)
	}
	if _, err := os.Stat(bridge); err != nil {
		t.Fatalf("the old program went while a terminal from before the move runs: %v", err)
	}
}

// The old programs a crossing kept go once nothing needs them: no terminal
// from before the move, no process running one, and no unfinished update of
// a release before the rename, whose last step runs one.
func TestTheCrossingRemovesTheOldProgramsOnlyOnceNothingNeedsThem(t *testing.T) {
	for name, testCase := range map[string]struct {
		isLocked  bool
		isRunning bool
		isKept    bool
	}{
		"nothing needs them":            {isKept: false},
		"an old update is unfinished":   {isLocked: true, isKept: true},
		"a process runs an old program": {isRunning: true, isKept: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s, bridge := crossedHome(t)
			stateDir := s.Store.Home.State
			if testCase.isLocked {
				if err := os.MkdirAll(update.Dir(stateDir), 0o700); err != nil {
					t.Fatal(err)
				}
				if _, err := lock.AcquireExclusiveNamed(update.Dir(stateDir), update.ReleaseLockName); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(update.Dir(stateDir), update.ReleaseLockName) })
			}
			if testCase.isRunning {
				s.Options.Programs = runningPrograms{{PID: 4242, Image: strings.ToUpper(bridge)}}
			}

			// Act
			s.finishCrossing(t.Context(), true)

			// Assert
			_, err := os.Stat(bridge)
			if isKept := err == nil; isKept != testCase.isKept {
				t.Fatalf("the old program is kept: %v, want %v", isKept, testCase.isKept)
			}
			crossing, _, err := cutover.ReadCrossing(stateDir)
			if err != nil || (len(crossing.Bridges) > 0) != testCase.isKept {
				t.Fatalf("the record's old programs are %v, %v", crossing.Bridges, err)
			}
			if view := s.crossingView(); view == nil || len(view.Waiting) != 0 || view.Problem != "" {
				t.Fatalf("the notice is %+v, want nothing waiting and no problem", view)
			}
		})
	}
}

// The GitHub labels are renamed once per supervisor, and when that fails the
// notice says so and the record keeps the reason for the next start.
func TestTheCrossingRelabelsOncePerSupervisorAndSaysWhenItFailed(t *testing.T) {
	// Arrange
	s, _ := crossedHome(t)
	calls := 0
	s.Options.Relabel = func(string) error {
		calls++
		return errors.New("offline")
	}

	// Act
	s.finishCrossing(t.Context(), false)
	s.finishCrossing(t.Context(), true)

	// Assert
	if calls != 1 {
		t.Fatalf("the labels were renamed %d times, want once", calls)
	}
	if view := s.crossingView(); view == nil || !strings.Contains(view.Problem, "The GitHub labels were not renamed yet (offline)") {
		t.Fatalf("the notice is %+v, want it to say the labels were not renamed", view)
	}
	crossing, _, err := cutover.ReadCrossing(s.Store.Home.State)
	if err != nil || crossing.IsRelabelled || crossing.Relabelling != "offline" {
		t.Fatalf("the record is %+v, %v", crossing, err)
	}
}

// The supervisor's own loop restarts nothing on its first look, so the
// board's notice and the CEO's wake say what restarts before anything does,
// and on its next look sets each terminal from before the move to restart.
func TestTheCrossingLoopRestartsNothingOnItsFirstLook(t *testing.T) {
	// Arrange
	s, _ := crossedHome(t)
	stateDir := s.Store.Home.State
	goblinFromBefore(t, stateDir, "cg-before", "Huey")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	t.Cleanup(func() { cancel(); <-done })
	const every = 2 * time.Second

	// Act
	go func() {
		defer close(done)
		s.keepCrossing(ctx, every)
	}()

	// Assert: the first look names Huey and sets nothing to restart.
	for deadline := time.Now().Add(30 * time.Second); s.crossingView() == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the loop's first look made no notice")
		}
	}
	if view := s.crossingView(); strings.Join(view.Waiting, ",") != "Huey" {
		t.Fatalf("the notice is %+v, want Huey waiting", view)
	}
	if _, err := state.ReadEngineChoice(stateDir, "cg-before"); err == nil {
		t.Fatal("the loop's first look set the goblin to restart before the notice said so")
	}
	// Assert: a later look sets it to restart under the new names.
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if choice, err := state.ReadEngineChoice(stateDir, "cg-before"); err == nil {
			if choice.When != "renamed" {
				t.Fatalf("the goblin's restart is %+v, want one under the new names", choice)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no later look set the goblin to restart under the new names")
		}
	}
}

// A board from before the move typed a wake line into the CEO's terminal that
// its harness never took, and no board of this build types over a prompt
// line that holds words, so nothing else would ever take it off: the
// crossing takes it back and restarts the CEO under the new names, whose
// restart tells it to drain the wakes. Words of his beside it keep the
// prompt line his, and the CEO waits.
func TestTheCrossingTakesBackAWakeLineABoardFromBeforeItLeftUnsent(t *testing.T) {
	cases := []struct {
		name, typed, shown string
		isRestarted        bool
	}{
		{name: "only the old wake line", typed: cutovertest.WakeLine, shown: "queued wake", isRestarted: true},
		{name: "his words beside it", typed: "my words " + cutovertest.WakeLine, shown: "my words", isRestarted: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, "ready")
			typeIntoCEO(t, home.State, test.typed, test.shown)
			if err := cutover.WriteCrossing(home.State, cutover.Crossing{Folder: filepath.Join(home.State, "backups", "cutover-1"), At: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			machine := newFirstRunMachine(t)
			machine.runs = true
			s := &Service{Store: store, Instance: "crossing", Options: Options{Programs: runningPrograms{}, FirstRun: machine.run, CEO: &CEOConnection{State: home.State}}}

			// Act
			s.finishCrossing(t.Context(), true)

			// Assert
			if isRestarted := machine.restarted == 1; isRestarted != test.isRestarted {
				t.Errorf("the CEO was restarted %d times, want restarted %v; the notice is %+v", machine.restarted, test.isRestarted, s.crossingView())
			}
			if isCleared := slices.Contains(terminal.lines(t), "cleared"); isCleared != test.isRestarted {
				t.Errorf("terminal input = %q; want the prompt line cleared %v", terminal.lines(t), test.isRestarted)
			}
		})
	}
}
