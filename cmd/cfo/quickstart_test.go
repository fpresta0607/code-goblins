package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// Every goblins run ends on one screen: the CFO's home, the board's address
// to Ctrl+click, the CFO's terminal as the choice Enter accepts, and the
// board on B. By then the CFO runs, and no browser has been opened.
func TestGoblinsEndsOnOneScreenWithTheCFOsTerminalAsItsDefault(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	board, _, _ := liveBoard(context.Background(), f.home.State)
	choose := f.runtime.choose
	f.runtime.choose = func(output io.Writer, step onboarding.Step) (int, error) {
		if !slices.Equal(f.nativeStarts, []string{f.home.Root}) || len(f.opened) != 0 {
			t.Errorf("at the final screen: nativeStarts=%q opened=%q, want the CFO started in its home and no browser opened", f.nativeStarts, f.opened)
		}
		return choose(output, step)
	}

	// Act
	exit, _, stderr := f.launch("--native")

	// Assert
	if exit != 0 || len(f.screens) != 1 {
		t.Fatalf("exit=%d screens=%+v stderr=%q, want one final screen", exit, f.screens, stderr)
	}
	shown := f.screens[0]
	if want := "Your CFO is starting\nHome   " + f.home.Root + "\nBoard  " + board + "  (Ctrl+click opens it)"; shown.title != want {
		t.Errorf("the final screen says %q, want %q", shown.title, want)
	}
	if want := []onboarding.Choice{{Label: "Open the CFO terminal"}, {Label: "Open the board", Key: 'b'}}; !slices.Equal(shown.choices, want) || shown.selected != 0 {
		t.Errorf("the final screen offers %+v starting on %d, want %+v starting on the CFO's terminal", shown.choices, shown.selected, want)
	}
	if !slices.Equal(f.nativeAttached, []string{supervisor.NativeCFOTerminal}) || len(f.opened) != 0 {
		t.Errorf("after Enter: native terminals shown = %q, opened = %q; want the CFO's terminal and no browser", f.nativeAttached, f.opened)
	}
}

// What the watch of a new native CFO's startup dialogs found is said before
// the final screen, which then opens the CFO's terminal on Enter.
func TestGoblinsSaysWhatItAnsweredForANewCFOBeforeTheFinalScreen(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.settleNotes = []string{"Answered the workspace trust dialog in the CFO's terminal: Yes, I trust this folder."}
	said := ""
	f.runtime.choose = func(output io.Writer, step onboarding.Step) (int, error) {
		said = output.(*bytes.Buffer).String()
		return step.Selected, nil
	}

	// Act
	exit, _, stderr := f.launch("--native")

	// Assert
	if exit != 0 || !strings.Contains(said, "Answered the workspace trust dialog in the CFO's terminal: Yes, I trust this folder.\n") {
		t.Errorf("exit=%d stderr=%q; before the final screen goblins said %q, want the answered dialog", exit, stderr, said)
	}
}

// The board opens only when the person chooses it on the final screen, and
// then no terminal is shown here.
func TestGoblinsOpensTheBoardOnlyWhenItIsChosen(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.nativeCFO = supervisor.NativeCFOTerminal
	board, _, _ := liveBoard(context.Background(), f.home.State)
	f.answer = 1

	// Act
	exit, _, stderr := f.launch()

	// Assert
	if exit != 0 || !slices.Equal(f.opened, []string{board}) || len(f.nativeAttached) != 0 {
		t.Fatalf("exit=%d opened=%q nativeAttached=%q stderr=%q, want the board opened and no terminal shown", exit, f.opened, f.nativeAttached, stderr)
	}
	if !strings.HasPrefix(f.screens[0].title, "Your CFO is running\n") {
		t.Errorf("the final screen says %q, want a CFO that already ran said to be running", f.screens[0].title)
	}
}

// A final screen with no answer, as with no console to ask in, shows no
// terminal and opens no browser, and says why; the CFO it started keeps
// running.
func TestGoblinsAcceptsNothingWhenTheFinalScreenHasNoAnswer(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.runtime.choose = func(io.Writer, onboarding.Step) (int, error) {
		return 0, onboarding.ErrNoConsole
	}

	// Act
	exit, _, stderr := f.launch("--native")

	// Assert
	if exit != 1 || !strings.Contains(stderr, "run goblins in a terminal window") {
		t.Fatalf("exit=%d stderr=%q, want the missing console reported", exit, stderr)
	}
	if len(f.nativeStarts) != 1 || len(f.nativeAttached) != 0 || len(f.opened) != 0 {
		t.Errorf("nativeStarts=%q nativeAttached=%q opened=%q, want the CFO started and nothing shown or opened", f.nativeStarts, f.nativeAttached, f.opened)
	}
}

