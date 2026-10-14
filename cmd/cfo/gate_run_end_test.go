package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// runEndGoblin is the task whose terminal the tests of a run's end run in.
const runEndGoblin = "cg-run-end"

// The screens a goblin's terminal shows, as Codex 0.160 and pi 0.85 draw
// them: idle at an empty composer, and in a turn.
var (
	codexIdle    = []string{"• Ran go test ./...", "", "› Ask Codex to do anything", "", "  gpt-6-astra xhigh · ~\\cg-run-end"}
	codexWorking = []string{"• Working (2m 10s • esc to interrupt)", "", "› Ask Codex to do anything", "", "  gpt-6-astra xhigh · ~\\cg-run-end"}
	piIdle       = []string{"Done.", "────────────────────", "", "────────────────────", "0.8%/1.0M (auto)"}
	// As Claude Code 2.1.288 draws them.
	claudeIdle    = []string{"● Ran go test ./...", "", "────────────────────", "> ", "────────────────────", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}
	claudeWorking = []string{"✽ Reticulating… (2m 10s · esc to interrupt)", "", "────────────────────", "> ", "────────────────────", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}
)

// sentTexts keeps what a runtime's sendText was asked to type, by target,
// and what check said each time it was.
type sentTexts struct {
	mu     sync.Mutex
	texts  []string
	checks []string
	check  func() string
}

func (s *sentTexts) send(_ context.Context, _ home.Home, target, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.texts = append(s.texts, target+": "+text)
	if s.check != nil {
		s.checks = append(s.checks, s.check())
	}
	return nil
}

func (s *sentTexts) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.texts...)
}

