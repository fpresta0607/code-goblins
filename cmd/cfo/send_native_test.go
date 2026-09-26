package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// cfo send reaches a native task through its own terminal, by its id or as
// gb-<id>: a key goes straight into the terminal, and text goes through the
// native delivery, which names the task whose terminal it cannot find. It used
// to refuse every native task as not a Herdr task, and it asks Herdr nothing.
func TestSendReachesANativeTaskThroughItsTerminal(t *testing.T) {
	// No herdr on the path: a Herdr request fails here rather than reach a
	// live server.
	t.Setenv("PATH", t.TempDir())
	stateDir := t.TempDir()
	h := home.Home{Root: filepath.Dir(stateDir), State: stateDir}
	for _, id := range []string{"t1", "t2"} {
		if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: id, Window: "native", Worktree: t.TempDir(), Harness: "codex", Kind: "ship", Backend: "native"}); err != nil {
			t.Fatal(err)
		}
	}
	hostAttachTestTerminal(t, stateDir, "t1")
	record, err := host.ReadRecord(stateDir, "t1")
	if err != nil {
		t.Fatal(err)
	}
	client, err := host.Dial(record)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Input([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	runtime := defaultCommandRuntime()

	keyErr := runtime.sendKey(context.Background(), h, "gb-t1", "Enter")
	textErr := runtime.sendText(context.Background(), h, "t2", "run the tests")

	if keyErr != nil {
		t.Errorf("send gb-t1 --key Enter: %v", keyErr)
	}
	screen := ""
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && !strings.Contains(screen, "got hello"); time.Sleep(50 * time.Millisecond) {
		screen, _ = peekTerminal(context.Background(), h, "t1", 0)
	}
	if !strings.Contains(screen, "got hello") {
		t.Errorf("t1's screen = %q, want the typed line entered", screen)
	}
	if textErr == nil || !strings.Contains(textErr.Error(), "native task t2 has no running terminal") {
		t.Errorf("send t2 = %v, want the native delivery to name t2's missing terminal", textErr)
	}
}
