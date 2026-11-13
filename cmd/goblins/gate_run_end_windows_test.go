package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/host/hosttest"
)

// showCodexScreen hosts native terminal id in the state at stateDir, running
// attachTestProgram, and returns once it shows the Codex screen named.
func showCodexScreen(t *testing.T, stateDir, id, name string) {
	t.Helper()
	hostAttachTestTerminal(t, stateDir, id)
	record, err := host.ReadRecord(stateDir, id)
	if err != nil {
		t.Fatal(err)
	}
	client, err := host.Dial(record)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Input([]byte(name + "\r")); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(50 * time.Millisecond) {
		rows, err := host.ReadScreen(record)
		shown := host.ScreenTail(rows, 0)
		if err == nil && !slices.ContainsFunc(codexScreens[name], func(row string) bool { return !strings.Contains(shown, strings.TrimSpace(row)) }) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never showed the %s screen: %v\n%s", name, err, shown)
		}
	}
}

// Whether a goblin sits idle is read from the host it runs in: a Codex
// terminal's screen, and the state a stream session's lines leave, where
// Claude Code waits for a line or works a turn (#2374). A run's end is told
// only to one that reads idle.
func TestTellGoblinRunEndedReadsWhetherItsGoblinIsIdleFromItsHost(t *testing.T) {
	cases := []struct {
		name        string
		harness     string
		screen      string
		isWorking   bool
		isDetached  bool
		isToldOfEnd bool
	}{
		{name: "codex terminal at its empty composer", harness: "codex", screen: "codex-idle", isToldOfEnd: true},
		{name: "codex terminal in a turn", harness: "codex", screen: "codex-working"},
		{name: "claude stream session waiting for a line", harness: "claude", isDetached: true, isToldOfEnd: true},
		{name: "claude stream session in a turn", harness: "claude", isWorking: true, isDetached: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			h, worktree, proof := goblinTerminal(t, c.harness)
			if c.screen != "" {
				showCodexScreen(t, h.State, runEndGoblin, c.screen)
			} else {
				hosttest.RunStream(t, h.State, runEndGoblin, c.isWorking)
			}
			environment := map[string]string{host.IDVariable: runEndGoblin, host.ProofVariable: proof}
			sent := &sentTexts{}
			runtime := testCommandRuntimeForHome(h)
			runtime.sendText, runtime.idle = sent.send, defaultCommandRuntime().idle
			var stderr strings.Builder

			// Act
			tellGoblinRunEnded(context.Background(), runtime, func(name string) string { return environment[name] }, "goblins gate turn", worktree, "npm test", c.isDetached, 3, 95*time.Second, &stderr)

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
