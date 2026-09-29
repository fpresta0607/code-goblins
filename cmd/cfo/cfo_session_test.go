package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	f.runtime.gitTop = func(context.Context) (string, error) {
		f.t.Error("goblins picked a project for a CFO that is live")
		return "", errors.New("not asked")
	}
}

// projectsRoot makes a projects root holding the named folders, those in
// checkouts with a .git of their own, and points the fixture at it with the
// terminal in no checkout.
func (f *launcherFixture) projectsRoot(folders []string, checkouts ...string) string {
	f.t.Helper()
	root := f.t.TempDir()
	for _, folder := range folders {
		if err := os.MkdirAll(filepath.Join(root, folder), 0o755); err != nil {
			f.t.Fatal(err)
		}
	}
	for _, checkout := range checkouts {
		if err := os.MkdirAll(filepath.Join(root, checkout, ".git"), 0o755); err != nil {
			f.t.Fatal(err)
		}
	}
	f.runtime.projectsRoot = func() (string, error) { return root, nil }
	f.runtime.gitTop = func(context.Context) (string, error) { return "", errors.New("not a git checkout") }
	return root
}

// A CFO whose registration names a live process is never started twice:
// goblins brings its registered tab to the front and attaches to the session
// it registered in.
func TestGoblinsBringsALiveCFOToTheFrontWithoutStartingAnother(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.cfoStarts) != 0 {
		t.Fatalf("exit=%d cfoStarts=%q stderr=%q, want no CFO started", exit, f.cfoStarts, stderr)
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

	if exit != 0 || len(f.cfoStarts) != 0 || len(f.nativeStarts) != 0 || len(f.focused) != 0 || len(f.attached) != 0 {
		t.Fatalf("exit=%d cfoStarts=%q nativeStarts=%q focused=%+v attached=%q stderr=%q, want nothing started and nothing in Herdr", exit, f.cfoStarts, f.nativeStarts, f.focused, f.attached, stderr)
	}
	if len(f.nativeAttached) != 1 || f.nativeAttached[0] != "cfo" {
		t.Errorf("native terminals shown = %q, want the CFO's, cfo", f.nativeAttached)
	}
}

// goblins --native with no live CFO starts one in native terminal cfo, in the
// project picked, and shows it in this terminal, starting nothing in Herdr.
func TestGoblinsNativeStartsTheCFOInANativeTerminal(t *testing.T) {
	f := newSessionFixture(t)

	exit, stdout, stderr := f.launch("--native")

	if exit != 0 || len(f.nativeStarts) != 1 || f.nativeStarts[0] != f.home.Root || len(f.cfoStarts) != 0 || len(f.attached) != 0 {
		t.Fatalf("exit=%d nativeStarts=%q cfoStarts=%q attached=%q stderr=%q, want the CFO started natively in %s", exit, f.nativeStarts, f.cfoStarts, f.attached, stderr, f.project)
	}
	if len(f.nativeAttached) != 1 || f.nativeAttached[0] != supervisor.NativeCFOTerminal {
		t.Errorf("native terminals shown = %q, want %s", f.nativeAttached, supervisor.NativeCFOTerminal)
	}
	if !strings.Contains(stdout, "The CFO starts as claude in "+f.home.Root+", in native terminal cfo.") {
		t.Errorf("stdout = %q, want it to say where the CFO starts", stdout)
	}
}

// goblins --native never starts a second CFO beside a live one in Herdr: it
// brings that one to the front.
func TestGoblinsNativeBringsALiveHerdrCFOToTheFront(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()

	exit, _, stderr := f.launch("--native")

	if exit != 0 || len(f.nativeStarts) != 0 || len(f.cfoStarts) != 0 || len(f.focused) != 1 {
		t.Fatalf("exit=%d nativeStarts=%q cfoStarts=%q focused=%+v stderr=%q, want the live CFO brought to the front and none started", exit, f.nativeStarts, f.cfoStarts, f.focused, stderr)
	}
}

// A native CFO that cannot start is reported, and nothing is shown.
func TestGoblinsNativeReportsACFOThatCannotStart(t *testing.T) {
	f := newSessionFixture(t)
	f.runtime.startNativeCFO = func(string, string, string) error { return errors.New("claude is not on PATH") }

	exit, _, stderr := f.launch("--native")

	if exit != 1 || !strings.Contains(stderr, "could not be started in a native terminal: claude is not on PATH") || len(f.nativeAttached) != 0 {
		t.Fatalf("exit=%d stderr=%q nativeAttached=%q, want the failure reported and nothing shown", exit, stderr, f.nativeAttached)
	}
}

