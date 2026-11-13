package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/harness"
)

// unreadPayload records whether a hook read its payload.
type unreadPayload struct{ read bool }

func (p *unreadPayload) Read([]byte) (int, error) {
	p.read = true
	return 0, io.EOF
}

// The CEO's hooks are user-level, so every goblin and every gate agent on
// the machine runs them on each tool call. Neither is the CEO: each hook
// returns on its environment alone, before it reads the payload or the home.
func TestGoblinAndGateAgentHooksReturnBeforeReadingAnything(t *testing.T) {
	for _, session := range []struct{ name, variable, value string }{
		{"goblin", harness.RoleVariable, harness.RoleGoblin},
		{"gate agent", "NO_MISTAKES_GATE", "1"},
	} {
		t.Run(session.name, func(t *testing.T) {
			// Arrange: a primary home the hooks would otherwise apply to.
			newCEOHome(t)
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
// the CEO's Bash calls are still refused a watcher arm and a relocation.
func TestTheCEOsBashHookAppliesBothGuardsWithoutStartingAProcess(t *testing.T) {
	// Arrange
	newCEOHome(t)
	t.Setenv("PATH", t.TempDir())
	cases := []struct {
		command string
		hook    string
		want    int
		reason  string
	}{
		{"goblins watch &", "pretool-bash", 2, "watcher-background"},
		{`cd C:\`, "pretool-bash", 2, "cwd-relocation"},
		{"git log --oneline", "pretool-bash", 0, ""},
		{`cd C:\`, "pretool-cd", 2, "cwd-relocation"},
		{"goblins watch &", "pretool-arm", 2, "watcher-background"},
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

func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