// Agent steps that end on no agent, as when the person cancels them or no
// console can ask them, start no CFO and show no final screen. The
// supervisor, started first, keeps running, and no browser opens.
func TestGoblinsStartsNoCFOWhenTheAgentStepsEndOnNoAgent(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.setupErr = onboarding.ErrCancelled

	// Act
	exit, stdout, stderr := f.launch()

	// Assert
	if exit != 1 || !strings.Contains(stderr, "setup was cancelled") {
		t.Fatalf("exit=%d stderr=%q, want the cancelled setup reported", exit, stderr)
	}
	if len(f.cfoStarts)+len(f.nativeStarts) != 0 || len(f.screens) != 0 || len(f.opened) != 0 {
		t.Errorf("cfoStarts=%q nativeStarts=%q screens=%+v opened=%q, want no CFO started and nothing shown or opened", f.cfoStarts, f.nativeStarts, f.screens, f.opened)
	}
	if !strings.Contains(stdout, "  board   ") {
		t.Errorf("stdout = %q, want the board's link printed before the agent steps", stdout)
	}
}

// goblins setup shows the choice of agent again, even while a CFO runs, which
// keeps the harness it runs; anything after setup is refused.
func TestGoblinsSetupRunsTheAgentStepsAgain(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.nativeCFO = supervisor.NativeCFOTerminal
	f.agent = "pi"

	// Act
	exit, stdout, stderr := f.launch("setup")
	extra, _, extraErr := f.launch("setup", "now")

	// Assert
	if exit != 0 || !slices.Equal(f.setups, []agentSetup{{chosen: "", rerun: true}}) || len(f.nativeStarts) != 0 {
		t.Fatalf("exit=%d setups=%+v nativeStarts=%q stderr=%q, want the agent steps rerun and no CFO started", exit, f.setups, f.nativeStarts, stderr)
	}
	if !strings.Contains(stdout, "CFO        already running in native terminal cfo\n               It keeps its agent; pi is the agent goblins starts the next CFO as.\n") {
		t.Errorf("stdout = %q, want it to say the running CFO keeps its agent", stdout)
	}
	if extra != 2 || !strings.Contains(extraErr, "usage: goblins setup") {
		t.Errorf("goblins setup now: exit=%d stderr=%q, want its usage", extra, extraErr)
	}
}

// A home with no usable home folder starts nothing.
func TestGoblinsStartsNothingWithoutAHome(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.runtime.resolveHome = func() (home.Home, error) { return home.Home{}, errors.New("CFO_HOME names no folder") }

	// Act
	exit, _, stderr := f.launch()

	// Assert
	if exit != 1 || !strings.Contains(stderr, "CFO_HOME names no folder") || len(f.setups) != 0 || len(f.nativeStarts) != 0 {
		t.Fatalf("exit=%d stderr=%q setups=%+v nativeStarts=%q, want the home's problem and nothing started", exit, stderr, f.setups, f.nativeStarts)
	}
}

// readyFlow is agent steps over agents that are all ready, which record the
// agent each run started from and whether it showed the choice.
type readyFlow struct {
	saved   []string
	chooses int
	choice  int
}

func (r *readyFlow) flow() onboarding.Flow {
	return onboarding.Flow{
		Detect: func(_ context.Context, id string) onboarding.Agent {
			return onboarding.Agent{ID: id, Name: id, State: onboarding.Ready}
		},
		Ask: func(step onboarding.Step) (int, error) {
			r.chooses++
			r.saved = append(r.saved, onboarding.Agents[step.Selected])
			return r.choice, nil
		},
		Done: func(string, string) {},
		Undo: func() {},
	}
}

