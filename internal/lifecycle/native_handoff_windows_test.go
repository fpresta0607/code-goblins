package lifecycle_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestNativePauseHandoffProgram(t *testing.T) {
	if len(os.Args) < 4 || os.Args[len(os.Args)-3] != "pause-handoff-fixture" {
		return
	}
	mode := os.Args[len(os.Args)-2]
	handoff := os.Args[len(os.Args)-1]
	if err := windows.SetConsoleMode(windows.Handle(os.Stdin.Fd()), windows.ENABLE_VIRTUAL_TERMINAL_INPUT); err != nil {
		t.Fatal(err)
	}
	ready := "\x1b[2J\x1b[HOpenAI Codex (fixture)\r\n\r\n\u203a Ask Codex to do anything\r\n\r\n  gpt-6.1-sol high\r\n"
	fmt.Print(ready)
	var typed strings.Builder
	keys := make([]byte, 4096)
	for {
		count, err := os.Stdin.Read(keys)
		if err != nil {
			return
		}
		if mode == "slow" && typed.Len() == 0 {
			time.Sleep(6 * time.Second)
		}
		for _, key := range keys[:count] {
			if key != '\r' {
				typed.WriteByte(key)
				continue
			}
			if strings.Contains(typed.String(), "Pause requested.") && mode == "text" {
				// What Claude goblins kept answering the request with: a turn
				// of text alone, and no tool call that writes the handoff.
				fmt.Print("\x1b[2J\x1b[H• Working (0s • esc to interrupt)\r\n")
				time.Sleep(time.Second)
				fmt.Print("\x1b[2J\x1b[H• No response requested.\r\n")
				typed.Reset()
				continue
			}
			if strings.Contains(typed.String(), "Pause requested.") {
				if mode == "slow" {
					fmt.Print("\x1b[2J\x1b[H\u2022 Working (0s \u2022 esc to interrupt)\r\n")
					time.Sleep(3 * time.Second)
				}
				if err := os.WriteFile(handoff+".partial", []byte("Continue the retained branch."), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(handoff+".partial", handoff); err != nil {
					t.Fatal(err)
				}
			}
			typed.Reset()
		}
		if typed.Len() > 0 {
			fmt.Print("\x1b[2J\x1b[H\u203a " + typed.String() + "\r\n")
		} else {
			fmt.Print(ready)
		}
	}
}

func TestPauseHarvestsANativeHandoff(t *testing.T) {
	for _, testCase := range []struct {
		name string
		mode string
	}{
		{name: "working screen missed", mode: "fast"},
		{name: "slow composer", mode: "slow"},
	} {
		t.Run(testCase.name, func(t *testing.T) { pauseNativeHandoff(t, testCase.mode) })
	}
}

func pauseNativeHandoff(t *testing.T, mode string) {
	t.Helper()
	stateDir, meta, handoff := startPauseFixture(t, mode)
	var deliveryError error
	// Delivery has the minute a pause gives it, of which the slow composer
	// takes 6 s.
	service := lifecycle.Service{StateDir: stateDir, Operations: lifecycle.Operations{
		Prepare: func(ctx context.Context, meta state.TaskMeta, handoff string) error {
			deliveryError = (spawn.Service{StateDir: stateDir}).SendNative(ctx, meta, lifecycle.PauseInstruction(handoff))
			return deliveryError
		},
		Stop:   func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) { return nil, nil },
		Notify: func(state.Lifecycle) error { return nil },
	}}

	record, err := service.Run(t.Context(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "native", Action: "pause", Reason: "overlord"})

	if err != nil {
		t.Fatal(err)
	}
	if (deliveryError == nil) != (mode == "slow") {
		t.Fatalf("native delivery in %s mode: %v", mode, deliveryError)
	}
	// The pause returns once the handoff's new name exists, which can be
	// while the task's rename still holds it; it is read as fleet programs
	// read, sharing deletion and waiting out the hold.
	if data, err := fsx.ReadFile(handoff); err != nil || string(data) != "Continue the retained branch." {
		t.Fatalf("the native task did not publish its final handoff: %q, %v", data, err)
	}
	if !record.HandoffSaved || record.Phase != "paused" || len(record.Problems) != 0 {
		t.Fatalf("pause discarded the published handoff after unconfirmed delivery %v: %+v", deliveryError, record)
	}
}

