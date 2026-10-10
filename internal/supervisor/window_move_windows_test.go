package supervisor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// hisDesktop is whose the supervisor and every program of the Overlord's are
// in a test: his Windows user, in the session of his desktop.
var hisDesktop = proc.Owner{User: "S-1-5-21-1004-1001", Session: 1}

// standInWindows is the machine as the proof of a board and the move of the
// window meet it: the program every process runs as Identify reads it until
// the window is ended, unless programs names another for a PID, whose each
// process is, which is the Overlord's unless owners names another for a PID,
// and what was ended and opened.
type standInWindows struct {
	mu       sync.Mutex
	image    string
	programs map[int]proc.Identity
	owners   map[int]proc.Owner
	ended    []string
	opened   []string
}

func (w *standInWindows) system() windowSystem {
	return windowSystem{
		identify: func(pid int, start time.Time) (proc.Identity, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			if len(w.ended) > 0 {
				return proc.Identity{}, errors.New("the process has exited")
			}
			if program, named := w.programs[pid]; named {
				return program, nil
			}
			return proc.Identity{Image: w.image}, nil
		},
		owner: func(pid int, start time.Time) (proc.Owner, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			if owner, named := w.owners[pid]; named {
				return owner, nil
			}
			return hisDesktop, nil
		},
		end: func(pid int, start time.Time, image string) error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.ended = append(w.ended, image)
			return nil
		},
		open: func(program string) error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.opened = append(w.opened, program)
			return nil
		},
	}
}

func (w *standInWindows) seen() ([]string, []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string{}, w.ended...), append([]string{}, w.opened...)
}