// The agent the quick start ends on is remembered by the home: a later run
// starts from it without asking, --harness names another, and goblins setup
// starts its choice on the remembered one.
func TestTheHomeRemembersTheAgentTheQuickStartEndsOn(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	steps := &readyFlow{choice: 1}
	ctx := context.Background()

	// Act
	first, firstErr := rememberAgent(ctx, stateDir, "", false, steps.flow())
	later, laterErr := rememberAgent(ctx, stateDir, "", false, steps.flow())
	named, namedErr := rememberAgent(ctx, stateDir, "pi", false, steps.flow())
	steps.choice = 2
	rerun, rerunErr := rememberAgent(ctx, stateDir, "", true, steps.flow())

	// Assert
	if err := errors.Join(firstErr, laterErr, namedErr, rerunErr); err != nil {
		t.Fatal(err)
	}
	if first != "codex" || later != "codex" || named != "pi" || rerun != "pi" {
		t.Errorf("agents = %q, %q, %q, %q; want codex chosen, codex remembered, pi named, pi kept", first, later, named, rerun)
	}
	// The first run asks, starting on Claude Code; the later and the named
	// runs ask nothing; the rerun asks, starting on the remembered pi.
	if want := []string{"claude", "pi"}; steps.chooses != 2 || !slices.Equal(steps.saved, want) {
		t.Errorf("the choice was shown %d times starting on %q, want twice starting on %q", steps.chooses, steps.saved, want)
	}
	if harness, err := cfoHarness(stateDir); err != nil || harness != "pi" {
		t.Errorf("the home remembers %q, %v; want pi", harness, err)
	}
}

// Agent steps that end on no agent leave what the home remembers as it was,
// and a remembered name that is no agent is asked about, never started.
func TestCancelledAgentStepsLeaveTheRememberedAgentAlone(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	if err := os.WriteFile(cfoHarnessPath(stateDir), []byte("kimi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := 0
	steps := (&readyFlow{}).flow()
	steps.Ask = func(onboarding.Step) (int, error) {
		asked++
		return 0, onboarding.ErrCancelled
	}

	// Act
	agent, err := rememberAgent(context.Background(), stateDir, "", false, steps)

	// Assert
	if !errors.Is(err, onboarding.ErrCancelled) || agent != "" || asked != 1 {
		t.Fatalf("rememberAgent = %q, %v after %d choices; want it cancelled at the one choice", agent, err, asked)
	}
	if data, err := os.ReadFile(cfoHarnessPath(stateDir)); err != nil || string(data) != "kimi\n" {
		t.Errorf("the home remembers %q, %v; want it left as it was", data, err)
	}
}

// A CFO started in Herdr, which only a Claude Code one is, has nothing
// watching its startup dialogs, so goblins says what to choose there: Claude
// Code's trust dialog focuses No, which exits.
func TestGoblinsSaysWhatToChooseAtTheDialogOfACFOStartedInHerdr(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.agent = "claude"

	// Act
	exit, stdout, stderr := f.launch()

	// Assert
	if exit != 0 || !slices.Equal(f.cfoStarts, []string{f.home.Root}) {
		t.Fatalf("exit=%d cfoStarts=%q stderr=%q, want the CFO started in Herdr", exit, f.cfoStarts, stderr)
	}
	if want := "If Claude Code asks whether you trust this folder, its first choice, No, exits: move to Yes, I trust this folder, then press Enter.\n"; !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want the guidance %q", stdout, want)
	}
}

// A Codex or pi CFO starts in a native terminal whether or not --native is
// given: the supervisor wakes it by typing, which it can do only there.
func TestACFOWokenByTypingStartsInANativeTerminal(t *testing.T) {
	for _, agent := range []string{"codex", "pi"} {
		t.Run(agent, func(t *testing.T) {
			// Arrange
			f := newSessionFixture(t)
			f.agent = agent

			// Act
			exit, stdout, stderr := f.launch()

			// Assert
			if exit != 0 || len(f.cfoStarts) != 0 || !slices.Equal(f.nativeStarts, []string{f.home.Root}) || !slices.Equal(f.harnesses, []string{agent}) {
				t.Fatalf("exit=%d cfoStarts=%q nativeStarts=%q harnesses=%q stderr=%q, want one native start of %s in the home", exit, f.cfoStarts, f.nativeStarts, f.harnesses, stderr, agent)
			}
			if !strings.Contains(stdout, "is woken by one line typed into this terminal while it sits idle at an empty prompt, once its first prompt has run cfo register.") {
				t.Errorf("stdout = %q, want it to say how the CFO is woken", stdout)
			}
		})
	}
}

// goblins says first what it finds running and starts nothing beside it: a
// supervisor and a CFO already there are each one line, no agent step is
// asked, and the final screen says the CFO is running. A CFO it has to start
// is one line too, said once its startup dialogs are answered.
func TestGoblinsSaysWhatAlreadyRunsAndStartsNothingBesideIt(t *testing.T) {
	t.Setenv("WT_SESSION", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "")
	for _, c := range []struct {
		name      string
		nativeCFO string
		starts    int
		want      string
		heading   string
	}{
		{"both run", supervisor.NativeCFOTerminal, 0, "\n  \u221a Supervisor already running\n  \u221a CFO        already running in native terminal cfo\n", "Your CFO is running\n"},
		{"only the supervisor runs", "", 1, "\n  \u221a Supervisor already running\n  \u221a Agent      claude\n  . CFO        starting as Claude Code\n  \u221a CFO        started as Claude Code in %s, in native terminal cfo\n", "Your CFO is starting\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			f := newSessionFixture(t)
			f.nativeCFO = c.nativeCFO
			want := c.want
			if strings.Contains(want, "%s") {
				want = fmt.Sprintf(want, f.home.Root)
			}

			// Act
			exit, stdout, stderr := f.launch("--native")

			// Assert
			if exit != 0 || f.starts != 0 || len(f.nativeStarts) != c.starts || len(f.cfoStarts) != 0 {
				t.Fatalf("exit=%d supervisor starts=%d nativeStarts=%q cfoStarts=%q stderr=%q, want no supervisor started and %d CFO", exit, f.starts, f.nativeStarts, f.cfoStarts, stderr, c.starts)
			}
			if !strings.HasSuffix(stdout, want) {
				t.Errorf("stdout ends\n%q\nwant\n%q", stdout, want)
			}
			if len(f.screens) != 1 || !strings.HasPrefix(f.screens[0].title, c.heading) {
				t.Errorf("the final screen is %+v, want %q", f.screens, c.heading)
			}
		})
	}
}

