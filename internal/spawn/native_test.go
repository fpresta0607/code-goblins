package spawn

import (
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A browser bridge belongs to one owner. chrome-devtools-axi keeps one bridge
// and one browser for each session name, and a terminal that names none
// shares the tool's unnamed session with every other program of the user: on
// 2026-10-09 a bridge of that session held 2.5 GB that no owner could be
// proved for. A task's terminal names its own session, by its id, whatever
// the user's environment or the project's manifest named.
func TestATasksTerminalNamesItsOwnBrowserSession(t *testing.T) {
	// Arrange
	env := map[string]string{"CHROME_DEVTOOLS_AXI_SESSION": "the-users-own"}

	// Act
	Service{}.nativeEnvironment(env, state.TaskMeta{ID: "cg-process-cleanup", SpawnGen: "s1"})

	// Assert
	if got := env["CHROME_DEVTOOLS_AXI_SESSION"]; got != "cg-process-cleanup" {
		t.Fatalf("the terminal's browser session = %q, want its task's id", got)
	}
}

// A goblin records the registered CEO's session as its parent, so the board
// hangs it under the CEO whoever started it, and with no CEO registered the
// session its start runs in. Tests on 2026-10-16: three live goblins floated
// on the canvas marked Parent unreported, one spawned by a CEO in Claude
// Code, whose session variable no start read, and two the supervisor brought
// back after a memory pause, which carry no session of a CEO at all.
func TestAGoblinsParentIsTheRegisteredCEOElseTheSessionItsStartRunsIn(t *testing.T) {
	registered := func() (string, string) { return "registered-ceo", "pi" }
	none := func() (string, string) { return "", "" }
	tests := []struct {
		name                    string
		env                     map[string]string
		ceo                     func() (string, string)
		helperOf                string
		wantParent, wantHarness string
	}{
		{"an unregistered CEO in Claude Code", map[string]string{"CLAUDE_CODE_SESSION_ID": "claude-session"}, none, "", "claude-session", "claude"},
		{"an unregistered CEO in Codex", map[string]string{"CODEX_THREAD_ID": "codex-thread"}, none, "", "codex-thread", "codex"},
		{"an unregistered CEO in pi", map[string]string{"PI_SESSION_ID": "pi-session"}, none, "", "pi-session", "pi"},
		{"the registered CEO wins over a harness variable its start inherited", map[string]string{"CODEX_THREAD_ID": "codex-thread", "PI_SESSION_ID": "pi-session"}, registered, "", "registered-ceo", "pi"},
		{"a registered CEO in a harness a board cannot hold leaves the caller's session", map[string]string{"CLAUDE_CODE_SESSION_ID": "claude-session"}, func() (string, string) { return "gemini-session", "gemini" }, "", "claude-session", "claude"},
		{"the explicit convention wins over a harness variable", map[string]string{"CEO_SESSION_ID": "the-ceo", "CEO_SESSION_HARNESS": "codex", "CLAUDE_CODE_SESSION_ID": "claude-session"}, registered, "", "the-ceo", "codex"},
		{"a supervisor start, resume or switch", map[string]string{}, registered, "", "registered-ceo", "pi"},
		{"a supervisor start with no CEO registered", map[string]string{}, none, "", "", ""},
		{"a helper the supervisor starts keeps to its goblin", map[string]string{}, registered, "cg-parent-goblin", "", ""},
		{"an explicit session with no harness is no parent", map[string]string{"CEO_SESSION_ID": "the-ceo"}, nil, "", "", ""},
		{"a session id with a line break is no parent", map[string]string{"CLAUDE_CODE_SESSION_ID": "one\ntwo"}, nil, "", "", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			for _, name := range []string{"CEO_SESSION_ID", "CEO_SESSION_HARNESS", "CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID", "PI_SESSION_ID"} {
				t.Setenv(name, "")
			}
			for name, value := range test.env {
				t.Setenv(name, value)
			}
			env := map[string]string{}

			// Act
			Service{CEOSession: test.ceo}.nativeEnvironment(env, state.TaskMeta{ID: "cg-parent", SpawnGen: "s1", Parent: test.helperOf})

			// Assert
			if env["CEO_PARENT_SESSION_ID"] != test.wantParent || env["CEO_PARENT_HARNESS"] != test.wantHarness {
				t.Fatalf("parent = %q/%q, want %q/%q", env["CEO_PARENT_SESSION_ID"], env["CEO_PARENT_HARNESS"], test.wantParent, test.wantHarness)
			}
		})
	}
}
