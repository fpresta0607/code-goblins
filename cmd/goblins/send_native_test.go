package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// goblins send reaches a goblin only in a native terminal: a task an older build
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