// goblinTerminal records goblin runEndGoblin, running harnessName in a
// worktree of its own, and the proof its terminal was given, and returns the
// home, the worktree and the proof a process in that terminal carries.
func goblinTerminal(t *testing.T, harnessName string) (h home.Home, worktree, proof string) {
	t.Helper()
	h = testHome(t)
	worktree = filepath.Join(t.TempDir(), "cg-run-end")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.State, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: runEndGoblin, Harness: harnessName, Worktree: worktree, Project: worktree, Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	proof = "proof-of-" + runEndGoblin
	line := host.ProofSum(proof) + " " + time.Now().UTC().Format(time.RFC3339Nano) + "\n"
	if err := os.WriteFile(filepath.Join(h.State, "hosts", runEndGoblin+".proofs"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return h, worktree, proof
}

// showing is a runtime's screen reader for a terminal that shows screen.
func showing(screen []string) func(string, string) ([]string, error) {
	return func(string, string) ([]string, error) { return screen, nil }
}

// A run's end is told only to a goblin whose harness starts no turn when a
// background run ends, and that sits idle at its prompt, since one in a turn
// has read how a run it waited on ended. It is told only of its own run: one
// in its own terminal, proven by the proof that terminal gave it, and in one
// of its worktrees, where a gate it started never runs its steps.
func TestTellGoblinRunEndedTellsOnlyAnIdleGoblinOfItsOwnRun(t *testing.T) {
	cases := []struct {
		name        string
		harness     string
		screen      []string
		folder      string
		isProven    bool
		hasHostID   bool
		isDetached  bool
		isToldOfEnd bool
	}{
		{name: "codex goblin idle at its prompt", harness: "codex", screen: codexIdle, folder: "frontend", isProven: true, hasHostID: true, isToldOfEnd: true},
		{name: "pi goblin idle at its prompt", harness: "pi", screen: piIdle, folder: ".", isProven: true, hasHostID: true, isToldOfEnd: true},
		{name: "codex goblin in a turn", harness: "codex", screen: codexWorking, folder: "frontend", isProven: true, hasHostID: true},
		{name: "claude goblin, whose harness starts a turn itself", harness: "claude", screen: codexIdle, folder: ".", isProven: true, hasHostID: true},
		{name: "claude goblin idle after a detached run, which its harness never waited on", harness: "claude", screen: claudeIdle, folder: ".", isProven: true, hasHostID: true, isDetached: true, isToldOfEnd: true},
		{name: "claude goblin in a turn after a detached run", harness: "claude", screen: claudeWorking, folder: ".", isProven: true, hasHostID: true, isDetached: true},
		{name: "a gate step outside its worktrees", harness: "codex", screen: codexIdle, folder: "elsewhere", isProven: true, hasHostID: true},
		{name: "a folder beside the worktree named like it", harness: "codex", screen: codexIdle, folder: "../cg-run-end-2", isProven: true, hasHostID: true},
		{name: "a proof its terminal never gave", harness: "codex", screen: codexIdle, folder: ".", hasHostID: true},
		{name: "no native terminal", harness: "codex", screen: codexIdle, folder: ".", isProven: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			h, worktree, proof := goblinTerminal(t, c.harness)
			dir := filepath.Join(worktree, c.folder)
			if c.folder == "elsewhere" {
				dir = t.TempDir()
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			environment := map[string]string{host.ProofVariable: "another terminal's proof"}
			if c.isProven {
				environment[host.ProofVariable] = proof
			}
			if c.hasHostID {
				environment[host.IDVariable] = runEndGoblin
			}
			sent := &sentTexts{}
			runtime := testCommandRuntimeForHome(h)
			runtime.sendText, runtime.screen = sent.send, showing(c.screen)
			var stderr strings.Builder

			// Act
			tellGoblinRunEnded(context.Background(), runtime, func(name string) string { return environment[name] }, "cfo gate turn", dir, "npm test", c.isDetached, 3, 95*time.Second, &stderr)

			// Assert
			want := []string(nil)
			if c.isToldOfEnd {
				want = []string{runEndGoblin + ": Your run npm test ended with exit 3 after 1m35s; read its output and carry on from it."}
			}
			if got := sent.all(); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("typed %q; want %q", got, want)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q; want nothing", stderr.String())
			}
		})
	}
}

// The line names the run on one line and no longer than runEndWhatReach,
// whatever its command held.
func TestTellGoblinRunEndedNamesTheRunOnOneBoundedLine(t *testing.T) {
	// Arrange
	h, worktree, proof := goblinTerminal(t, "codex")
	sent := &sentTexts{}
	runtime := testCommandRuntimeForHome(h)
	runtime.sendText, runtime.screen = sent.send, showing(codexIdle)
	environment := map[string]string{host.IDVariable: runEndGoblin, host.ProofVariable: proof}
	what := "cmd.exe /c\r\nnpm test " + strings.Repeat("x", 300)

	// Act
	tellGoblinRunEnded(context.Background(), runtime, func(name string) string { return environment[name] }, "cfo gate turn", worktree, what, false, 0, time.Second, &strings.Builder{})

	// Assert
	want := runEndGoblin + ": Your run cmd.exe /c npm test " + strings.Repeat("x", runEndWhatReach-len("cmd.exe /c npm test ")) + "... ended with exit 0 after 1s; read its output and carry on from it."
	if got := sent.all(); len(got) != 1 || got[0] != want {
		t.Errorf("typed %q; want %q", got, want)
	}
}

// Text typed while the goblin turned busy reaches it at its next tool call,
// which is no failure; a terminal that does not take the text is said on
// stderr, and the run's own exit code is left as it was.
func TestTellGoblinRunEndedSaysOnlyADeliveryThatFailed(t *testing.T) {
	for _, c := range []struct {
		name    string
		sendErr error
		want    string
	}{
		{name: "queued for its next tool call", sendErr: fmt.Errorf("send: %w", fleet.ErrQueuedForToolCall)},
		{name: "terminal gone", sendErr: errors.New("send: native task cg-run-end has no running terminal"), want: "cfo gate prepush: cg-run-end could not be told that this run ended: send: native task cg-run-end has no running terminal\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			h, worktree, proof := goblinTerminal(t, "codex")
			runtime := testCommandRuntimeForHome(h)
			runtime.sendText = func(context.Context, home.Home, string, string) error { return c.sendErr }
			runtime.screen = showing(codexIdle)
			environment := map[string]string{host.IDVariable: runEndGoblin, host.ProofVariable: proof}
			var stderr strings.Builder

			// Act
			tellGoblinRunEnded(context.Background(), runtime, func(name string) string { return environment[name] }, "cfo gate prepush", worktree, "cfo gate prepush", false, 0, time.Minute, &stderr)

			// Assert
			if stderr.String() != c.want {
				t.Errorf("stderr = %q; want %q", stderr.String(), c.want)
			}
		})
	}
}

// heldTurns says which runs hold a turn in the line the test named.
func heldTurns(t *testing.T) func() string {
	t.Helper()
	store, err := verify.AdmissionDir()
	if err != nil {
		t.Fatal(err)
	}
	return func() string {
		holding, _, err := verify.Line(store)
		if err != nil {
			return err.Error()
		}
		var who []string
		for _, run := range holding {
			who = append(who, run.Who)
		}
		return strings.Join(who, "; ")
	}
}

