package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// The desktop window follows an update by itself. An update copies the new
// goblins-window.exe into the home's bin and renames the open window's
// program aside, and that window keeps running the old program until it is
// quit and opened again, which was left for the Overlord after v0.5.5 and
// again after v0.5.6 (2026-10-08). So the board's page in the window asks
// POST /api/window/move once it is in the tray, and the supervisor moves it: only a
// page shown by his own window, as the board's AFK switch proves it, and only
// a window whose program is a renamed copy beside the home's window and not
// that program itself. It ends that window and opens the home's through the
// desktop shell, so the new window starts with the desktop's environment and
// none of the supervisor's. The shell that opens it then exits, so the new
// window has no parent at the desktop, and the board's proofs take it as his
// by the program it runs (afk_board.go).

// windowName is the desktop window's program, in the home's bin.
const windowName = "goblins-window.exe"

// windowMoveFile records that the supervisor moved the window and whether
// the new one starts in the tray, which the window reads as it starts.
const windowMoveFile = "window-move.json"

// windowExitWait bounds how long a move waits for the old window to end
// before it opens the new one, which a window still running would take as
// its own second start.
const windowExitWait = 10 * time.Second

// windowMove is the move's record: whether the window was in the tray, so
// the new one starts there, and when the move was made.
type windowMove struct {
	Background bool      `json:"background"`
	At         time.Time `json:"at"`
}

// windowSystem is the machine as the proof of a board and the move of the
// window meet it: read what a program is and whose, end the window through
// the handle that proved it, and open the home's window. Its zero value is
// Windows itself.
type windowSystem struct {
	identify func(pid int, start time.Time) (proc.Identity, error)
	owner    func(pid int, start time.Time) (proc.Owner, error)
	end      func(pid int, start time.Time, image string) error
	open     func(program string) error
}

func (w windowSystem) orWindows() windowSystem {
	if w.identify == nil {
		w.identify = proc.Identify
	}
	if w.owner == nil {
		w.owner = proc.OwnerOf
	}
	if w.end == nil {
		w.end = func(pid int, start time.Time, image string) error {
			return proc.TerminateVerifiedIn(pid, start, func(identity proc.Identity) error {
				if !strings.EqualFold(identity.Image, image) {
					return fmt.Errorf("process %d runs %s now, not the window %s", pid, identity.Image, image)
				}
				return nil
			})
		}
	}
	if w.open == nil {
		w.open = openThroughTheDesktop
	}
	return w
}

// openThroughTheDesktop starts program as the desktop shell starts what the
// Overlord opens, so its parent is the shell and not the supervisor.
// Explorer starts it and exits with a code of its own, which says nothing.
func openThroughTheDesktop(program string) error {
	command := execx.Command(filepath.Join(os.Getenv("SystemRoot"), "explorer.exe"), program)
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

// movingWindow asks to move the desktop window onto the home's program. The
// page asks by itself and shows nobody the answer, so it has no sentence for
// the Overlord.
var movingWindow = boardAsker("Moving the desktop window onto the program an update installed is for the Overlord's own window", ": the page in his Code Goblins window asks it once it is in the tray", "", "")

// moveWindowFromBoard serves POST /api/window/move.
func (h *HTTP) moveWindowFromBoard(w http.ResponseWriter, r *http.Request) {
	asked := time.Now()
	var input struct {
		Hidden bool `json:"hidden"`
	}
	if err := decodeBody(w, r, &input, 1<<10); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	moved, err := h.Service.moveWindow(r, h.Host, asked, input.Hidden)
	if err != nil {
		apiError(w, http.StatusForbidden, err.Error())
		return
	}
	respond(w, http.StatusOK, struct {
		Moved bool `json:"moved"`
	}{moved})
}

// moveWindow moves the Overlord's window that shows the board r came from
// onto the home's program, when it runs a renamed copy beside it, and
// reports whether it does. The page's own idleness is the page's to judge:
// it asks only with nothing unsent and no answer in progress. hidden says
// the window is in the tray, where the new one starts too.
func (s *Service) moveWindow(r *http.Request, board string, asked time.Time, hidden bool) (bool, error) {
	started, err := s.overlordsProgram(r, board, asked, movingWindow)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(started.ExeBase, windowName) {
		return false, nil
	}
	machine := s.windows.orWindows()
	identity, err := machine.identify(started.PID, started.Start)
	if err != nil {
		return false, fmt.Errorf("the window's program could not be read: %w", err)
	}
	program := filepath.Join(s.Store.Home.Bin(), windowName)
	if !leftOnAnOldProgram(identity.Image, program) {
		return false, nil
	}
	data, err := json.Marshal(windowMove{Background: hidden, At: time.Now().UTC()})
	if err != nil {
		return false, err
	}
	if err := fsx.AtomicWriteFile(filepath.Join(s.Store.Home.State, windowMoveFile), data); err != nil {
		return false, err
	}
	go s.relaunchWindow(machine, started, identity.Image, program)
	return true, nil
}

// leftOnAnOldProgram reports whether a window running image was left on an
// old program by an update: image is a renamed copy beside program, the
// home's window, which is there.
func leftOnAnOldProgram(image, program string) bool {
	if _, err := os.Stat(program); err != nil {
		return false
	}
	return strings.EqualFold(filepath.Dir(image), filepath.Dir(program)) && !strings.EqualFold(filepath.Clean(image), filepath.Clean(program))
}

// relaunchWindow ends the window started, which runs image, waits for it to
// go, and opens program in its place. What goes wrong is the supervisor's
// error, which the board shows and the CFO is woken for.
func (s *Service) relaunchWindow(machine windowSystem, started proc.Entry, image, program string) {
	if err := machine.end(started.PID, started.Start, image); err != nil {
		s.publish(fmt.Errorf("the desktop window (pid %d) could not be moved onto %s: %w", started.PID, program, err))
		return
	}
	for deadline := time.Now().Add(windowExitWait); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if _, err := machine.identify(started.PID, started.Start); err != nil {
			break
		}
	}
	if err := machine.open(program); err != nil {
		s.publish(errors.Join(fmt.Errorf("the desktop window (pid %d) was ended to move it onto %s, which did not open: open Code Goblins from the Start menu", started.PID, program), err))
	}
}
