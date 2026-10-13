package main

import (
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
)

// runEndGoblin is the task whose terminal the tests of a run's end run in.
const runEndGoblin = "cg-run-end"

// sentTexts keeps what a runtime's sendText was asked to type, by target.
type sentTexts struct {
	mu    sync.Mutex
	texts []string
}

func (s *sentTexts) send(_ context.Context, _ home.Home, target, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.texts = append(s.texts, target+": "+text)
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
	worktree = t.TempDir()
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

// A run ends unseen only by a goblin whose harness starts no turn when a
// background run ends, and the goblin is told only of its own run: one in its
// own terminal, proven by the proof that terminal gave it, and in one of its
// worktrees, where a gate it started never runs its steps.
func TestTellGoblinRunEndedTellsOnlyAGoblinThatWouldNotSeeItsOwnRunEnd(t *testing.T) {
	cases := []struct {
		name        string
		harness     string
		inWorktree  bool
		isProven    bool
		hasHostID   bool
		isToldOfEnd bool
	}{
		{name: "codex goblin's own run", harness: "codex", inWorktree: true, isProven: true, hasHostID: true, isToldOfEnd: true},
		{name: "pi goblin's own run", harness: "pi", inWorktree: true, isProven: true, hasHostID: true, isToldOfEnd: true},
		{name: "claude goblin, whose harness starts a turn itself", harness: "claude", inWorktree: true, isProven: true, hasHostID: true},
		{name: "a gate step outside its worktrees", harness: "codex", isProven: true, hasHostID: true},
		{name: "a proof its terminal never gave", harness: "codex", inWorktree: true, hasHostID: true},
		{name: "no native terminal", harness: "codex", inWorktree: true, isProven: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			h, worktree, proof := goblinTerminal(t, c.harness)
			dir := filepath.Join(worktree, "frontend")
			if !c.inWorktree {
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
			runtime.sendText = sent.send
			var stderr strings.Builder

			// Act
			tellGoblinRunEnded(runtime, func(name string) string { return environment[name] }, "cfo gate turn", dir, "npm test", 3, 95*time.Second, &stderr)

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

// Text typed while the goblin is in a turn reaches it at its next tool call,
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
			environment := map[string]string{host.IDVariable: runEndGoblin, host.ProofVariable: proof}
			var stderr strings.Builder

			// Act
			tellGoblinRunEnded(runtime, func(name string) string { return environment[name] }, "cfo gate prepush", worktree, "cfo gate prepush", 0, time.Minute, &stderr)

			// Assert
			if stderr.String() != c.want {
				t.Errorf("stderr = %q; want %q", stderr.String(), c.want)
			}
		})
	}
}

// cfo gate turn, run in a Codex goblin's own terminal from its worktree,
// tells the goblin how its command ended once it has, with the turn given
// back first: a goblin that ended its turn while the run waited sits at its
// prompt otherwise (2026-10-13, 17 to 26 minutes each).
func TestGateTurnTellsItsCodexGoblinThatItsCommandEnded(t *testing.T) {
	// Arrange
	turnLine(t)
	h, worktree, proof := goblinTerminal(t, "codex")
	t.Setenv(host.IDVariable, runEndGoblin)
	t.Setenv(host.ProofVariable, proof)
	t.Chdir(worktree)
	dir := commandFolder(t)
	sent := &sentTexts{}
	runtime := turnRuntime()
	runtime.resolveHome = func() (home.Home, error) { return h, nil }
	runtime.sendText = sent.send
	var stdout, stderr lockedBuffer

	// Act
	exit := gateTurn(runtime, &stdout, &stderr, append([]string{"--as", "frontend suite"}, turnCommand(dir, "command", 4, false)...)...)

	// Assert
	got := sent.all()
	if exit != 4 || len(got) != 1 || !strings.HasPrefix(got[0], runEndGoblin+": Your run frontend suite ended with exit 4 after ") {
		t.Errorf("exit = %d, typed %q; want exit 4 and one line naming the run and its exit", exit, got)
	}
}

// cfo gate prepush tells its Codex goblin how the checks ended, failed or
// passed, once it has given back its turn.
func TestGatePrepushTellsItsCodexGoblinThatItsChecksEnded(t *testing.T) {
	// Arrange
	turnLine(t)
	repository := testStepModule(t,
		map[string]string{"config/verify.json": quiet, "frontend/node_modules/.keep": "", "frontend/src/RunCard.tsx": ""},
		map[string]string{"frontend/src/RunCard.tsx": "changed\n"})
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
	runtime.sendText = sent.send
	var stdout, stderr lockedBuffer

	// Act
	exit := gatePrepushWith(runtime, &stdout, &stderr)

	// Assert
	got := sent.all()
	if exit != 0 || len(got) != 1 || !strings.HasPrefix(got[0], runEndGoblin+": Your run cfo gate prepush ended with exit 0 after ") {
		t.Errorf("exit = %d, typed %q; want exit 0 and one line naming the run\nstdout=%s", exit, got, stdout.String())
	}
}
