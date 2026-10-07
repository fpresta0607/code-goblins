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
	stateDir := t.TempDir()
	meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", Backend: "native", Harness: "codex", Worktree: stateDir, TaskTmp: stateDir}
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	handoff := filepath.Join(stateDir, "pause-native.md")
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
		case <-time.After(15 * time.Second):
			t.Error("the scratch terminal host did not end")
		}
	})
	screens, _ := harness.NativeScreens(harness.Codex)
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if record, err := host.ReadRecord(stateDir, meta.ID); err == nil {
			if screen, err := host.ReadScreen(record); err == nil && screens.IsReady(screen) && screens.ComposerEmpty(screen) {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the scratch Codex composer was never ready")
		}
	}
	var deliveryError error
	// Delivery has the minute a pause gives it: the slow composer takes 6 s
	// of it, and every look at the screen while it waits starts a process,
	// which a loaded machine can take seconds to start.
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
