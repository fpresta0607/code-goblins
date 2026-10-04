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

// cfo send reaches a task through its own native terminal, by its id or as
// gb-<id>: a key goes straight into the terminal, and text goes through the
// native delivery, which names the task whose terminal it cannot find.
func TestSendReachesANativeTaskThroughItsTerminal(t *testing.T) {
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

	keyErr := runtime.sendKey(h, "gb-t1", "Enter")
	textErr := runtime.sendText(context.Background(), h, "t2", "run the tests")

	if keyErr != nil {
		t.Errorf("send gb-t1 --key Enter: %v", keyErr)
	}
	screen := ""
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && !strings.Contains(screen, "got hello"); time.Sleep(50 * time.Millisecond) {
		screen, _ = peekTerminal(h, "t1", 0)
	}
	if !strings.Contains(screen, "got hello") {
		t.Errorf("t1's screen = %q, want the typed line entered", screen)
	}
	if textErr == nil || !strings.Contains(textErr.Error(), "native task t2 has no running terminal") {
		t.Errorf("send t2 = %v, want the native delivery to name t2's missing terminal", textErr)
	}
}

// cfo send reaches a goblin only in a native terminal: a task an older build
// recorded in Herdr, a Herdr pane address and an unknown name are each
// refused by name, for text and for a key.
func TestSendRefusesWhatIsNotANativeTask(t *testing.T) {
	stateDir := t.TempDir()
	h := home.Home{Root: filepath.Dir(stateDir), State: stateDir}
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "t1", Worktree: t.TempDir(), Harness: "claude", Kind: "ship", Backend: "herdr", HerdrSession: "fleet", HerdrWorkspaceID: "ws", HerdrTabID: "tab-t1", HerdrPaneID: "pane-t1"}); err != nil {
		t.Fatal(err)
	}
	runtime := defaultCommandRuntime()

	for target, want := range map[string]string{
		"t1":            "task t1 was recorded in Herdr by an older build",
		"gb-t1":         "task t1 was recorded in Herdr by an older build",
		"fleet:pane-t1": `unknown task "fleet:pane-t1"`,
		"nobody":        `unknown task "nobody"`,
	} {
		textErr := runtime.sendText(context.Background(), h, target, "run the tests")
		keyErr := runtime.sendKey(h, target, "Enter")

		if textErr == nil || !strings.Contains(textErr.Error(), want) {
			t.Errorf("send %s = %v, want %q", target, textErr, want)
		}
		if keyErr == nil || !strings.Contains(keyErr.Error(), want) {
			t.Errorf("send %s --key Enter = %v, want %q", target, keyErr, want)
		}
	}
}
