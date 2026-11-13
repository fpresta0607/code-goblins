package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// paneVariables are what a Herdr pane gave the shell the CEO restarted goblins
// serve from on 2026-09-27; every terminal the board then opened failed with
// Herdr's nested-instance error.
var paneVariables = map[string]string{
	"HERDR_ENV":          "1",
	"HERDR_PANE_ID":      "w1:p1",
	"HERDR_TAB_ID":       "w1:t1",
	"HERDR_WORKSPACE_ID": "w1",
	"HERDR_STARTUP_CWD":  `C:\dev`,
	"HERDR_SOCKET_PATH":  `\\.\pipe\herdr-test`,
	"HERDR_BIN_PATH":     `C:\herdr\herdr.exe`,
}

// goblins serve started under an agent harness, as an update run in the CEO's own
// native terminal starts it, forgets the marks that harness sets for every
// command it runs: the supervisor is the fleet's own service, and on
// 2026-10-25 one an update had started that way had every automatic start it
// made refused as an agent's session (#1508).
func TestAServeStartedUnderAnAgentHarnessForgetsItsMarks(t *testing.T) {
	// Arrange: the CEO's terminal carried AI_AGENT=pi that day, since pi sets
	// it for every command it runs, and Claude Code sets CLAUDECODE.
	t.Setenv("AI_AGENT", "pi")
	t.Setenv("CLAUDECODE", "1")
	// A variable that marks no agent's session stays.
	t.Setenv("HERDR_SESSION", "fleet")

	// Act: serve starts as from the CEO's terminal and stops at an address it
	// refuses.
	var stdout, stderr bytes.Buffer
	if exit := run([]string{"serve", "--listen", "example.invalid:1"}, &stdout, &stderr); exit != 2 {
		t.Fatalf("serve exit = %d (stderr %q), want the address refused", exit, stderr.String())
	}

	// Assert: no command the supervisor runs carries a mark the CEO-act check
	// reads as an agent's session.
	for _, entry := range os.Environ() {
		if name, _, _ := strings.Cut(entry, "="); supervisor.IsAgentSessionVariable(name) {
			t.Errorf("a command the supervisor runs inherits %s", entry)
		}
	}
	if os.Getenv("HERDR_SESSION") != "fleet" {
		t.Errorf("serve forgot HERDR_SESSION, which marks no agent's session")
	}
	// A dispatch the supervisor makes by itself is no longer refused for a
	// mark in its environment: the check finds none there.
	dispatch := []string{"spawn", "g1", "--project", t.TempDir(), "--brief", filepath.Join(t.TempDir(), "brief.md")}
	resolve := func() (home.Home, error) { return home.Home{State: t.TempDir()}, nil }
	if refusal := refuseAnotherAgentsSession(dispatch, resolve); strings.Contains(refusal, "AI_AGENT") || strings.Contains(refusal, "CLAUDECODE") {
		t.Errorf("the supervisor's own dispatch = %q, want the check to find no agent's mark in its environment", refusal)
	}
}

// goblins, run from a Herdr pane, starts a native CEO whose terminal carries
// none of the pane's variables either.
func TestANativeCEOStartedFromAHerdrPaneCarriesNoneOfIt(t *testing.T) {
	// Arrange
	env := []string{"PATH=C:\\bin", "HERDR_SESSION=fleet"}
	for name, value := range paneVariables {
		env = append(env, name+"="+value)
	}

	// Act
	kept := nativeCEOEnvironment(nil, env, home.Home{Root: `C:\home`, State: `C:\home\state`}, "")

	// Assert
	for _, entry := range kept {
		if name, _, _ := strings.Cut(entry, "="); paneVariables[strings.ToUpper(name)] != "" {
			t.Errorf("the native CEO starts with %s", entry)
		}
	}
	if !strings.Contains(strings.Join(kept, "\n"), "HERDR_SESSION=fleet") {
		t.Errorf("the native CEO lost HERDR_SESSION: %q", kept)
	}
}
