package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// A record of a native terminal whose host does not answer names nothing
// running.
func TestANativeTerminalWhoseHostDoesNotAnswerDoesNotRun(t *testing.T) {
	stateDir := t.TempDir()
	record := host.Record{ID: supervisor.NativeCEOTerminal, Pipe: fmt.Sprintf(`\\.\pipe\ceo-attach-test-%d`, time.Now().UnixNano()), Token: "token", Version: host.Version, HostPID: os.Getpid()}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", supervisor.NativeCEOTerminal+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	runs := supervisor.NativeTerminalRuns(stateDir, supervisor.NativeCEOTerminal)

	if runs {
		t.Errorf("nativeTerminalRuns = true, want false for a host that does not answer")
	}
}

// goblins attach with no terminal named shows the registered CEO's native
// terminal. With no CEO registered it shows terminal ceo while its host
// answers, and a CEO registered in Herdr is never passed over for it.
func TestAttachWithNoTerminalNamedShowsTheCEOsNativeTerminal(t *testing.T) {
	for name, test := range map[string]struct {
		registeredNative string
		inHerdr          bool
		ceoRuns          bool
		shown            string
		refusal          string
	}{
		"a CEO registered in a native terminal":        {registeredNative: "n1", ceoRuns: true, shown: "n1"},
		"a CEO registered in Herdr, terminal ceo runs": {inHerdr: true, ceoRuns: true, refusal: "the CEO runs in Herdr, not in a native terminal"},
		"nothing registered, terminal ceo runs":        {ceoRuns: true, shown: supervisor.NativeCEOTerminal},
		"nothing registered, nothing runs":             {refusal: "no CEO runs in a native terminal"},
	} {
		t.Run(name, func(t *testing.T) {
			stateDir := t.TempDir()
			var shown []string
			var stdout, stderr strings.Builder
			runtime := commandRuntime{
				resolveHome: func() (home.Home, error) { return home.Home{Root: filepath.Dir(stateDir), State: stateDir}, nil },
				nativeCEO: func(string) (string, bool) {
					return test.registeredNative, test.registeredNative != ""
				},
				liveCEO: func(string) (herdr.Endpoint, bool) { return herdr.Endpoint{}, test.inHerdr },
				nativeTerminalRuns: func(_, id string) bool {
					return test.ceoRuns && id == supervisor.NativeCEOTerminal
				},
				attachNative: func(_, id string, _, _ io.Writer) int {
					shown = append(shown, id)
					return 0
				},
			}

			exit := runAttach(nil, &stdout, &stderr, runtime)

			if test.refusal != "" {
				if exit != 1 || !strings.Contains(stderr.String(), test.refusal) || len(shown) != 0 {
					t.Errorf("exit=%d stderr=%q shown=%q, want %q and nothing shown", exit, stderr.String(), shown, test.refusal)
				}
				return
			}
			if exit != 0 || !slices.Equal(shown, []string{test.shown}) {
				t.Errorf("exit=%d stderr=%q shown=%q, want %s shown", exit, stderr.String(), shown, test.shown)
			}
		})
	}
}

