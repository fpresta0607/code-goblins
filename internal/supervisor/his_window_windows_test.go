package supervisor

import (
	"context"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// asAWindowWhoseOpenerExited makes every request for one of the Overlord's
// switches read as one from his own desktop window after it restarted itself
// onto an update. The supervisor opens that window through the desktop shell,
// and the program that started it exits, so the window's parents stop at the
// window itself. It is the window he pressed the switch in on 2026-10-09:
// goblins-window.exe pid 22504, whose parent pid 21820 was gone. image is the
// program that window runs.
func asAWindowWhoseOpenerExited(s *Service, image string) {
	s.peerOf = func(netip.AddrPort, netip.AddrPort) (int, error) { return 7001, nil }
	s.inspectCaller = func(pid int) ([]proc.Entry, []string, error) {
		started := time.Now().Add(-2 * time.Hour)
		return []proc.Entry{
			{PID: pid, ParentPID: 6200, ExeBase: "msedgewebview2.exe", Start: started.Add(2 * time.Second)},
			{PID: 6200, ParentPID: 22504, ExeBase: "msedgewebview2.exe", Start: started.Add(time.Second)},
			{PID: 22504, ParentPID: 21820, ExeBase: "goblins-window.exe", Start: started},
		}, []string{"USERNAME=overlord"}, nil
	}
	s.windows = (&standInWindows{image: image}).system()
}

// "fix this issue i should alwyas be abel to turn on afk no issues" (the
// Overlord, 2026-10-09): he pressed the switch in the Code Goblins window
// itself and was refused, because that window had restarted itself onto
// v0.5.8 and its opener had exited. His own window turns AFK mode on and off
// whoever opened it.
func TestHisWindowSwitchesAFKModeOnAndOffAfterItRestartedItselfOntoAnUpdate(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := boardService(store)
	asAWindowWhoseOpenerExited(s, filepath.Join(h.Bin(), windowName))

	// Act
	onCode, onBody := askTheBoard(t, s, "POST", "/api/afk", `{"on":true}`, nil)
	on, onErr := afk.Read(h.State)
	offCode, offBody := askTheBoard(t, s, "POST", "/api/afk", `{"on":false}`, nil)
	off, offErr := afk.Read(h.State)

	// Assert
	if onCode != http.StatusOK || strings.TrimSpace(onBody) != `{"state":"on"}` {
		t.Fatalf("POST on from his window = %d %s, want AFK mode turned on", onCode, onBody)
	}
	if onErr != nil || !on.On || on.From != "his own board (goblins-window.exe pid 22504)" {
		t.Errorf("the switch after on = %+v, %v, want on from his own window", on, onErr)
	}
	if offCode != http.StatusOK || strings.TrimSpace(offBody) != `{"state":"off"}` {
		t.Fatalf("POST off from his window = %d %s, want AFK mode turned off", offCode, offBody)
	}
	if offErr != nil || off.On || off.EndedFrom != "his own board (goblins-window.exe pid 22504)" {
		t.Errorf("the switch after off = %+v, %v, want off from his own window", off, offErr)
	}
}

// The Update item is pressed on a board of his own, proven as the switch
// proves one, so it broke for the same window the same way.
func TestHisWindowPressesUpdateAfterItRestartedItselfOntoAnUpdate(t *testing.T) {
	// Arrange
	source := newReleaseSource(t, "v0.5.0")
	s := releaseService(t, source, "v0.4.2")
	s.checkReleases(context.Background())
	item := updateItems(s)[0]
	asAWindowWhoseOpenerExited(s, filepath.Join(s.Store.Home.Bin(), windowName))

	// Act
	status, body := askTheBoard(t, s, "POST", "/api/actions", `{"id":"press-1","kind":"run","run_id":"`+item.ID+`","generation":"`+item.Identity+`"}`, nil)

	// Assert
	if status != http.StatusAccepted {
		t.Fatalf("Update pressed in his window = %d %s, want it accepted", status, body)
	}
}

// A window the supervisor moved once is moved again by the next update: the
// page in it asks, and the supervisor takes it for his own window although
// the shell that opened it has exited.
func TestHisWindowMovesOntoTheNextUpdateAfterItRestartedItselfOntoOne(t *testing.T) {
	// Arrange
	s, machine, program := windowHome(t, func(bin string) string {
		return filepath.Join(bin, "goblins-window.exe.LU4BWTHK5ISXTSKSYN7HMYSJ32.old")
	})
	his := s.windows
	asAWindowWhoseOpenerExited(s, machine.image)
	s.windows = his

	// Act
	code, body := askTheBoard(t, s, "POST", "/api/window/move", `{"hidden":false}`, nil)

	// Assert
	if code != http.StatusOK || strings.TrimSpace(body) != `{"moved":true}` {
		t.Fatalf("POST /api/window/move from his window = %d %s, want the window moved", code, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, opened := machine.seen(); len(opened) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, opened := machine.seen(); len(opened) != 1 || opened[0] != program {
		t.Errorf("opened = %q, want the home's window %s", opened, program)
	}
}
