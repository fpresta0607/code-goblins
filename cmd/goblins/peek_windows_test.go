package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/host/hosttest"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// goblins peek reads a native terminal's screen as its console holds it: the rows
// its program wrote, without the blanks after them, and the last ones when
// fewer are asked for.
func TestPeekReadsANativeTerminalsScreen(t *testing.T) {
	stateDir := t.TempDir()
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
	if err := client.Input([]byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	for shown := ""; !strings.Contains(shown, "got hello"); {
		event, err := client.Next()
		if err != nil || event.Exited {
			t.Fatalf("the terminal ended before it answered: %+v, %v", event, err)
		}
		shown += string(event.Output)
	}
	h := home.Home{Root: filepath.Dir(stateDir), State: stateDir}

	screen, err := peekTerminal(h, "t1", 0)
	last, lastErr := peekTerminal(h, "t1", 1)

	if err != nil || screen != "ready\nhello\ngot hello\n" {
		t.Errorf("peek t1 = %q, %v; want the three rows written", screen, err)
	}
	if lastErr != nil || last != "got hello\n" {
		t.Errorf("peek t1 1 = %q, %v; want the last row written", last, lastErr)
	}
}

func TestPeekAndReadinessReadAnInlineCodexScreen(t *testing.T) {
	stateDir := t.TempDir()
	hostAttachTestTerminal(t, stateDir, "t1")
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "t1", Worktree: t.TempDir(), Harness: "codex", Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	record, err := host.ReadRecord(stateDir, "t1")
	if err != nil {
		t.Fatal(err)
	}
	client, err := host.Dial(record)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if err := client.Input([]byte("codex-inline\r")); err != nil {
		t.Fatal(err)
	}
	screens, ok := harness.NativeScreens(harness.Codex)
	if !ok {
		t.Fatal("Codex has no native readiness detector")
	}
	readAttempts := 0
	readScreen := func(record host.Record) ([]string, error) {
		readAttempts++
		if readAttempts == 1 {
			return nil, errors.New("forced transient console-attach error")
		}
		return host.ReadScreen(record)
	}
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(50 * time.Millisecond) {
		rows, err := readScreen(record)
		if err == nil && screens.IsReady(rows) && screens.ComposerEmpty(rows) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("inline Codex screen never became ready: %q; last read error: %v", rows, err)
		}
	}
	h := home.Home{Root: filepath.Dir(stateDir), State: stateDir}
	var stdout, stderr bytes.Buffer
	exit := runPeek([]string{"gb-t1"}, &stdout, &stderr, commandRuntime{
		resolveHome: func() (home.Home, error) { return h, nil },
		peek:        peekTerminal,
	})

	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("peek exit=%d stderr=%q", exit, stderr.String())
	}
	for _, text := range []string{"Working tree is clean.", "\u203a Ask Codex to do anything", "gpt-6.1-sol high"} {
		if !strings.Contains(stdout.String(), text) {
			t.Errorf("peek = %q, want inline Codex row %q", stdout.String(), text)
		}
	}
}

// goblins peek gb-<id>, the form fleet-view suggests, reads a native task's
// terminal as goblins peek <id> does, never asking Herdr for it.
func TestPeekByTheGoblinsNameReadsANativeTasksTerminal(t *testing.T) {
	stateDir := t.TempDir()
	hostAttachTestTerminal(t, stateDir, "t1")
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "t1", Window: "native", Worktree: t.TempDir(), Harness: "claude", Kind: "ship", Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	h := home.Home{Root: filepath.Dir(stateDir), State: stateDir}
	// The terminal's program prints ready once it has started; read it by id
	// until it has, so both names are asked of the same screen.
	byID := ""
	for deadline := time.Now().Add(time.Minute); !strings.Contains(byID, "ready"); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never showed ready: %q", byID)
		}
		byID, _ = peekTerminal(h, "t1", 0)
	}

	screen, err := peekTerminal(h, "gb-t1", 0)

	if err != nil || screen != byID {
		t.Errorf("peek gb-t1 = %q, %v; want the screen peek t1 reads, %q", screen, err, byID)
	}
}

// goblins peek of a stream session, which has no screen to read, shows what
// its lines say of it: whether Claude Code waits for a line or works a turn
// (#2374).
func TestPeekOfAStreamSessionShowsWhatItsLinesSay(t *testing.T) {
	for _, test := range []struct {
		name      string
		isWorking bool
		want      string
	}{
		{name: "waiting for a line", want: "idle\n"},
		{name: "in a turn", isWorking: true, want: "working\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			hosttest.RunStream(t, stateDir, "t1", test.isWorking)
			h := home.Home{Root: filepath.Dir(stateDir), State: stateDir}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runPeek([]string{"t1"}, &stdout, &stderr, commandRuntime{
				resolveHome: func() (home.Home, error) { return h, nil },
				peek:        peekTerminal,
			})

			// Assert
			if exit != 0 || stdout.String() != test.want || stderr.Len() != 0 {
				t.Errorf("peek t1 = exit %d, %q, stderr %q; want %q", exit, stdout.String(), stderr.String(), test.want)
			}
		})
	}
}
