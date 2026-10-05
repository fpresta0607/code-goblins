package supervisor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestCompletionWakeRemainsDeliverableWhileTaskHeld(t *testing.T) {
	// Arrange: the terminal and transport are mocked; no provider is launched.
	service, directory, typed, _ := typedWakeCFO(t, "codex", idleCodexCFO, idleCodexCFO, []string{"Working"})
	hold := "blocked: Provider reserve hold"
	if err := state.AppendStatus(directory, "held-task", hold); err != nil {
		t.Fatal(err)
	}
	record, err := wake.Append(directory, "notify", "held-task", hold)
	if err != nil {
		t.Fatal(err)
	}
	if err := wake.AckThrough(directory, record.Seq); err != nil {
		t.Fatal(err)
	}
	completion, err := wake.Append(directory, "notify", "held-task", "done: PR https://github.com/o/r/pull/9")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	if err := service.wakeCFO(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Assert
	pending, err := wake.Pending(directory)
	if err != nil || len(pending) != 1 || pending[0].Seq != completion.Seq || pending[0].Detail != completion.Detail || len(*typed) != 1 || !strings.Contains((*typed)[0], "notify held-task") {
		t.Fatalf("hold suppressed a new completion or delivery ACK consumed it: wakes=%+v typed=%+v err=%v", pending, *typed, err)
	}
}