// A console that does not inherit the supervisor's environment, such as a
// Windows Terminal window the board opened, names the state folder, and the
// terminal is looked for there and nowhere else.
func TestAttachLooksInTheStateFolderItIsGiven(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	var seen []string
	runtime := commandRuntime{
		resolveHome: func() (home.Home, error) { return home.Home{}, errors.New("no home in this console") },
		attachNative: func(dir, id string, _, _ io.Writer) int {
			seen = append(seen, dir, id)
			return 0
		},
	}
	var stdout, stderr strings.Builder

	// Act
	exit := runAttach([]string{"--state", stateDir, "task-1"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 || !slices.Equal(seen, []string{stateDir, "task-1"}) {
		t.Errorf("exit=%d stderr=%q attached %q, want task-1 in %s", exit, stderr.String(), seen, stateDir)
	}
}

// Ctrl-] leaves the terminal whichever way the console sends it: as the byte
// itself, or as a Windows key event once the terminal asked for Windows input
// mode. Its release, and other keys that look alike, stay with the terminal.
func TestCtrlCloseBracketLeavesTheTerminalInEitherEncoding(t *testing.T) {
	for name, keys := range map[string]struct {
		keys string
		at   int
	}{
		"the byte":                         {"ab\x1dcd", 2},
		"a Windows key event, pressed":     {"a\x1b[221;27;29;1;8;1_", 1},
		"a Windows key event, released":    {"\x1b[221;27;29;0;8;1_", -1},
		"another key's Windows key event":  {"\x1b[65;30;97;1;0;1_", -1},
		"an arrow key, then an underscore": {"\x1b[A_", -1},
		"typing":                           {"hello\r", -1},
	} {
		t.Run(name, func(t *testing.T) {
			if at := detachAt([]byte(keys.keys)); at != keys.at {
				t.Errorf("detachAt(%q) = %d, want %d", keys.keys, at, keys.at)
			}
		})
	}
}

// The CEO's terminal starts from the user's environment: a launcher's
// session markers, billing keys and the home the user scope names are
// replaced, the supervisor's own home and projects root are what the CEO
// works in, it keeps the launcher's Herdr session without its pane, and its
// browser session is its own whatever session the user's environment names.
func TestANativeCEOStartsFromTheUsersEnvironmentInThisHome(t *testing.T) {
	userEnv := []string{
		`Path=C:\bin`, "USERS_OWN_SETTING=kept", `GOBLINS_HOME=C:\installed-home`, `goblins_projects_root=C:\dev`,
		"CLAUDECODE=1", "CLAUDE_CODE_CHILD_SESSION=1", "HERDR_PANE_ID=w1:p1", "HERDR_SESSION=users", "ANTHROPIC_API_KEY=sk-billed",
		"chrome_devtools_axi_session=the-users-own",
		"LAVISH_AXI_HOST=100.64.1.2", "lavish_axi_link_url=https://old.example.ts.net:4388", "LAVISH_AXI_ALLOWED_HOSTS=*",
	}
	launcherEnv := []string{`Path=C:\launcher`, "CLAUDECODE=1", "HERDR_PANE_ID=w9:p0", "herdr_pane_id=w9:p1", "HERDR_SESSION=fleet"}
	h := home.Home{Root: `C:\scratch\home`, State: `C:\scratch\home\state`}

	got := nativeCEOEnvironment(userEnv, launcherEnv, h, `C:\scratch\projects`)

	want := []string{
		`Path=C:\bin`, "USERS_OWN_SETTING=kept", "HERDR_SESSION=fleet",
		`GOBLINS_HOME=C:\scratch\home`, `GOBLINS_STATE_OVERRIDE=C:\scratch\home\state`, `GOBLINS_PROJECTS_ROOT=C:\scratch\projects`,
		"CHROME_DEVTOOLS_AXI_SESSION=ceo", "LAVISH_AXI_HOST=127.0.0.1",
	}
	if !slices.Equal(got, want) {
		t.Errorf("nativeCEOEnvironment =\n%q\nwant\n%q", got, want)
	}
}

// A supervisor with no projects root of its own leaves the user's setting in
// place, and one run outside Herdr gives the CEO no Herdr session.
func TestANativeCEOLeavesUnsetWhatTheSupervisorDoesNotSet(t *testing.T) {
	got := nativeCEOEnvironment([]string{`Path=C:\bin`, `GOBLINS_PROJECTS_ROOT=C:\dev`, "HERDR_SESSION=users"}, []string{`Path=C:\launcher`}, home.Home{Root: `C:\home`, State: `C:\home\state`}, "")

	want := []string{`Path=C:\bin`, `GOBLINS_PROJECTS_ROOT=C:\dev`, `GOBLINS_HOME=C:\home`, `GOBLINS_STATE_OVERRIDE=C:\home\state`, "CHROME_DEVTOOLS_AXI_SESSION=ceo", "LAVISH_AXI_HOST=127.0.0.1"}
	if !slices.Equal(got, want) {
		t.Errorf("nativeCEOEnvironment = %q, want %q", got, want)
	}
}

// A CEO a goblin's rig starts in a scratch home keeps the codex guard the rig
// runs under first on its PATH, so a scratch CEO on Codex meets that guard
// while Codex would bill credits, as the rig's own codex does.
func TestANativeCEOKeepsTheCodexGuardItsLauncherRunsUnder(t *testing.T) {
	// Arrange
	userEnv := []string{`Path=C:\bin`, "USERS_OWN_SETTING=kept"}
	launcherEnv := []string{`Path=C:\launcher`, `goblins_codex_guard=C:\fleet\state\codex-guard\g1;C:\fleet\state\codex-guard\g1`}

	// Act
	got := nativeCEOEnvironment(userEnv, launcherEnv, home.Home{Root: `C:\scratch`, State: `C:\scratch\state`}, "")

	// Assert
	want := []string{
		"USERS_OWN_SETTING=kept", `GOBLINS_HOME=C:\scratch`, `GOBLINS_STATE_OVERRIDE=C:\scratch\state`,
		`GOBLINS_CODEX_GUARD=C:\fleet\state\codex-guard\g1`, `PATH=C:\fleet\state\codex-guard\g1;C:\bin`, "CHROME_DEVTOOLS_AXI_SESSION=ceo", "LAVISH_AXI_HOST=127.0.0.1",
	}
	if !slices.Equal(got, want) {
		t.Errorf("nativeCEOEnvironment =\n%q\nwant\n%q", got, want)
	}
}

// fakeClaudeArguments is the file the test binary, run as claude.exe in a
// native terminal, writes its arguments to, one a line, in its working
// directory.
const fakeClaudeArguments = "claude-arguments.txt"

// A native terminal starts its program itself, with no shell to run a script
// shim, so a claude found only as a script is refused before any host starts.
func TestANativeCEOIsNotStartedFromAScriptShim(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude.cmd"), []byte("@echo off\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	stateDir := t.TempDir()

	err := startNativeCEO(home.Home{Root: filepath.Dir(stateDir), State: stateDir}, t.TempDir(), "claude", nil, "")

	if err == nil || !strings.Contains(err.Error(), "not a program a native terminal can start") {
		t.Fatalf("startNativeCEO error = %v, want the script shim refused", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "hosts")); !os.IsNotExist(err) {
		t.Errorf("hosts directory stat = %v, want no host started", err)
	}
}
