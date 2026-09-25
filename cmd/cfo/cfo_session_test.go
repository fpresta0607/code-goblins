package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
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

// A CFO registered in a native terminal is left where it runs: goblins starts
// no CFO in Herdr beside it, and brings nothing to the front.
func TestGoblinsLeavesANativeCFOWhereItRuns(t *testing.T) {
	f := newSessionFixture(t)
	f.nativeCFO = "cfo"

	exit, stdout, stderr := f.launch()

	if exit != 0 || len(f.cfoStarts) != 0 || len(f.focused) != 0 || len(f.attached) != 0 {
		t.Fatalf("exit=%d cfoStarts=%q focused=%+v attached=%q stderr=%q, want nothing started, focused or attached", exit, f.cfoStarts, f.focused, f.attached, stderr)
	}
	if !strings.Contains(stdout, "The CFO runs in native terminal cfo") {
		t.Errorf("stdout = %q, want it to say the CFO runs in native terminal cfo", stdout)
	}
}

// A cold start: the registered CFO's process has ended and the new board has
// not checked the registration yet, so its snapshot's is empty. goblins
// starts the CFO in the checkout the terminal is in and attaches to the
// fleet's session.
func TestGoblinsStartsTheCFOWhenTheRegisteredOneHasEnded(t *testing.T) {
	f := newSessionFixture(t)

	exit, stdout, stderr := f.launch()

	if exit != 0 || len(f.cfoStarts) != 1 || f.cfoStarts[0] != f.project || len(f.focused) != 0 {
		t.Fatalf("exit=%d cfoStarts=%q focused=%+v stderr=%q, want the CFO started in %s", exit, f.cfoStarts, f.focused, stderr, f.project)
	}
	if len(f.attached) != 1 || f.attached[0] != "fixture-fleet" {
		t.Errorf("attached %q, want the fleet's session", f.attached)
	}
	if !strings.Contains(stdout, "The CFO starts in "+f.project+".") || strings.Contains(stdout, "already running") {
		t.Errorf("stdout = %q, want it to say where the CFO starts", stdout)
	}
}

// An unregistered CFO already running in the cfo tab keeps running where it
// is, so goblins does not claim it starts in the project picked.
func TestGoblinsSaysACFOInTheCFOTabIsAlreadyRunning(t *testing.T) {
	f := newSessionFixture(t)
	f.runtime.startCFO = func(context.Context, string) (bool, error) { return false, nil }

	exit, stdout, stderr := f.launch()

	if exit != 0 || len(f.attached) != 1 {
		t.Fatalf("exit=%d attached=%q stderr=%q, want an attach", exit, f.attached, stderr)
	}
	if !strings.Contains(stdout, "The CFO is already running in Herdr's cfo tab.") || strings.Contains(stdout, "The CFO starts in") {
		t.Errorf("stdout = %q, want the CFO already running and no start in the project", stdout)
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

// Outside a checkout, goblins lists the checkouts under the projects root, a
// folder with no .git left out, and starts the CFO in the one picked.
func TestGoblinsAsksWhichProjectOutsideACheckout(t *testing.T) {
	f := newSessionFixture(t)
	root := f.projectsRoot([]string{"notes"}, "alpha", "beta")
	f.runtime.stdin = strings.NewReader("2\n")

	exit, stdout, stderr := f.launch()

	if exit != 0 || len(f.cfoStarts) != 1 || f.cfoStarts[0] != filepath.Join(root, "beta") {
		t.Fatalf("exit=%d cfoStarts=%q stderr=%q, want beta", exit, f.cfoStarts, stderr)
	}
	if !strings.Contains(stdout, "  1  alpha\n  2  beta\n") || strings.Contains(stdout, "notes") {
		t.Errorf("stdout = %q, want the two checkouts listed and the plain folder left out", stdout)
	}
}

// A projects root with one checkout needs no question.
func TestGoblinsTakesTheOnlyProjectWithoutAsking(t *testing.T) {
	f := newSessionFixture(t)
	root := f.projectsRoot(nil, "alpha")

	exit, stdout, _ := f.launch()

	if exit != 0 || len(f.cfoStarts) != 1 || f.cfoStarts[0] != filepath.Join(root, "alpha") || strings.Contains(stdout, "Project number") {
		t.Fatalf("exit=%d cfoStarts=%q stdout=%q, want alpha without a question", exit, f.cfoStarts, stdout)
	}
}

// A number that names no project starts nothing and attaches nothing.
func TestGoblinsRefusesAProjectNumberThatNamesNoProject(t *testing.T) {
	for _, answer := range []string{"3\n", "zero\n", ""} {
		f := newSessionFixture(t)
		f.projectsRoot(nil, "alpha", "beta")
		f.runtime.stdin = strings.NewReader(answer)

		exit, _, stderr := f.launch()

		if exit != 1 || len(f.cfoStarts) != 0 || len(f.attached) != 0 {
			t.Errorf("answer %q: exit=%d cfoStarts=%q attached=%q stderr=%q, want a refusal", answer, exit, f.cfoStarts, f.attached, stderr)
		}
	}
}

// With neither a checkout nor a projects root, goblins says how to give it one.
func TestGoblinsNamesTheFixWhenThereIsNoProject(t *testing.T) {
	f := newSessionFixture(t)
	f.runtime.gitTop = func(context.Context) (string, error) { return "", errors.New("not a git checkout") }

	exit, _, stderr := f.launch()

	if exit != 1 || !strings.Contains(stderr, "cfo install --projects-root <dir>") || len(f.cfoStarts) != 0 {
		t.Fatalf("exit=%d stderr=%q cfoStarts=%q, want the fix named", exit, stderr, f.cfoStarts)
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
	f.runtime.startCFO = func(context.Context, string) (bool, error) { return false, errors.New("herdr is not installed") }

	exit, _, stderr := f.launch()

	if exit != 1 || len(f.attached) != 0 || !strings.Contains(stderr, "herdr is not installed") {
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

			reported, err := startCFOWith(context.Background(), client, `C:\dev\app`)
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
	f.runtime.attachHerdr = func(string) int { return 3 }

	if exit, _, stderr := f.launch(); exit != 3 {
		t.Fatalf("exit=%d stderr=%q, want herdr's 3", exit, stderr)
	}
}
