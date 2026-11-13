package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/host/hosttest"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// firstWrittenLine is the first line the board wrote into the stream session
// record names, as its host kept it.
func firstWrittenLine(t *testing.T, record host.Record) string {
	t.Helper()
	viewer, err := host.WatchStream(record)
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	written := make(chan string, 1)
	go func() {
		for {
			event, err := viewer.Next()
			if err != nil || event.IsExited {
				return
			}
			if event.Line.IsWritten {
				written <- string(event.Line.Line)
				return
			}
		}
	}()
	select {
	case line := <-written:
		return line
	case <-time.After(time.Minute):
		t.Fatal("the board wrote no line into the stream session")
		return ""
	}
}

// A view of a goblin on stream-json pipes is told so, that no history
// replays and that typing is held, so the board's box sends through the
// board; it is told the turn runs, and once it claims a size it is drawn the
// session's rows. Escape or Ctrl+C interrupts the turn even while a gate holds
// typing, since an interrupt writes nothing into the branch, and other typing
// writes nothing into the session. It closes with Claude Code's exit code.
func TestANativeViewOfAStreamGoblinDrawsItsSessionAndInterruptsItsTurnOnEscape(t *testing.T) {
	for _, mode := range []string{"direct", "no-mistakes"} {
		for _, key := range []struct{ name, typed string }{{"escape", "\x1b"}, {"ctrl-c", "\x03"}} {
			t.Run(mode+"-"+key.name, func(t *testing.T) {
				// Arrange
				h, server := nativeBoard(t, mode)
				if mode == "no-mistakes" {
					gitFixture(t, taskWorktree(t, h))
					h.Service.Options.Gate = fakeProgress{value: pipeline.Progress{Status: "running"}}
				}
				record := hosttest.RunStream(t, h.Service.Store.Home.State, "task-1", true)
				v := openNativeView(t, server, viewQuery)

				// Assert: what it shows, and that a turn runs.
				v.waitFor(t, `{"type":"stream"}{"type":"history","bytes":0}{"type":"held","reason":"This task runs on stream-json pipes, so the board sends its messages"}`)
				v.waitFor(t, `{"type":"screen","asking":false,"open":false,"working":true,"submission":""}`)

				// Act: a size, then a key it drops, then the interrupt key.
				v.send(t, websocket.MessageText, `{"type":"resize","cols":80,"rows":24}`)
				v.waitFor(t, `{"type":"size","cols":80,"rows":24}`+"\x1b[H\x1b[2J\x1b[3JThis task runs Claude Code on stream-json pipes. goblins switch task-1 --terminal takes it over in a terminal.\r\n\r\nworking")
				v.send(t, websocket.MessageBinary, "y")
				v.send(t, websocket.MessageBinary, key.typed)

				// Assert: the one line written is the interrupt.
				if line := firstWrittenLine(t, record); !strings.Contains(line, `"subtype":"interrupt"`) || !strings.Contains(line, `"type":"control_request"`) {
					t.Errorf("the first line written into the session is %s, want an interrupt", line)
				}

				// Act and Assert: Claude Code ends.
				if err := host.Close(h.Service.Store.Home.State, record, 15*time.Second); err != nil {
					t.Fatal(err)
				}
				closed := v.waitForClose(t)
				if closed.Code != websocket.StatusNormalClosure || closed.Reason != "Claude Code ended with exit code 0." {
					t.Errorf("the view closed with %d %q, want a normal close with exit code 0", closed.Code, closed.Reason)
				}
			})
		}
	}
}

func TestANativeViewOfAStreamGoblinClosesWhenItsTaskIsReplaced(t *testing.T) {
	// Arrange
	h, server := nativeBoard(t, "direct")
	hosttest.RunStream(t, h.Service.Store.Home.State, "task-1", false)
	h.terminalTick = 10 * time.Millisecond
	v := openNativeView(t, server, viewQuery)
	v.waitFor(t, `{"type":"stream"}`)

	// Act
	meta, err := state.ReadTaskMeta(h.Service.Store.Home.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.SpawnGen = "g2"
	if err := state.WriteTaskMeta(h.Service.Store.Home.State, meta); err != nil {
		t.Fatal(err)
	}

	// Assert
	closed := v.waitForClose(t)
	if closed.Code != websocket.StatusNormalClosure || closed.Reason != "This task restarted or was replaced. Select its current session." {
		t.Errorf("the view closed with %d %q, want it ended with the replaced task's reason", closed.Code, closed.Reason)
	}
}

// A view of a stream goblin closes as one that comes back once the board it
// was opened on restarts.
func TestANativeViewOfAStreamGoblinClosesWhenTheBoardRestarts(t *testing.T) {
	// Arrange
	h, server := nativeBoard(t, "direct")
	hosttest.RunStream(t, h.Service.Store.Home.State, "task-1", false)
	v := openNativeView(t, server, viewQuery)
	v.waitFor(t, `{"type":"stream"}`)

	// Act
	close(h.Service.done)

	// Assert
	closed := v.waitForClose(t)
	if closed.Code != websocket.StatusGoingAway || closed.Reason != "The board is restarting." {
		t.Errorf("the view closed with %d %q, want it going away as the board restarts", closed.Code, closed.Reason)
	}
}
