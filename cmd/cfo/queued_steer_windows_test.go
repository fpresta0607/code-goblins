package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// queuedSender is a cfo send to a goblin in a turn: typed and submitted once,
// and queued by its harness for its next tool call.
func queuedSender(sent *[]string) func(context.Context, home.Home, string, string) error {
	return func(_ context.Context, _ home.Home, target, text string) error {
		*sent = append(*sent, target+": "+text)
		return fmt.Errorf("spawn: native terminal %s has the text submitted: %w", target, fleet.ErrQueuedForToolCall)
	}
}

// Every command that steers a goblin through cfo send counts a steer queued
// for the goblin's next tool call as delivered: it was typed once and lands
// there, so none of them reports a failure that would have it sent again.
func TestASteerQueuedForTheGoblinsNextToolCallIsDeliveredForEveryCommand(t *testing.T) {
	t.Run("cfo supersede", func(t *testing.T) {
		// Arrange
		h := testHome(t)
		taskTmp := filepath.Join(h.Root, "tasktmp", "task-1")
		for _, dir := range []string{taskTmp, h.State} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "task-1", TaskTmp: taskTmp, Harness: "claude", Backend: "native", SpawnGen: "g1"}); err != nil {
			t.Fatal(err)
		}
		runtime := testCommandRuntimeForHome(h)
		var sent []string
		runtime.sendText = queuedSender(&sent)
		var stdout, stderr bytes.Buffer

		// Act
		exit := runSupersede([]string{"task-1", "--reason", "main moved"}, &stdout, &stderr, runtime)

		// Assert
		if exit != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "next tool call") || len(sent) != 1 {
			t.Errorf("exit %d, stdout %q, stderr %q, sent %d; want the steer reported queued for the next tool call, sent once", exit, stdout.String(), stderr.String(), len(sent))
		}
	})
	t.Run("the credential refresh notice", func(t *testing.T) {
		// Arrange
		runtime := testCommandRuntime(t)
		var sent []string
		runtime.sendText = queuedSender(&sent)
		var stderr bytes.Buffer

		// Act
		told := deliverRefreshNotice(context.Background(), runtime, testHome(t), spawn.Refreshed{ID: "task-1", Path: `C:\work\auth.ps1`}, &stderr)

		// Assert
		if !told || stderr.Len() != 0 || len(sent) != 1 {
			t.Errorf("told %t, stderr %q, sent %d; want the queued notice counted as told, sent once", told, stderr.String(), len(sent))
		}
	})
	t.Run("the pause instruction", func(t *testing.T) {
		// Arrange
		runtime := testCommandRuntime(t)
		var sent []string
		runtime.sendText = queuedSender(&sent)

		// Act
		err := pauseInstruction(runtime, testHome(t))(context.Background(), state.TaskMeta{ID: "task-1"}, `C:\work\handoff.md`)

		// Assert
		if err != nil || len(sent) != 1 {
			t.Errorf("err %v, sent %d; want the queued pause instruction delivered once, so the pause waits for its handoff", err, len(sent))
		}
	})
}