// On 2026-10-22 cg-local-ci-private-repos answered its pause's request with
// text alone, idle and waiting, as Claude goblins kept doing: no handoff
// appeared, the wait ran out, and its resume read nothing but its worktree
// (issue #1338). The pause writes the handoff of such a goblin from where
// its task stands before its stop, and the goblin is paused all the same.
func TestPauseWritesTheHandoffOfAGoblinThatAnswersWithTextAlone(t *testing.T) {
	// Arrange
	stateDir, meta, handoff := startPauseFixture(t, "text")
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	if err := state.AppendStatus(stateDir, meta.ID, "working: the suite passes; opening the pull request next"); err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("b", 40)
	var deliveryError error
	var atStop []byte
	stops := 0
	service := lifecycle.Service{StateDir: stateDir, PauseWait: time.Second, Operations: lifecycle.Operations{
		Prepare: func(ctx context.Context, meta state.TaskMeta, handoff string) error {
			deliveryError = (spawn.Service{StateDir: stateDir}).SendNative(ctx, meta, lifecycle.PauseInstruction(handoff))
			return deliveryError
		},
		Handoff: lifecycle.HandoffWriter{StateDir: stateDir, Commands: handoffCommands{branch: "fix/pause", head: head, pulls: "[]"}, Owner: func(int) string { return "" }}.Handoff,
		Stop: func(_ context.Context, _ state.TaskMeta, record *state.Lifecycle) ([]string, error) {
			stops++
			atStop, _ = fsx.ReadFile(record.Handoff)
			return []string{"terminal host"}, nil
		},
		Notify: func(state.Lifecycle) error { return nil },
	}}

	// Act
	record, err := service.Run(t.Context(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "native", Action: "pause", Reason: "overlord"})

	// Assert
	if err != nil || deliveryError != nil {
		t.Fatalf("pause = %v, with the request delivered %v", err, deliveryError)
	}
	if !record.HandoffSaved || record.Phase != "paused" || stops != 1 || !strings.Contains(strings.Join(record.Problems, "; "), "so the pause wrote one for it") {
		t.Fatalf("pause of a goblin that answered with text alone = %+v, stopped %d times", record, stops)
	}
	note, err := fsx.ReadFile(handoff)
	if err != nil || string(note) != string(atStop) {
		t.Fatalf("the handoff was not written before the stop: %q at the stop, %q after, %v", atStop, note, err)
	}
	for _, want := range []string{"the pause wrote this one for you", "- Branch: fix/pause\n", "- HEAD: " + head + "\n", "- Open pull requests: none from fix/pause\n", "working: the suite passes; opening the pull request next\n", "None held a turn or waited for one."} {
		if !strings.Contains(string(note), want) {
			t.Errorf("written handoff lacks %q:\n%s", want, note)
		}
	}
}

// startPauseFixture starts a native terminal running the scratch Codex
// goblin in mode and waits for its composer. The goblin answers the pause's
// request by publishing its handoff, after a slow composer in "slow" mode,
// and with text alone in "text" mode.
func startPauseFixture(t *testing.T, mode string) (stateDir string, meta state.TaskMeta, handoff string) {
	t.Helper()
	stateDir = t.TempDir()
	meta = state.TaskMeta{ID: "task", SpawnGen: "generation-1", Backend: "native", Harness: "codex", Worktree: stateDir, TaskTmp: stateDir}
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	handoff = filepath.Join(stateDir, "pause-native.md")
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() {
		ended <- host.Run(stateDir, host.Spec{ID: meta.ID, Args: []string{program, "-test.run=^TestNativePauseHandoffProgram$", "--", "pause-handoff-fixture", mode, handoff}, Dir: stateDir, Cols: 120, Rows: 30})
	}()
	t.Cleanup(func() {
		if record, err := host.ReadRecord(stateDir, meta.ID); err == nil {
			if client, err := host.Dial(record); err == nil {
				_ = client.CloseTerminal()
				_ = client.Close()
			}
		}
		select {
		case err := <-ended:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Minute):
			t.Error("the scratch terminal host did not end")
		}
	})
	screens, _ := harness.NativeScreens(harness.Codex)
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(20 * time.Millisecond) {
		if record, err := host.ReadRecord(stateDir, meta.ID); err == nil {
			if screen, err := host.ReadScreen(record); err == nil && screens.IsReady(screen) && screens.ComposerEmpty(screen) {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the scratch Codex composer was never ready")
		}
	}
	return stateDir, meta, handoff
}
