package main

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// unreadPayload records whether a hook read its payload.
type unreadPayload struct{ read bool }

func (p *unreadPayload) Read([]byte) (int, error) {
	p.read = true
	return 0, io.EOF
}

// The CFO's hooks are user-level, so every goblin and every gate agent on
// the machine runs them on each tool call. Neither is the CFO: each hook
// returns on its environment alone, before it reads the payload or the home.
func TestGoblinAndGateAgentHooksReturnBeforeReadingAnything(t *testing.T) {
	for _, session := range []struct{ name, variable, value string }{
		{"goblin", harness.RoleVariable, harness.RoleGoblin},
		{"gate agent", "NO_MISTAKES_GATE", "1"},
	} {
		t.Run(session.name, func(t *testing.T) {
			// Arrange: a primary home the hooks would otherwise apply to.
			newCFOHome(t)
			t.Setenv(session.variable, session.value)
			for _, name := range []string{"pretool-bash", "pretool-powershell", "pretool-arm", "pretool-cd", "pretool-subagent", "turnend-guard", "stop-autoarm", "session-start"} {
				payload := &unreadPayload{}
				var stdout, stderr bytes.Buffer

				// Act
				exit := runHook(name, payload, &stdout, &stderr)

				// Assert
				if exit != 0 || payload.read || stdout.Len() != 0 || stderr.Len() != 0 {
					t.Errorf("%s: exit %d, read payload %v, stdout %q, stderr %q; want 0, unread and silent", name, exit, payload.read, stdout.String(), stderr.String())
				}
			}
		})
	}
}

// A Bash call runs one hook, pretool-bash, which applies both Bash guards.
// It judges its home from the files alone: with nothing on PATH to start,
// the CFO's Bash calls are still refused a watcher arm and a relocation.
func TestTheCFOsBashHookAppliesBothGuardsWithoutStartingAProcess(t *testing.T) {
	// Arrange
	newCFOHome(t)
	t.Setenv("PATH", t.TempDir())
	cases := []struct {
		command string
		hook    string
		want    int
		reason  string
	}{
		{"cfo watch &", "pretool-bash", 2, "watcher-background"},
		{`cd C:\`, "pretool-bash", 2, "cwd-relocation"},
		{"git log --oneline", "pretool-bash", 0, ""},
		{`cd C:\`, "pretool-cd", 2, "cwd-relocation"},
		{"cfo watch &", "pretool-arm", 2, "watcher-background"},
	}
	for _, c := range cases {
		var stdout, stderr bytes.Buffer
		payload := strings.NewReader(`{"session_id":"s","tool_name":"Bash","tool_input":{"command":` + quoteJSON(c.command) + `}}`)

		// Act
		exit := runHook(c.hook, payload, &stdout, &stderr)

		// Assert
		if exit != c.want || !strings.Contains(stderr.String(), c.reason) {
			t.Errorf("%s %q: exit %d, stderr %q; want exit %d naming %q", c.hook, c.command, exit, stderr.String(), c.want, c.reason)
		}
	}
}

// A pre-tool hook's own work, after cfo.exe has started, is reading its
// payload and one of the home's files, with no child process, symlink
// resolution, Herdr call or walk of the process tree on the path. Its budget
// is what it does, counted: hookBodyOpens files opened, and no list of the
// machine's processes, which every walk of the process tree takes.
//
// Until 2026-10 the budget was a median of 5 ms and a 95th percentile of
// 50 ms, said to be broken by any of those and by no loaded machine. Measured
// on 2026-10-09, neither held. Beside a cold build the same hook, with
// nothing added, took 5 to 16 ms at the median and 34 to 181 ms at the 95th
// percentile, so the test failed on main. And on a quiet machine, where the
// hook takes 0.6 ms and a walk of the process tree 3.5 ms, a hook with a walk
// added would have passed.
//
// No time is asserted, because every yardstick tried measured the machine
// before the hook. A list of the machine's processes, timed beside the hook,
// cost six times the hook on a workstation running hundreds of processes and
// a third of it on a CI runner running few. The hook's time is logged.
const hookBodyOpens = 1

// The CFO's pre-tool hooks run before every tool call they select, so what
// each does after starting stays within its budget.
func TestTheCFOsPreToolHooksStayWithinTheirBudget(t *testing.T) {
	// Arrange
	newCFOHome(t)
	bash := `{"session_id":"s","tool_name":"Bash","tool_input":{"command":"git log --oneline"}}`
	cases := []struct{ hook, payload string }{
		{"pretool-bash", bash},
		{"pretool-powershell", `{"session_id":"s","tool_name":"PowerShell","tool_input":{"command":"git log --oneline"}}`},
		{"pretool-arm", bash},
		{"pretool-subagent", `{"session_id":"s","tool_name":"TaskCreate"}`},
	}
	const runs = 40
	for _, c := range cases {
		durations := make([]time.Duration, runs)
		opened, listed := fsx.Opens(), proc.Lists()
		for i := range durations {
			var stdout, stderr bytes.Buffer
			start := time.Now()

			// Act
			exit := runHook(c.hook, strings.NewReader(c.payload), &stdout, &stderr)

			durations[i] = time.Since(start)
			if exit != 0 {
				t.Fatalf("%s exit %d, stderr %q; want the call allowed", c.hook, exit, stderr.String())
			}
		}
		opened, listed = fsx.Opens()-opened, proc.Lists()-listed

		// Assert
		if opened != runs*hookBodyOpens || listed != 0 {
			t.Errorf("%s opened %d file(s) and listed the machine's processes %d time(s) in %d runs, want %d file(s) a run and no list", c.hook, opened, listed, runs, hookBodyOpens)
		}
		slices.Sort(durations)
		t.Logf("%s: median %v, 95th percentile %v", c.hook, durations[runs/2], durations[runs*95/100])
	}
}

func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
