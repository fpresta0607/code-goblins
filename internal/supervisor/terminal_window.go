package supervisor

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
)

// openTerminalWindow opens the terminal a view shows in a Windows Terminal
// window of its own, beside the board: a goblin or CFO in Herdr attached to
// its session with its tab in front, a native terminal through cfo attach.
// The browser names only what its view shows; the program and its arguments
// are the supervisor's own. A goblin whose gate owns its task is refused, as
// typing into it is.
func (h *HTTP) openTerminalWindow(w http.ResponseWriter, r *http.Request) {
	var input struct {
		terminalSelection
		// Native is a native view's query: cfo=<terminal>, or a task and its
		// generation.
		Native string `json:"native"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if input.Native != "" {
		query, err := url.ParseQuery(input.Native)
		if err != nil {
			apiError(w, 400, "Invalid terminal selection")
			return
		}
		binding, err := h.Service.nativeBinding(query)
		if err != nil {
			apiError(w, 409, err.Error())
			return
		}
		custody, err := binding.check(ctx)
		if err == nil {
			err = custody
		}
		if err != nil {
			apiError(w, 409, err.Error())
			return
		}
		cfo, err := os.Executable()
		if err != nil {
			apiError(w, 503, "The board cannot find its own program to attach with.")
			return
		}
		if err := h.openWindow(ctx, cfo, "attach", "--state", h.Service.Store.Home.State, binding.id); err != nil {
			apiError(w, 503, err.Error())
			return
		}
	} else {
		b, err := h.Service.resolveTerminal(ctx, input.terminalSelection, true)
		if err != nil {
			apiError(w, 409, err.Error())
			return
		}
		endpoint := herdr.Endpoint{Target: b.Target, WorkspaceID: b.Workspace, TabID: b.Tab}
		if err := h.Service.Options.CFO.Terminals(b.Target.Session).Focus(ctx, endpoint); err != nil {
			apiError(w, 503, "Herdr could not bring this terminal to the front.")
			return
		}
		if err := h.openWindow(ctx, "herdr", "--session", b.Target.Session); err != nil {
			apiError(w, 503, err.Error())
			return
		}
	}
	respond(w, 200, struct {
		Message string `json:"message"`
	}{"Opened in Windows Terminal."})
}

// newWindowArgs is wt.exe's command line for a new window running path with
// args. Windows Terminal reads a semicolon as the start of another command,
// so one inside an argument is escaped.
func newWindowArgs(path string, args []string) []string {
	command := []string{"-w", "new", strings.ReplaceAll(path, ";", `\;`)}
	for _, arg := range args {
		command = append(command, strings.ReplaceAll(arg, ";", `\;`))
	}
	return command
}

// windowsTerminal starts a Windows Terminal window running program, found on
// PATH, with args.
func windowsTerminal(ctx context.Context, program string, args ...string) error {
	wt, err := exec.LookPath("wt.exe")
	if err != nil {
		return errors.New("Windows Terminal is not installed, so the terminal cannot open beside the board.")
	}
	path, err := exec.LookPath(program)
	if err != nil {
		return errors.New(program + " was not found, so the terminal cannot open beside the board.")
	}
	if err := (execx.OSRunner{}).Start(ctx, execx.Request{Name: wt, Args: newWindowArgs(path, args)}); err != nil {
		return errors.New("Windows Terminal could not be started.")
	}
	return nil
}