// A supervisor goblins had to start is said to be started, not found.
func TestGoblinsSaysItStartedTheSupervisor(t *testing.T) {
	// Arrange
	t.Setenv("WT_SESSION", "a1b2")
	board := fakeBoard(t, busySnapshot)
	f := newLauncherFixture(t, func(h home.Home) (<-chan struct{}, error) {
		if err := writeBoardRecord(h.State, boardRecord{PID: fakeBoardPID, URL: board}); err != nil {
			t.Fatal(err)
		}
		return make(chan struct{}), nil
	})
	f.nativeCFO = supervisor.NativeCFOTerminal

	// Act
	exit, stdout, stderr := f.launch()

	// Assert
	if exit != 0 || f.starts != 1 || !strings.Contains(stdout, "\n  \u2713 Supervisor started\n  \u2713 CFO        already running in native terminal cfo\n") {
		t.Fatalf("exit=%d starts=%d stdout=%q stderr=%q, want the supervisor said to be started", exit, f.starts, stdout, stderr)
	}
}

// Escape on the final screen leaves goblins with the CFO and the board
// running, says so, and opens and shows nothing.
func TestEscapeOnTheFinalScreenLeavesTheCFOAndTheBoardRunning(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.runtime.choose = func(io.Writer, onboarding.Step) (int, error) {
		return 0, onboarding.ErrBack
	}

	// Act
	exit, stdout, stderr := f.launch("--native")

	// Assert
	if exit != 0 || stderr != "" || !strings.HasSuffix(stdout, "\nThe CFO and the board keep running; run goblins to see them again.\n") {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want a clean exit saying the CFO and the board keep running", exit, stdout, stderr)
	}
	if len(f.nativeStarts) != 1 || len(f.nativeAttached)+len(f.attached)+len(f.opened) != 0 {
		t.Errorf("nativeStarts=%q nativeAttached=%q attached=%q opened=%q, want the CFO started and nothing shown or opened", f.nativeStarts, f.nativeAttached, f.attached, f.opened)
	}
}

// Escape at the first choice of agent is reported in the quick start's own
// words, with nothing internal before them.
func TestGoblinsReportsEscapeAtTheChoiceAsACancel(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.setupErr = onboarding.ErrBack

	// Act
	exit, _, stderr := f.launch()

	// Assert
	if exit != 1 || stderr != "goblins: setup was cancelled; run goblins to continue\n" {
		t.Errorf("exit=%d stderr=%q, want the cancel reported in the quick start's words", exit, stderr)
	}
}
