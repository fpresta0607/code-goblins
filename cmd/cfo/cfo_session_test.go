package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// newSessionFixture is a launcher fixture with no live CFO whose supervisor
// is already serving a snapshot whose registration is empty, as a
// supervisor's is before its first check, so each test starts at the CFO
// session.
func newSessionFixture(t *testing.T) *launcherFixture {
	t.Helper()
	f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) {
		t.Fatal("goblins started a second supervisor")
		return nil, nil
	})
	f.cfoLive = false
	f.record(fakeBoard(t, busySnapshot))
	return f
}

// withLiveCFO registers a live CFO in a session other than the fleet's.
func (f *launcherFixture) withLiveCFO() {
	f.cfo = herdr.Endpoint{Target: herdr.Target{Session: "cfo-session", Pane: "w9:p2"}, WorkspaceID: "w9", TabID: "w9:t2", PaneID: "w9:p2"}
	f.cfoLive = true
}

// A CFO whose registration names a live process is never started twice:
// goblins runs no agent steps, brings its registered tab to the front and
// attaches to the session it registered in.
func TestGoblinsBringsALiveCFOToTheFrontWithoutStartingAnother(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.setups) != 0 || len(f.nativeStarts) != 0 {
		t.Fatalf("exit=%d setups=%+v nativeStarts=%q stderr=%q, want no agent steps and no CFO started", exit, f.setups, f.nativeStarts, stderr)
	}
	if len(f.focused) != 1 || f.focused[0] != f.cfo {
		t.Errorf("focused %+v, want the registered CFO %+v", f.focused, f.cfo)
	}
	if len(f.attached) != 1 || f.attached[0] != "cfo-session" {
		t.Errorf("attached %q, want the registered session", f.attached)
	}
}

// A CFO registered in a native terminal is shown in this terminal: goblins
// starts no CFO beside it and touches nothing in Herdr.
func TestGoblinsShowsANativeCFOInThisTerminal(t *testing.T) {
	f := newSessionFixture(t)
	f.nativeCFO = "cfo"

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.setups) != 0 || len(f.nativeStarts) != 0 || len(f.focused) != 0 || len(f.attached) != 0 {
		t.Fatalf("exit=%d setups=%+v nativeStarts=%q focused=%+v attached=%q stderr=%q, want nothing started and nothing in Herdr", exit, f.setups, f.nativeStarts, f.focused, f.attached, stderr)
	}
	if len(f.nativeAttached) != 1 || f.nativeAttached[0] != "cfo" {
		t.Errorf("native terminals shown = %q, want the CFO's, cfo", f.nativeAttached)
	}
}

// With no CFO running, goblins starts one in the Code Goblins home, whatever
// folder it is run from, and asks for no project: in Herdr, attached to the
// fleet's session, or with --native in native terminal cfo shown here.
func TestGoblinsStartsTheCFOInItsHome(t *testing.T) {
	// Arrange
	herdrStart, nativeStart := newSessionFixture(t), newSessionFixture(t)

	// Act
	herdrExit, herdrOut, herdrErr := herdrStart.launch()
	nativeExit, nativeOut, nativeErr := nativeStart.launch("--native")

	// Assert
	if herdrExit != 0 || !slices.Equal(herdrStart.cfoStarts, []string{herdrStart.home.Root}) || len(herdrStart.nativeStarts) != 0 || !slices.Equal(herdrStart.attached, []string{"fixture-fleet"}) {
		t.Errorf("goblins: exit=%d cfoStarts=%q nativeStarts=%q attached=%q stderr=%q, want the CFO started in Herdr in the home %s and the fleet's session attached", herdrExit, herdrStart.cfoStarts, herdrStart.nativeStarts, herdrStart.attached, herdrErr, herdrStart.home.Root)
	}
	if !strings.Contains(herdrOut, "The CFO starts as claude in "+herdrStart.home.Root+".") {
		t.Errorf("goblins: stdout = %q, want it to say where the CFO starts", herdrOut)
	}
	if nativeExit != 0 || !slices.Equal(nativeStart.nativeStarts, []string{nativeStart.home.Root}) || len(nativeStart.cfoStarts) != 0 || !slices.Equal(nativeStart.nativeAttached, []string{supervisor.NativeCFOTerminal}) {
		t.Errorf("goblins --native: exit=%d nativeStarts=%q cfoStarts=%q nativeAttached=%q stderr=%q, want the CFO started natively in the home %s and shown", nativeExit, nativeStart.nativeStarts, nativeStart.cfoStarts, nativeStart.nativeAttached, nativeErr, nativeStart.home.Root)
	}
	if !strings.Contains(nativeOut, "The CFO starts as claude in "+nativeStart.home.Root+", in native terminal cfo.") {
		t.Errorf("goblins --native: stdout = %q, want it to say where the CFO starts", nativeOut)
	}
}

