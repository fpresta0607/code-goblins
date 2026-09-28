package main

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
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
			newPrimaryHome(t)
			t.Setenv(session.variable, session.value)
			for _, name := range []string{"pretool-bash", "pretool-arm", "pretool-cd", "pretool-subagent", "turnend-guard", "stop-autoarm", "session-start"} {
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
	newPrimaryHome(t)
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
// payload and a few of the home's files, with no child process, symlink
// resolution, Herdr call or walk of the process tree on the path. Its median
// stays within hookBodyMedian, which any of those breaks and a loaded machine
// does not, and its 95th percentile within the 50 ms a whole hook may take.
const (
	hookBodyMedian = 5 * time.Millisecond
	hookBodyP95    = 50 * time.Millisecond
)

// The CFO's pre-tool hooks run before every tool call they select, so what
// each does after starting stays within its budget.
func TestTheCFOsPreToolHooksStayWithinTheirBudget(t *testing.T) {
	// Arrange
	newPrimaryHome(t)
	bash := `{"session_id":"s","tool_name":"Bash","tool_input":{"command":"git log --oneline"}}`
	cases := []struct{ hook, payload string }{
		{"pretool-bash", bash},
		{"pretool-arm", bash},
		{"pretool-subagent", `{"session_id":"s","tool_name":"TaskCreate"}`},
	}
	for _, c := range cases {
		durations := make([]time.Duration, 40)
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

		// Assert
		slices.Sort(durations)
		p50, p95 := durations[len(durations)/2], durations[len(durations)*95/100]
		t.Logf("%s: p50 %v, p95 %v", c.hook, p50, p95)
		if p50 > hookBodyMedian || p95 > hookBodyP95 {
			t.Errorf("%s p50 %v, p95 %v; want <= %v and <= %v", c.hook, p50, p95, hookBodyMedian, hookBodyP95)
		}
	}
}

func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