// windowHome is a home whose bin holds the desktop window an update copied
// in, and a supervisor the board's page in his window asks to move it.
func windowHome(t *testing.T, image func(bin string) string) (*Service, *standInWindows, string) {
	t.Helper()
	store, h := testStore(t)
	s := boardService(store)
	asOverlordsBoard(s)
	program := filepath.Join(h.Bin(), windowName)
	if err := os.MkdirAll(h.Bin(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, []byte("the window v0.5.6 copied in"), 0o600); err != nil {
		t.Fatal(err)
	}
	machine := &standInWindows{image: image(h.Bin())}
	s.windows = machine.system()
	return s, machine, program
}

// An update copies the new window into bin and renames the open one aside,
// which keeps running the old program: "Quit the desktop window from its
// tray icon and open Code Goblins again, so the window runs v0.5.5" was left
// for the Overlord on 2026-10-08, and again for v0.5.6. Once the page in that
// window is idle it asks, and the supervisor ends the old window and opens
// the home's new one through the desktop shell, in the tray when it was.
func TestAWindowLeftOnAnOldProgramMovesOntoTheHomesNewOneWhenItsPageAsks(t *testing.T) {
	// Arrange
	s, machine, program := windowHome(t, func(bin string) string {
		return filepath.Join(bin, "goblins-window.exe.LU4BWTHK5ISXTSKSYN7HMYSJ32.old")
	})

	// Act
	code, body := askTheBoard(t, s, "POST", "/api/window/move", `{"hidden":true}`, nil)

	// Assert
	if code != 200 || strings.TrimSpace(body) != `{"moved":true}` {
		t.Fatalf("POST /api/window/move = %d %s, want the window moved", code, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, opened := machine.seen(); len(opened) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	ended, opened := machine.seen()
	if len(ended) != 1 || ended[0] != machine.image {
		t.Errorf("ended = %q, want the old window, by the program it runs", ended)
	}
	if len(opened) != 1 || opened[0] != program {
		t.Errorf("opened = %q, want the home's window %s", opened, program)
	}
	data, err := fsx.ReadFile(filepath.Join(s.Store.Home.State, windowMoveFile))
	var move windowMove
	if err != nil || json.Unmarshal(data, &move) != nil || !move.Background || time.Since(move.At) > time.Minute {
		t.Errorf("the move's record = %s (%v), want it to say the new window starts in the tray", data, err)
	}
}

// Nothing moves a window that already runs the home's program, a window run
// from another folder, such as a build from source, or a board in a browser.
func TestOnlyAWindowLeftOnARenamedOldProgramIsMoved(t *testing.T) {
	for name, image := range map[string]func(bin string) string{
		"the home's own program":   func(bin string) string { return filepath.Join(bin, windowName) },
		"a window of another home": func(string) string { return `C:\Users\fpres\AppData\Local\CodeGoblinsWindow\goblins-window.exe.old` },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s, machine, _ := windowHome(t, image)

			// Act
			code, body := askTheBoard(t, s, "POST", "/api/window/move", `{"hidden":false}`, nil)

			// Assert
			if code != 200 || strings.TrimSpace(body) != `{"moved":false}` {
				t.Fatalf("POST /api/window/move = %d %s, want nothing moved", code, body)
			}
			if ended, opened := machine.seen(); len(ended)+len(opened) != 0 {
				t.Errorf("ended %q and opened %q, want neither", ended, opened)
			}
		})
	}

	t.Run("a board in a browser", func(t *testing.T) {
		// Arrange
		s, machine, _ := windowHome(t, func(bin string) string { return filepath.Join(bin, "goblins-window.exe.old") })
		s.inspectCaller = func(pid int) ([]proc.Entry, []string, error) {
			started := time.Now().Add(-time.Hour)
			return []proc.Entry{
				{PID: pid, ParentPID: 4242, ExeBase: "msedge.exe", Start: started},
				{PID: 4242, ParentPID: 900, ExeBase: "msedge.exe", Start: started.Add(-time.Minute)},
				{PID: 900, ExeBase: "explorer.exe", Start: started.Add(-time.Hour)},
			}, []string{"USERNAME=overlord"}, nil
		}

		// Act
		code, body := askTheBoard(t, s, "POST", "/api/window/move", `{"hidden":true}`, nil)

		// Assert
		if code != 200 || strings.TrimSpace(body) != `{"moved":false}` {
			t.Fatalf("POST /api/window/move = %d %s, want nothing moved", code, body)
		}
		if ended, opened := machine.seen(); len(ended)+len(opened) != 0 {
			t.Errorf("ended %q and opened %q, want neither", ended, opened)
		}
	})
}

// Only his own window is ever ended: a page an agent's program shows is
// refused, as the board's AFK switch refuses it.
func TestAMoveAskedFromAProgramThatIsNotHisIsRefused(t *testing.T) {
	// Arrange
	s, machine, _ := windowHome(t, func(bin string) string { return filepath.Join(bin, "goblins-window.exe.old") })
	s.inspectCaller = func(pid int) ([]proc.Entry, []string, error) {
		started := time.Now().Add(-time.Hour)
		return []proc.Entry{
			{PID: pid, ParentPID: 4242, ExeBase: "msedgewebview2.exe", Start: started},
			{PID: 4242, ParentPID: 3100, ExeBase: "goblins-window.exe", Start: started.Add(-time.Minute)},
			{PID: 3100, ParentPID: 900, ExeBase: "claude.exe", Start: started.Add(-2 * time.Minute)},
			{PID: 900, ExeBase: "explorer.exe", Start: started.Add(-time.Hour)},
		}, []string{"USERNAME=overlord"}, nil
	}

	// Act
	code, body := askTheBoard(t, s, "POST", "/api/window/move", `{"hidden":true}`, nil)

	// Assert
	if code != 403 || !strings.Contains(body, "agent harness") {
		t.Fatalf("POST /api/window/move = %d %s, want it refused", code, body)
	}
	if ended, opened := machine.seen(); len(ended)+len(opened) != 0 {
		t.Errorf("ended %q and opened %q, want neither", ended, opened)
	}
}