// A CFO that cannot start is reported, and nothing is shown.
func TestGoblinsReportsACFOThatCannotStart(t *testing.T) {
	// Arrange
	herdrStart, nativeStart := newSessionFixture(t), newSessionFixture(t)
	herdrStart.runtime.startCFO = func(context.Context, string, string) (bool, error) { return false, errors.New("herdr is not installed") }
	nativeStart.runtime.startNativeCFO = func(home.Home, string, string) error { return errors.New("claude is not on PATH") }

	// Act
	herdrExit, _, herdrErr := herdrStart.launch()
	nativeExit, _, nativeErr := nativeStart.launch("--native")

	// Assert
	if herdrExit != 1 || !strings.Contains(herdrErr, "could not be started in Herdr: herdr is not installed") || len(herdrStart.attached) != 0 || len(herdrStart.screens) != 0 {
		t.Errorf("goblins: exit=%d stderr=%q attached=%q screens=%+v, want the failure reported and nothing shown", herdrExit, herdrErr, herdrStart.attached, herdrStart.screens)
	}
	if nativeExit != 1 || !strings.Contains(nativeErr, "could not be started in a native terminal: claude is not on PATH") || len(nativeStart.nativeAttached) != 0 || len(nativeStart.screens) != 0 {
		t.Errorf("goblins --native: exit=%d stderr=%q nativeAttached=%q screens=%+v, want the failure reported and nothing shown", nativeExit, nativeErr, nativeStart.nativeAttached, nativeStart.screens)
	}
}

// An unregistered CFO already running in the cfo tab keeps running where it
// is, so goblins does not claim to start one.
func TestGoblinsSaysACFOInTheCFOTabIsAlreadyRunning(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.runtime.startCFO = func(context.Context, string, string) (bool, error) { return false, nil }

	// Act
	exit, stdout, stderr := f.launch()

	// Assert
	if exit != 0 || len(f.attached) != 1 {
		t.Fatalf("exit=%d attached=%q stderr=%q, want an attach", exit, f.attached, stderr)
	}
	if !strings.Contains(stdout, "The CFO is already running in Herdr's cfo tab.") || strings.Contains(stdout, "The CFO starts") || !strings.HasPrefix(f.screens[0].title, "Your CFO is running\n") {
		t.Errorf("stdout = %q, final screen %q; want the CFO already running and no start claimed", stdout, f.screens[0].title)
	}
}

// With no CFO registered, a CFO already running in native terminal cfo, which
// may not have registered yet, is shown rather than started a second time.
func TestGoblinsShowsAnUnregisteredCFOInNativeTerminalCFO(t *testing.T) {
	f := newSessionFixture(t)
	f.cfoTerminalRuns = true

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.setups) != 0 || len(f.nativeStarts) != 0 || len(f.attached) != 0 {
		t.Fatalf("exit=%d setups=%+v nativeStarts=%q attached=%q stderr=%q, want nothing started", exit, f.setups, f.nativeStarts, f.attached, stderr)
	}
	if !slices.Equal(f.nativeAttached, []string{supervisor.NativeCFOTerminal}) {
		t.Errorf("native terminals shown = %q, want cfo", f.nativeAttached)
	}
}

// A CFO registered in Herdr is brought to the front even while native
// terminal cfo runs: the registration decides which CFO goblins shows.
func TestGoblinsPrefersARegisteredHerdrCFOToAnUnregisteredNativeTerminal(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()
	f.cfoTerminalRuns = true

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.focused) != 1 || len(f.nativeAttached) != 0 {
		t.Fatalf("exit=%d focused=%+v nativeAttached=%q stderr=%q, want the Herdr CFO in front and no native terminal shown", exit, f.focused, f.nativeAttached, stderr)
	}
}

// A live CFO that cannot be brought to the front is reported, and nothing is
// attached.
func TestGoblinsReportsALiveCFOItCannotBringToTheFront(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()
	f.runtime.focusCFO = func(context.Context, herdr.Endpoint) error { return errors.New("herdr server is not running") }

	exit, _, stderr := f.launch()

	if exit != 1 || len(f.attached) != 0 || len(f.nativeStarts) != 0 || !strings.Contains(stderr, "herdr server is not running") {
		t.Fatalf("exit=%d attached=%q nativeStarts=%q stderr=%q, want the failure and no attach", exit, f.attached, f.nativeStarts, stderr)
	}
}