// cfo gate turn, run in a Codex goblin's own terminal from its worktree,
// tells the goblin how its command ended once it has, with the turn given
// back first: a goblin that ended its turn while the run waited sits at its
// prompt otherwise (2026-10-13, 17 to 26 minutes each).
func TestGateTurnTellsItsIdleCodexGoblinThatItsCommandEnded(t *testing.T) {
	// Arrange
	turnLine(t)
	h, worktree, proof := goblinTerminal(t, "codex")
	t.Setenv(host.IDVariable, runEndGoblin)
	t.Setenv(host.ProofVariable, proof)
	t.Chdir(worktree)
	dir := commandFolder(t)
	sent := &sentTexts{check: heldTurns(t)}
	runtime := turnRuntime()
	runtime.resolveHome = func() (home.Home, error) { return h, nil }
	runtime.sendText, runtime.screen = sent.send, showing(codexIdle)
	var stdout, stderr lockedBuffer

	// Act
	exit := gateTurn(runtime, &stdout, &stderr, append([]string{"--as", "frontend suite"}, turnCommand(dir, "command", 4, false)...)...)

	// Assert
	got := sent.all()
	if exit != 4 || len(got) != 1 || !strings.HasPrefix(got[0], runEndGoblin+": Your run frontend suite ended with exit 4 after ") {
		t.Errorf("exit = %d, typed %q; want exit 4 and one line naming the run and its exit", exit, got)
	}
	if strings.Join(sent.checks, "") != "" {
		t.Errorf("the turn was held by %q as the goblin was told; want it given back first", sent.checks)
	}
}

// cfo gate prepush tells its Codex goblin how the checks ended once it has
// given back its turn, and so does one that ends before it took one, as a
// pick with nothing to run does.
func TestGatePrepushTellsItsIdleCodexGoblinThatItsChecksEnded(t *testing.T) {
	for _, c := range []struct {
		name    string
		changed map[string]string
	}{
		{name: "checks ran", changed: map[string]string{"frontend/src/RunCard.tsx": "changed\n"}},
		{name: "nothing picked", changed: map[string]string{"README.md": "changed\n"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			turnLine(t)
			repository := testStepModule(t,
				map[string]string{"config/verify.json": quiet, "frontend/node_modules/.keep": "", "frontend/src/RunCard.tsx": ""},
				c.changed)
			h, _, proof := goblinTerminal(t, "codex")
			if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: runEndGoblin, Harness: "codex", Worktree: repository, Project: repository, Backend: "native"}); err != nil {
				t.Fatal(err)
			}
			t.Setenv(host.IDVariable, runEndGoblin)
			t.Setenv(host.ProofVariable, proof)
			t.Chdir(repository)
			sent := &sentTexts{check: heldTurns(t)}
			var ran []string
			runtime := recorded(&ran)
			runtime.resolveHome = func() (home.Home, error) { return h, nil }
			runtime.sendText, runtime.screen = sent.send, showing(codexIdle)
			var stdout, stderr bytes.Buffer

			// Act
			exit := gatePrepushWith(runtime, &stdout, &stderr)

			// Assert
			got := sent.all()
			if exit != 0 || len(got) != 1 || !strings.HasPrefix(got[0], runEndGoblin+": Your run cfo gate prepush ended with exit 0 after ") {
				t.Errorf("exit = %d, typed %q; want exit 0 and one line naming the run\nstdout=%s", exit, got, stdout.String())
			}
			if strings.Join(sent.checks, "") != "" {
				t.Errorf("the turn was held by %q as the goblin was told; want it given back first", sent.checks)
			}
		})
	}
}

// --plan runs nothing, so it tells nothing.
func TestGatePrepushPlanTellsItsGoblinNothing(t *testing.T) {
	// Arrange
	turnLine(t)
	repository := testStepModule(t, map[string]string{"config/verify.json": quiet}, map[string]string{"README.md": "changed\n"})
	h, _, proof := goblinTerminal(t, "codex")
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: runEndGoblin, Harness: "codex", Worktree: repository, Project: repository, Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(host.IDVariable, runEndGoblin)
	t.Setenv(host.ProofVariable, proof)
	t.Chdir(repository)
	sent := &sentTexts{}
	var ran []string
	runtime := recorded(&ran)
	runtime.resolveHome = func() (home.Home, error) { return h, nil }
	runtime.sendText, runtime.screen = sent.send, showing(codexIdle)

	// Act
	exit := gatePrepushWith(runtime, &bytes.Buffer{}, &bytes.Buffer{}, "--plan")

	// Assert
	if got := sent.all(); exit != 0 || len(got) != 0 {
		t.Errorf("exit = %d, typed %q; want exit 0 and nothing typed", exit, got)
	}
}

