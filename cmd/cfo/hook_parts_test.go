package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// postToolUse is a goblin's PostToolUse payload: a Read whose output held
// text, as Claude Code hands its hooks.
func postToolUse(t *testing.T, output string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"hook_event_name": "PostToolUse", "session_id": "s1", "cwd": t.TempDir(), "tool_name": "Read", "tool_use_id": "toolu_1", "tool_response": map[string]any{"file": map[string]string{"content": output}}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// After each tool call a goblin ran the native hook, which reports its
// session to the supervisor, and the key alarm, which wakes the CFO when the
// output held a key, as two hook entries, two cfo.exe processes. The one
// entry that runs both does what each did: the session's event is spooled
// with no part of the payload, the CFO is woken once naming the kind of key
// and never the key, and the reply Claude Code reads is the native hook's.
func TestTheMergedPostToolHookDoesWhatTheNativeHookAndTheKeyAlarmDid(t *testing.T) {
	// Arrange
	stateDir := goblinHookHome(t)
	t.Setenv("CFO_SPAWN_GEN", "g1")
	root := filepath.Dir(stateDir)
	key := keyShaped("sk" + "_test_")
	var stdout, stderr bytes.Buffer

	// Act
	exit := runNativeHook([]string{"claude", "--home", root, "--state", stateDir, "--key-alarm"}, bytes.NewReader(postToolUse(t, "STRIPE_TEST_SECRET_KEY="+key)), &stdout, &stderr, commandRuntime{})

	// Assert
	if exit != 0 || strings.TrimSpace(stdout.String()) != "{}" {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q, want the native hook's reply alone", exit, stdout.String(), stderr.String())
	}
	files, err := os.ReadDir(nativehook.SpoolDir(stateDir))
	if err != nil || len(files) != 1 {
		t.Fatalf("spooled %v, %v, want the tool call's one event", files, err)
	}
	spooled, err := os.ReadFile(filepath.Join(nativehook.SpoolDir(stateDir), files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var event nativehook.Event
	if err := json.Unmarshal(spooled, &event); err != nil || event.TaskID != "pd-keys" || event.Generation != "g1" || bytes.Contains(spooled, []byte(key)) {
		t.Fatalf("spooled %s, %v, want pd-keys's event without the tool's output", spooled, err)
	}
	pending, err := wake.Pending(stateDir)
	if err != nil || len(pending) != 1 || !strings.Contains(pending[0].Detail, "pd-keys's Read output held") || strings.Contains(pending[0].Detail, key) {
		t.Fatalf("wakes = %+v, %v, want one key alarm that never repeats the key", pending, err)
	}
}

// Each part of a merged hook entry keeps the limit it had as an entry of its
// own: one past its limit is left and reported, while one with a longer
// limit still runs to its end, and what a finished part wrote reaches
// Claude Code in order.
func TestEachPartOfAMergedHookKeepsItsOwnTimeLimit(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	tests := []struct {
		name       string
		parts      []hookPart
		wantExit   int
		wantStdout string
		wantStderr string
	}{
		{
			name: "a part past its limit",
			parts: []hookPart{
				{name: "the native hook", limit: 50 * time.Millisecond, run: func(io.Writer, io.Writer) int { <-release; return 0 }},
				{name: "the key alarm", limit: time.Minute, run: func(stdout, _ io.Writer) int { io.WriteString(stdout, "alarm"); return 0 }},
			},
			wantExit: 1, wantStdout: "alarm", wantStderr: "the native hook did not finish within 50ms\n",
		},
		{
			name: "a part that failed",
			parts: []hookPart{
				{name: "the native hook", limit: time.Minute, run: func(_, stderr io.Writer) int { io.WriteString(stderr, "spool failed\n"); return 1 }},
				{name: "the key alarm", limit: time.Minute, run: func(io.Writer, io.Writer) int { return 0 }},
			},
			wantExit: 1, wantStderr: "spool failed\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			var stdout, stderr bytes.Buffer

			// Act
			exit := runHookParts(&stdout, &stderr, test.parts...)

			// Assert
			if exit != test.wantExit || stdout.String() != test.wantStdout || stderr.String() != test.wantStderr {
				t.Fatalf("exit %d, stdout %q, stderr %q; want %d, %q, %q", exit, stdout.String(), stderr.String(), test.wantExit, test.wantStdout, test.wantStderr)
			}
		})
	}
}

// reported is stderr that says when anything was first written to it.
type reported struct {
	bytes.Buffer
	once    sync.Once
	written chan struct{}
}

func (r *reported) Write(data []byte) (int, error) {
	r.once.Do(func() { close(r.written) })
	return r.Buffer.Write(data)
}

// A part with a longer limit runs on past a shorter part's limit: the key
// alarm here finishes only once the native hook was reported unfinished at
// its own, and what the alarm wrote still reaches Claude Code.
func TestAPartRunsOnPastAnotherPartsShorterLimit(t *testing.T) {
	// Arrange
	release := make(chan struct{})
	defer close(release)
	var stdout bytes.Buffer
	stderr := &reported{written: make(chan struct{})}
	parts := []hookPart{
		{name: "the native hook", limit: 50 * time.Millisecond, run: func(io.Writer, io.Writer) int { <-release; return 0 }},
		{name: "the key alarm", limit: time.Minute, run: func(stdout, _ io.Writer) int {
			<-stderr.written
			io.WriteString(stdout, "alarm")
			return 0
		}},
	}

	// Act
	exit := runHookParts(&stdout, stderr, parts...)

	// Assert
	if exit != 1 || stdout.String() != "alarm" || stderr.String() != "the native hook did not finish within 50ms\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q; want the alarm's output after the native hook's limit", exit, stdout.String(), stderr.String())
	}
}

// The limits are those Claude Code holds each hook to as an entry of its
// own: the native hook the native hooks' limit, the key alarm Claude Code's
// default for a command hook.
func TestTheMergedHookKeepsEachHooksOwnLimit(t *testing.T) {
	if nativeHookLimit != nativehook.TimeoutSeconds*time.Second || keyAlarmLimit != 600*time.Second {
		t.Fatalf("limits = %s and %s, want %ds and 10m0s", nativeHookLimit, keyAlarmLimit, nativehook.TimeoutSeconds)
	}
}