// Inside a Herdr pane there is nothing to attach: goblins brings the CFO to
// the front and says so.
func TestGoblinsInsideHerdrOnlyBringsTheCFOToTheFront(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()
	t.Setenv("HERDR_PANE_ID", "p1")

	exit, stdout, _ := f.launch()

	if exit != 0 || len(f.attached) != 0 || len(f.focused) != 1 || !strings.Contains(stdout, "The CFO is in front in Herdr.") {
		t.Fatalf("exit=%d attached=%q focused=%+v stdout=%q, want the CFO in front and no attach", exit, f.attached, f.focused, stdout)
	}
}

// herdrScript answers herdr commands by their subcommand, each from its own
// queue of replies, and records every command it was asked.
type herdrScript struct {
	replies  map[string][]string
	commands []string
}

func (s *herdrScript) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	s.commands = append(s.commands, strings.Join(req.Args, " "))
	if req.Name != "herdr" || len(req.Args) < 2 {
		return execx.Result{}, fmt.Errorf("unexpected command %s %q", req.Name, req.Args)
	}
	key := req.Args[0] + " " + req.Args[1]
	queue := s.replies[key]
	if len(queue) == 0 {
		return execx.Result{}, fmt.Errorf("no reply scripted for herdr %s", key)
	}
	s.replies[key] = queue[1:]
	if strings.HasPrefix(queue[0], "exit1:") {
		return execx.Result{Stdout: []byte(strings.TrimPrefix(queue[0], "exit1:")), ExitCode: 1}, nil
	}
	return execx.Result{Stdout: []byte(queue[0])}, nil
}

func (s *herdrScript) asked(prefix string) bool {
	for _, command := range s.commands {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}

// The CFO's session in Herdr: the server is up, the fleet workspace exists,
// and Claude Code is started as the CFO only in a fresh cfo tab in the
// project, the old one with no agent closed; a running CFO is only brought
// to the front.
func TestStartingTheCFOStartsClaudeOnlyWhereNoAgentRuns(t *testing.T) {
	for name, c := range map[string]struct {
		agent, pane, tab string
	}{
		"a cfo tab with no agent": {`exit1:{"error":{"code":"agent_not_found"}}`, "pane-new", "tab-new"},
		"a CFO already running":   {`{"result":{"agent":{"agent_status":"idle"}}}`, "pane-cfo", "tab-cfo"},
	} {
		t.Run(name, func(t *testing.T) {
			script := &herdrScript{replies: map[string][]string{
				"status --json":     {`{"server":{"running":true}}`},
				"workspace list":    {`{"result":{"workspaces":[{"workspace_id":"ws-1","label":"cfo"}]}}`},
				"tab list":          {`{"result":{"tabs":[{"tab_id":"tab-cfo","label":"cfo"}]}}`},
				"pane list":         {`{"result":{"panes":[{"pane_id":"pane-cfo","tab_id":"tab-cfo"}]}}`},
				"pane get":          {`{"result":{"pane":{"pane_id":"pane-cfo"}}}`},
				"agent get":         {c.agent},
				"tab create":        {`{"result":{"tab":{"tab_id":"tab-new"},"root_pane":{"pane_id":"pane-new"}}}`},
				"pane process-info": {`{"result":{"process_info":{"foreground_process_group_id":40,"shell_pid":40}}}`},
				"tab close":         {`{"result":{}}`},
				"agent start":       {`{"result":{}}`},
				"workspace focus":   {`{"result":{}}`},
				"tab focus":         {`{"result":{}}`},
			}}
			client := &herdr.Client{Commands: script, Session: "fleet"}

			reported, err := startCFOWith(context.Background(), client, `C:\dev\app`, "claude", io.Discard)
			if err != nil {
				t.Fatalf("startCFOWith: %v (commands %q)", err, script.commands)
			}

			started := script.asked("agent start cfo --kind claude --pane " + c.pane)
			if wantStart := strings.HasPrefix(c.agent, "exit1:"); started != wantStart || reported != wantStart {
				t.Errorf("agent start in %s asked=%v reported=%v, want %v (commands %q)", c.pane, started, reported, wantStart, script.commands)
			}
			if !script.asked("workspace focus ws-1") || !script.asked("tab focus "+c.tab) {
				t.Errorf("the CFO's tab %s was not brought to the front: %q", c.tab, script.commands)
			}
		})
	}
}

// goblins ends with herdr's own exit code.
func TestGoblinsEndsWithHerdrsExitCode(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()
	f.runtime.attachHerdr = func(string) int { return 3 }

	if exit, _, stderr := f.launch(); exit != 3 {
		t.Fatalf("exit=%d stderr=%q, want herdr's 3", exit, stderr)
	}
}
