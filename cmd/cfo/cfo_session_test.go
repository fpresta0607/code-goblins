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

const noCFOSnapshot = `{"registration":"no CFO has registered yet"}`

// newSessionFixture is a launcher fixture whose supervisor is already serving
// snapshot, so each test starts at the CFO session.
func newSessionFixture(t *testing.T, snapshot string) *launcherFixture {
	t.Helper()
	f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) {
		t.Fatal("goblins started a second supervisor")
		return nil, nil
	})
	f.record(fakeBoard(t, snapshot))
	return f
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

// A CFO the board already reaches is never started twice: goblins only hands
// the terminal to herdr.
func TestGoblinsAttachesToALiveCFOWithoutStartingAnother(t *testing.T) {
	f := newSessionFixture(t, busySnapshot)

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.cfoStarts) != 0 || f.attaches != 1 {
		t.Fatalf("exit=%d cfoStarts=%q attaches=%d stderr=%q, want only an attach", exit, f.cfoStarts, f.attaches, stderr)
	}
}

// With no live CFO, goblins starts it in the checkout the terminal is in and
// then attaches.
func TestGoblinsStartsTheCFOInTheCheckoutItRunsIn(t *testing.T) {
	f := newSessionFixture(t, noCFOSnapshot)

	exit, stdout, stderr := f.launch()

	if exit != 0 || len(f.cfoStarts) != 1 || f.cfoStarts[0] != f.project || f.attaches != 1 {
		t.Fatalf("exit=%d cfoStarts=%q attaches=%d stderr=%q, want the CFO started in %s and an attach", exit, f.cfoStarts, f.attaches, stderr, f.project)
	}
	if !strings.Contains(stdout, "The CFO starts in "+f.project+".") {
		t.Errorf("stdout = %q, want it to say where the CFO starts", stdout)
	}
}

// Outside a checkout, goblins lists the checkouts under the projects root, a
// folder with no .git left out, and starts the CFO in the one picked.
func TestGoblinsAsksWhichProjectOutsideACheckout(t *testing.T) {
	f := newSessionFixture(t, noCFOSnapshot)
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
	f := newSessionFixture(t, noCFOSnapshot)
	root := f.projectsRoot(nil, "alpha")

	exit, stdout, _ := f.launch()

	if exit != 0 || len(f.cfoStarts) != 1 || f.cfoStarts[0] != filepath.Join(root, "alpha") || strings.Contains(stdout, "Project number") {
		t.Fatalf("exit=%d cfoStarts=%q stdout=%q, want alpha without a question", exit, f.cfoStarts, stdout)
	}
}

// A number that names no project starts nothing and attaches nothing.
func TestGoblinsRefusesAProjectNumberThatNamesNoProject(t *testing.T) {
	for _, answer := range []string{"3\n", "zero\n", ""} {
		f := newSessionFixture(t, noCFOSnapshot)
		f.projectsRoot(nil, "alpha", "beta")
		f.runtime.stdin = strings.NewReader(answer)

		exit, _, stderr := f.launch()

		if exit != 1 || len(f.cfoStarts) != 0 || f.attaches != 0 {
			t.Errorf("answer %q: exit=%d cfoStarts=%q attaches=%d stderr=%q, want a refusal", answer, exit, f.cfoStarts, f.attaches, stderr)
		}
	}
}

// With neither a checkout nor a projects root, goblins says how to give it one.
func TestGoblinsNamesTheFixWhenThereIsNoProject(t *testing.T) {
	f := newSessionFixture(t, noCFOSnapshot)
	f.runtime.gitTop = func(context.Context) (string, error) { return "", errors.New("not a git checkout") }

	exit, _, stderr := f.launch()

	if exit != 1 || !strings.Contains(stderr, "cfo install --projects-root <dir>") || len(f.cfoStarts) != 0 {
		t.Fatalf("exit=%d stderr=%q cfoStarts=%q, want the fix named", exit, stderr, f.cfoStarts)
	}
}

// Inside a Herdr pane there is nothing to attach: goblins says where the CFO
// is and leaves the pane alone.
func TestGoblinsInsideHerdrOnlySaysWhereTheCFOIs(t *testing.T) {
	f := newSessionFixture(t, busySnapshot)
	t.Setenv("HERDR_PANE_ID", "p1")

	exit, stdout, _ := f.launch()

	if exit != 0 || f.attaches != 0 || !strings.Contains(stdout, "The CFO is in Herdr's cfo tab.") {
		t.Fatalf("exit=%d attaches=%d stdout=%q, want no attach and where the CFO is", exit, f.attaches, stdout)
	}
}

// A CFO that cannot be started is reported, and nothing is attached.
func TestGoblinsReportsACFOThatCannotStart(t *testing.T) {
	f := newSessionFixture(t, noCFOSnapshot)
	f.runtime.startCFO = func(context.Context, string) error { return errors.New("herdr is not installed") }

	exit, _, stderr := f.launch()

	if exit != 1 || f.attaches != 0 || !strings.Contains(stderr, "herdr is not installed") {
		t.Fatalf("exit=%d attaches=%d stderr=%q, want the failure and no attach", exit, f.attaches, stderr)
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
// and Claude Code is started as the CFO only in a cfo tab that holds no
// agent; a running CFO is only brought to the front.
func TestStartingTheCFOStartsClaudeOnlyWhereNoAgentRuns(t *testing.T) {
	for name, agent := range map[string]string{
		"a cfo tab with no agent": `exit1:{"error":{"code":"agent_not_found"}}`,
		"a CFO already running":   `{"result":{"agent":{"agent_status":"idle"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			script := &herdrScript{replies: map[string][]string{
				"status --json":   {`{"server":{"running":true}}`},
				"workspace list":  {`{"result":{"workspaces":[{"workspace_id":"ws-1","label":"cfo"}]}}`},
				"tab list":        {`{"result":{"tabs":[{"tab_id":"tab-cfo","label":"cfo"}]}}`},
				"pane list":       {`{"result":{"panes":[{"pane_id":"pane-cfo","tab_id":"tab-cfo"}]}}`},
				"pane get":        {`{"result":{"pane":{"pane_id":"pane-cfo"}}}`},
				"agent get":       {agent},
				"agent start":     {`{"result":{}}`},
				"workspace focus": {`{"result":{}}`},
				"tab focus":       {`{"result":{}}`},
			}}
			client := &herdr.Client{Commands: script, Session: "fleet"}

			if err := startCFOWith(context.Background(), client, `C:\dev\app`); err != nil {
				t.Fatalf("startCFOWith: %v (commands %q)", err, script.commands)
			}

			started := script.asked("agent start cfo --kind claude --pane pane-cfo")
			if wantStart := strings.HasPrefix(agent, "exit1:"); started != wantStart {
				t.Errorf("agent start asked=%v, want %v (commands %q)", started, wantStart, script.commands)
			}
			if !script.asked("workspace focus ws-1") || !script.asked("tab focus tab-cfo") {
				t.Errorf("the CFO's tab was not brought to the front: %q", script.commands)
			}
		})
	}
}

// goblins ends with herdr's own exit code.
func TestGoblinsEndsWithHerdrsExitCode(t *testing.T) {
	f := newSessionFixture(t, busySnapshot)
	f.runtime.attachHerdr = func() int { return 3 }

	if exit, _, stderr := f.launch(); exit != 3 {
		t.Fatalf("exit=%d stderr=%q, want herdr's 3", exit, stderr)
	}
}
