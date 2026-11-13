package supervisor

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/sys/windows"

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

// launchStream hosts task-1's stream session in a host process of its own,
// as goblins host does, its program the stand-in hosttest.StreamChild names
// given args, and closes it when the test ends.
func launchStream(t *testing.T, stateDir string, args ...string) host.Record {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := runHostCommand(t)
	record, err := host.Launch(stateDir, command, os.Environ(), host.Spec{ID: "task-1", Args: append([]string{program, hosttest.StreamChild}, args...), IsStream: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Close(stateDir, record, 15*time.Second); err != nil {
			t.Errorf("close task-1's stream session: %v", err)
		}
	})
	return record
}

// A view of a stream goblin whose host stops answering, as one Windows ended
// does, closes as one that comes back.
func TestANativeViewOfAStreamGoblinClosesWhenItsHostStopsAnswering(t *testing.T) {
	// Arrange
	h, server := nativeBoard(t, "direct")
	record := launchStream(t, h.Service.Store.Home.State)
	v := openNativeView(t, server, viewQuery)
	v.waitFor(t, `{"type":"stream"}`)

	// Act
	process, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(record.HostPID))
	if err != nil {
		t.Fatal(err)
	}
	err = windows.TerminateProcess(process, 1)
	_ = windows.CloseHandle(process)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	closed := v.waitForClose(t)
	if closed.Code != websocket.StatusGoingAway || closed.Reason != "The stream session's host stopped answering." {
		t.Errorf("the view closed with %d %q, want it going away as its host stopped answering", closed.Code, closed.Reason)
	}
}

// A view whose stream session refuses the interrupt, as one whose program
// stopped reading refuses every line, closes saying so rather than drop the
// key, and leaves the session running.
func TestANativeViewOfAStreamGoblinClosesWhenItsSessionRefusesTheInterrupt(t *testing.T) {
	// Arrange: the stand-in writes its turn's start once it closed its input.
	h, server := nativeBoard(t, "direct")
	record := launchStream(t, h.Service.Store.Home.State, hosttest.ClosesInput, hosttest.TakenLine)
	v := openNativeView(t, server, viewQuery)
	v.waitFor(t, `"working":true`)

	// Act
	v.send(t, websocket.MessageBinary, "\x1b")

	// Assert
	closed := v.waitForClose(t)
	if closed.Code != websocket.StatusGoingAway || closed.Reason != "The stream session did not take the interrupt. Inspect it before stopping it again." {
		t.Errorf("the view closed with %d %q, want it going away as the session refused the interrupt", closed.Code, closed.Reason)
	}
	if !host.Running(record) {
		t.Error("the stream session ended, want it left running for inspection")
	}
}