// With no CFO registered, a CFO already running in native terminal cfo, which
// may not have registered yet, is shown rather than started a second time,
// with or without --native.
func TestGoblinsShowsAnUnregisteredCFOInNativeTerminalCFO(t *testing.T) {
	for name, args := range map[string][]string{"goblins": nil, "goblins --native": {"--native"}} {
		t.Run(name, func(t *testing.T) {
			f := newSessionFixture(t)
			f.cfoTerminalRuns = true

			exit, stdout, stderr := f.launch(args...)

			if exit != 0 || len(f.nativeStarts) != 0 || len(f.cfoStarts) != 0 || len(f.attached) != 0 {
				t.Fatalf("exit=%d nativeStarts=%q cfoStarts=%q attached=%q stderr=%q, want nothing started", exit, f.nativeStarts, f.cfoStarts, f.attached, stderr)
			}
			if !slices.Equal(f.nativeAttached, []string{supervisor.NativeCFOTerminal}) || !strings.Contains(stdout, "The CFO is already running in native terminal cfo.") {
				t.Errorf("native terminals shown = %q, stdout = %q; want cfo shown and said so", f.nativeAttached, stdout)
			}
		})
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

// A cold start: the registered CFO's process has ended and the new board has
// not checked the registration yet, so its snapshot's is empty. goblins
// starts the CFO in the checkout the terminal is in and attaches to the
// fleet's session.
func TestGoblinsStartsTheCFOWhenTheRegisteredOneHasEnded(t *testing.T) {
	f := newSessionFixture(t)
	exit, stdout, stderr := f.launch()
	if exit != 0 || len(f.nativeStarts) != 1 || f.nativeStarts[0] != f.home.Root || len(f.cfoStarts) != 0 || !slices.Equal(f.nativeAttached, []string{"cfo"}) {
		t.Fatalf("exit=%d native=%q stderr=%s", exit, f.nativeStarts, stderr)
	}
	if !strings.Contains(stdout, "The CFO starts as claude in "+f.home.Root) {
		t.Fatalf("stdout=%s", stdout)
	}
}

func TestGoblinsReusesANativeCFOOnALaterLaunch(t *testing.T) {
	f := newSessionFixture(t)
	for range 2 {
		if exit, _, stderr := f.launch(); exit != 0 {
			t.Fatalf("exit=%d stderr=%s", exit, stderr)
		}
	}
	if len(f.nativeStarts) != 1 || len(f.nativeAttached) != 2 {
		t.Fatalf("starts=%q attached=%q", f.nativeStarts, f.nativeAttached)
	}
}

// A live CFO that cannot be brought to the front is reported, and nothing is
// attached.
func TestGoblinsReportsALiveCFOItCannotBringToTheFront(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()
	f.runtime.focusCFO = func(context.Context, herdr.Endpoint) error { return errors.New("herdr server is not running") }

	exit, _, stderr := f.launch()

	if exit != 1 || len(f.attached) != 0 || len(f.cfoStarts) != 0 || !strings.Contains(stderr, "herdr server is not running") {
		t.Fatalf("exit=%d attached=%q cfoStarts=%q stderr=%q, want the failure and no attach", exit, f.attached, f.cfoStarts, stderr)
	}
}

func TestGoblinsUsesHomeOutsideACheckout(t *testing.T) {
	f := newSessionFixture(t)
	f.projectsRoot([]string{"notes"}, "alpha", "beta")
	exit, stdout, stderr := f.launch()
	if exit != 0 || len(f.nativeStarts) != 1 || f.nativeStarts[0] != f.home.Root || strings.Contains(stdout, "Project number") {
		t.Fatalf("exit=%d starts=%q stdout=%s stderr=%s", exit, f.nativeStarts, stdout, stderr)
	}
}

func TestGoblinsUsesHomeWhenOnlyOneProjectExists(t *testing.T) {
	f := newSessionFixture(t)
	f.projectsRoot(nil, "alpha")
	exit, stdout, stderr := f.launch()
	if exit != 0 || len(f.nativeStarts) != 1 || f.nativeStarts[0] != f.home.Root || strings.Contains(stdout, "Project number") {
		t.Fatalf("exit=%d starts=%q stderr=%s", exit, f.nativeStarts, stderr)
	}
}

func TestGoblinsNeverReadsAProjectNumber(t *testing.T) {
	for _, answer := range []string{"3\n", "zero\n", ""} {
		f := newSessionFixture(t)
		f.projectsRoot(nil, "alpha", "beta")
		f.runtime.stdin = strings.NewReader(answer)
		if exit, _, stderr := f.launch(); exit != 0 || len(f.nativeStarts) != 1 || f.nativeStarts[0] != f.home.Root {
			t.Fatalf("answer=%q exit=%d starts=%q stderr=%s", answer, exit, f.nativeStarts, stderr)
		}
	}
}

func TestGoblinsNeedsNoProjectRoot(t *testing.T) {
	f := newSessionFixture(t)
	f.runtime.gitTop = func(context.Context) (string, error) { return "", errors.New("not a checkout") }
	if exit, _, stderr := f.launch(); exit != 0 || len(f.nativeStarts) != 1 || f.nativeStarts[0] != f.home.Root {
		t.Fatalf("exit=%d starts=%q stderr=%s", exit, f.nativeStarts, stderr)
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

// A CFO that cannot be started is reported, and nothing is attached.
func TestGoblinsReportsACFOThatCannotStart(t *testing.T) {
	f := newSessionFixture(t)
	f.runtime.startNativeCFO = func(string, string, string) error {
		return errors.New("agent is not installed")
	}

	exit, _, stderr := f.launch()

	if exit != 1 || len(f.attached) != 0 || !strings.Contains(stderr, "agent is not installed") {
		t.Fatalf("exit=%d attached=%q stderr=%q, want the failure and no attach", exit, f.attached, stderr)
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