// A run whose command could not start tells its goblin so, with the turn
// given back, as one that ran does: the goblin that left it in the
// background waits on it all the same.
func TestGateTurnTellsItsIdleCodexGoblinThatItsCommandDidNotStart(t *testing.T) {
	// Arrange
	turnLine(t)
	h, worktree, proof := goblinTerminal(t, "codex")
	t.Setenv(host.IDVariable, runEndGoblin)
	t.Setenv(host.ProofVariable, proof)
	t.Chdir(worktree)
	sent := &sentTexts{check: heldTurns(t)}
	runtime := turnRuntime()
	runtime.resolveHome = func() (home.Home, error) { return h, nil }
	runtime.sendText, runtime.screen = sent.send, showing(codexIdle)
	missing := filepath.Join(t.TempDir(), "no-such-program.exe")
	var stdout, stderr lockedBuffer

	// Act
	exit := gateTurn(runtime, &stdout, &stderr, "--as", "suite", "--", missing)

	// Assert
	got := sent.all()
	if exit != exitNotStarted || len(got) != 1 || !strings.HasPrefix(got[0], fmt.Sprintf("%s: Your run suite ended with exit %d after ", runEndGoblin, exitNotStarted)) {
		t.Errorf("exit = %d, typed %q; want exit %d and one line naming the run", exit, got, exitNotStarted)
	}
	if strings.Join(sent.checks, "") != "" {
		t.Errorf("the turn was held by %q as the goblin was told; want it given back first", sent.checks)
	}
}

// A refused wait on a gate tells its goblin so, as a run that did not start
// does: the goblin that left it in the background waits on it all the same.
func TestGateTurnTellsItsIdleCodexGoblinThatItsGateWaitWasRefused(t *testing.T) {
	// Arrange
	turnLine(t)
	h, worktree, proof := goblinTerminal(t, "codex")
	t.Setenv(host.IDVariable, runEndGoblin)
	t.Setenv(host.ProofVariable, proof)
	t.Chdir(worktree)
	sent := &sentTexts{}
	runtime := turnRuntime()
	runtime.resolveHome = func() (home.Home, error) { return h, nil }
	runtime.sendText, runtime.screen = sent.send, showing(codexIdle)
	missing := filepath.Join(t.TempDir(), "no-mistakes.exe")
	var stdout, stderr lockedBuffer

	// Act
	exit := gateTurn(runtime, &stdout, &stderr, "--as", "fix round", "--", missing, "axi", "respond", "--action", "fix")

	// Assert
	got := sent.all()
	if exit != exitNoTurn || len(got) != 1 || !strings.HasPrefix(got[0], fmt.Sprintf("%s: Your run fix round ended with exit %d after ", runEndGoblin, exitNoTurn)) {
		t.Errorf("exit = %d, typed %q; want exit %d and one line naming the run", exit, got, exitNoTurn)
	}
}

// An interrupted run tells its goblin nothing: whoever interrupted it knows.
func TestGateTurnInterruptedTellsItsGoblinNothing(t *testing.T) {
	// Arrange
	turnLine(t)
	h, worktree, proof := goblinTerminal(t, "codex")
	t.Setenv(host.IDVariable, runEndGoblin)
	t.Setenv(host.ProofVariable, proof)
	t.Chdir(worktree)
	dir := commandFolder(t)
	sent := &sentTexts{}
	signals := make(chan os.Signal, 1)
	runtime := turnRuntime()
	runtime.resolveHome = func() (home.Home, error) { return h, nil }
	runtime.sendText, runtime.screen = sent.send, showing(codexIdle)
	runtime.interrupts = func() (<-chan os.Signal, func()) { return signals, func() {} }
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)
	go func() { exited <- gateTurn(runtime, &stdout, &stderr, turnCommand(dir, "command", 9, true)...) }()
	eventually(t, "the run's command starts", func() bool { return left(dir, "command", "started") })

	// Act
	signals <- os.Interrupt
	time.Sleep(100 * time.Millisecond)
	letGo(t, dir, "command")

	// Assert
	select {
	case exit := <-exited:
		if got := sent.all(); exit != 9 || len(got) != 0 {
			t.Errorf("exit = %d, typed %q; want the command's 9 and nothing typed", exit, got)
		}
	case <-time.After(time.Minute):
		t.Fatalf("the run had not ended a minute after its command; stderr=%q", stderr.String())
	}
}
