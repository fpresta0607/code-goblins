package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// openedWindow is one Windows Terminal window a test's board started, with
// the Herdr focus already given when it opened.
type openedWindow struct {
	program string
	args    []string
	focused []string
}

func recordWindows(h *HTTP, runner *cfoRunner) *[]openedWindow {
	var opened []openedWindow
	h.openWindow = func(_ context.Context, program string, args ...string) error {
		window := openedWindow{program: program, args: args}
		if runner != nil {
			window.focused = slices.Clone(runner.focused)
		}
		opened = append(opened, window)
		return nil
	}
	return &opened
}

// Open in terminal shows a Herdr view's terminal in a Windows Terminal window
// of its own: Herdr brings the pane's workspace and tab to the front first,
// and the window attaches to that session, so it opens on the pane.
func TestOpenInTerminalAttachesToTheHerdrSessionWithTheTabInFront(t *testing.T) {
	// Arrange
	native := newTestTerminal()
	h, server, _, runner := terminalHTTPFixture(t, native)
	opened := recordWindows(h, runner)

	// Act
	reply := terminalPost(t, server, "/api/terminal/open", `{}`)
	defer reply.Body.Close()

	// Assert
	if reply.StatusCode != 200 || len(*opened) != 1 {
		body, _ := io.ReadAll(reply.Body)
		t.Fatalf("open = %d %s with %d windows, want 200 and one window", reply.StatusCode, body, len(*opened))
	}
	window := (*opened)[0]
	if window.program != "herdr" || !slices.Equal(window.args, []string{"--session", "isolated"}) {
		t.Errorf("window runs %s %q, want herdr attached to session isolated", window.program, window.args)
	}
	if !slices.Equal(window.focused, []string{"workspace focus w1", "tab focus w1:t1"}) {
		t.Errorf("focused %q before the window opened, want the CFO's workspace and tab", window.focused)
	}
}

// A native terminal opens through cfo attach, told the state folder, since a
// Windows Terminal window does not inherit the supervisor's environment.
func TestOpenInTerminalAttachesANativeTerminalInItsStateFolder(t *testing.T) {
	// Arrange
	h, server := nativeBoard(t, "direct")
	opened := recordWindows(h, nil)
	stateDir := h.Service.Store.Home.State
	meta, err := state.ReadTaskMeta(stateDir, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"native": "task=task-1&generation=" + meta.SpawnGen})

	// Act
	reply := terminalPost(t, server, "/api/terminal/open", string(body))
	defer reply.Body.Close()

	// Assert
	if reply.StatusCode != 200 || len(*opened) != 1 {
		t.Fatalf("open = %d with %d windows, want 200 and one window", reply.StatusCode, len(*opened))
	}
	window := (*opened)[0]
	if window.program != self || !slices.Equal(window.args, []string{"attach", "--state", stateDir, "task-1"}) {
		t.Errorf("window runs %s %q, want this program attaching task-1 in %s", window.program, window.args, stateDir)
	}
}

// Nothing opens for a terminal the board cannot prove, and a window that
// cannot start says why.
func TestOpenInTerminalRefusesWhatItCannotOpen(t *testing.T) {
	cases := []struct {
		name   string
		native bool
		body   string
		fail   error
		status int
		reason string
	}{
		{"a replaced task", true, `{"native":"task=task-1&generation=old"}`, nil, 409, "restarted or was replaced"},
		{"no Windows Terminal", false, `{}`, errors.New("Windows Terminal is not installed, so the terminal cannot open beside the board."), 503, "Windows Terminal is not installed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var h *HTTP
			var server *httptest.Server
			if c.native {
				h, server = nativeBoard(t, "direct")
			} else {
				h, server, _, _ = terminalHTTPFixture(t, newTestTerminal())
			}
			attempts := 0
			h.openWindow = func(context.Context, string, ...string) error {
				attempts++
				return c.fail
			}

			reply := terminalPost(t, server, "/api/terminal/open", c.body)
			defer reply.Body.Close()
			body, _ := io.ReadAll(reply.Body)

			if reply.StatusCode != c.status || !strings.Contains(string(body), c.reason) {
				t.Fatalf("open = %d %s, want %d naming %q", reply.StatusCode, body, c.status, c.reason)
			}
			if c.fail == nil && attempts != 0 {
				t.Fatalf("a refused open started %d windows, want none", attempts)
			}
		})
	}
}

// Windows Terminal reads a semicolon as the start of another command, so one
// inside the program or an argument is escaped and the window runs it whole.
func TestWindowsTerminalKeepsASemicolonInsideAnArgument(t *testing.T) {
	got := newWindowArgs(`C:\tools;x\herdr.exe`, []string{"--session", "a;b"})

	want := []string{"-w", "new", `C:\tools\;x\herdr.exe`, "--session", `a\;b`}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}
