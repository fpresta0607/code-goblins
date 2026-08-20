package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/install"
)

// TestHookFamilyEndToEnd is Task 13's whole-family proof: it builds the real
// cfo.exe once, drives it exactly as Claude Code would (a subprocess reading
// stdin JSON), and checks every contract Tasks 1-12 shipped from outside the
// package. This is the only test in the repo that never calls runHook or any
// internal package directly.
//
// Four phases, in this order, none parallel (t.Parallel is deliberately
// never called; each phase's fixtures are self-contained rather than
// depending on execution order):
//  1. Seven invocations against a genuine primary home (the brief's table).
//  2. The inertness proof: the same seven invocations against a bare dev
//     home must be silent no-ops, verified by a recursive directory diff.
//  3. The exact six command strings cfo install registers, run through the
//     POSIX shell Step 3a identified, against both homes.
//  4. A timing sweep of the four hooks Global Constraints budgets.
func TestHookFamilyEndToEnd(t *testing.T) {
	goBin := resolveGoBin(t)
	repoRoot := repoRootFromCmdCFO(t)
	exe := buildCFOBinary(t, goBin, repoRoot)
	buildDir := filepath.Dir(exe)

	sessionStartPayload := hookPayload(t, "s1", "startup", "", "")
	subagentPayload := hookPayload(t, "s1", "", "Agent", "")
	armDenyPayload := hookPayload(t, "s1", "", "Bash", "cfo watch &")
	cdDenyPayload := hookPayload(t, "s1", "", "Bash", `cd C:\`)
	turnendPayload := hookPayload(t, "s1", "", "", "")
	stopAutoarmPayload := hookPayload(t, "s1", "", "", "")
	armAllowPayload := hookPayload(t, "s1", "", "Bash", "git log --oneline")
	cdAllowPayload := hookPayload(t, "s1", "", "Bash", "go test ./...")
	subagentAllowPayload := hookPayload(t, "s1", "", "Read", "")

	// --- Phase 1: seven invocations against a genuine primary home ---

	// The registered commands resolve cfo.exe through CFO_HOME, so a home
	// that stands in for a real $CFO_HOME has to hold the binary.
	sharedHome := homeWithBinary(t, exe)

	t.Run("case1 session-start full compose", func(t *testing.T) {
		home := newPrimaryHome(t)
		res := runHookBinary(t, exe, "session-start", sessionStartPayload, buildEnv(map[string]string{"CFO_HOME": home}))
		assertExit(t, res, 0, "session-start")
		assertEmptyStderr(t, res, "session-start")
		assertHasHeaders(t, res.stdout, "session-start")
	})

	t.Run("case2 pretool-subagent deny", func(t *testing.T) {
		res := runHookBinary(t, exe, "pretool-subagent", subagentPayload, buildEnv(map[string]string{"CFO_HOME": sharedHome}))
		assertDeny(t, res, "", "pretool-subagent")
	})

	t.Run("case3 pretool-arm deny watcher-background", func(t *testing.T) {
		res := runHookBinary(t, exe, "pretool-arm", armDenyPayload, buildEnv(map[string]string{"CFO_HOME": sharedHome}))
		assertDeny(t, res, "watcher-background", "pretool-arm")
	})

	t.Run("case4 pretool-cd deny cwd-relocation", func(t *testing.T) {
		res := runHookBinary(t, exe, "pretool-cd", cdDenyPayload, buildEnv(map[string]string{"CFO_HOME": sharedHome}))
		assertDeny(t, res, "cwd-relocation", "pretool-cd")
	})

	t.Run("case5 turnend-guard blind block", func(t *testing.T) {
		home := newPrimaryHome(t)
		writeMetaFixture(t, filepath.Join(home, "state"), "g1.meta")
		res := runHookBinary(t, exe, "turnend-guard", turnendPayload, buildEnv(map[string]string{
			"CFO_HOME":                        home,
			"CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS": "1",
		}))
		assertBlock(t, res, "TURN WOULD END BLIND", "turnend-guard")
	})

	t.Run("case6 stop-autoarm rewake", func(t *testing.T) {
		home := newPrimaryHome(t)
		state := filepath.Join(home, "state")
		writeMetaFixture(t, state, "g1.meta")
		done := statusApendAfter(state, "g1.status", 300*time.Millisecond)
		res := runHookBinary(t, exe, "stop-autoarm", stopAutoarmPayload, buildEnv(map[string]string{
			"CFO_HOME":                    home,
			"CFO_TEST_ANCESTOR_PID":       strconv.Itoa(os.Getpid()),
			"CFO_POLL":                    "1",
			"CFO_SIGNAL_GRACE":            "1",
			"CFO_HEARTBEAT":               "1",
			"CFO_CLAUDE_AUTOARM_ATTEMPTS": "1",
		}))
		<-done
		assertBlock(t, res, "cfo watcher wake", "stop-autoarm")
	})

	t.Run("case7 pretool-arm allow", func(t *testing.T) {
		res := runHookBinary(t, exe, "pretool-arm", armAllowPayload, buildEnv(map[string]string{"CFO_HOME": sharedHome}))
		assertSilentZero(t, res, "pretool-arm allow")
	})

	// --- Phase 2: the inertness proof ---

	t.Run("inertness proof against dev home", func(t *testing.T) {
		devHome := newDevHome(t)
		before, err := recursiveListing(devHome)
		if err != nil {
			t.Fatal(err)
		}

		type inertCase struct {
			name     string
			hookName string
			stdin    string
			env      map[string]string
		}
		cases := []inertCase{
			{"session-start", "session-start", sessionStartPayload, nil},
			{"pretool-subagent", "pretool-subagent", subagentPayload, nil},
			{"pretool-arm (deny shape)", "pretool-arm", armDenyPayload, nil},
			{"pretool-cd (deny shape)", "pretool-cd", cdDenyPayload, nil},
			{"turnend-guard", "turnend-guard", turnendPayload, map[string]string{"CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS": "1"}},
			{"stop-autoarm", "stop-autoarm", stopAutoarmPayload, map[string]string{
				"CFO_TEST_ANCESTOR_PID":       strconv.Itoa(os.Getpid()),
				"CFO_POLL":                    "1",
				"CFO_SIGNAL_GRACE":            "1",
				"CFO_HEARTBEAT":               "1",
				"CFO_CLAUDE_AUTOARM_ATTEMPTS": "1",
			}},
			{"pretool-arm (allow shape)", "pretool-arm", armAllowPayload, nil},
		}

		// Guards against a shrunk table (a deleted row silently running the
		// loop fewer than seven times with no comment anywhere saying so). A
		// short-circuit mid-loop is a different failure mode already covered
		// by Go's own testing semantics: assertSilentZero's t.Errorf calls
		// never abort the loop, so every row always runs and reports on its
		// own; nothing here needs to additionally count executions to prove
		// that.
		if len(cases) != 7 {
			t.Fatalf("inertness table has %d rows, want exactly 7", len(cases))
		}
		for _, c := range cases {
			env := buildEnv(mergeEnv(map[string]string{"CFO_HOME": devHome}, c.env))
			res := runHookBinary(t, exe, c.hookName, c.stdin, env)
			assertSilentZero(t, res, c.name+" against dev home")
		}

		after, err := recursiveListing(devHome)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			added, removed := diffListings(before, after)
			t.Fatalf("dev home directory listing changed (INERT MEANS INERT violated): added=%v removed=%v\nbefore=%v\nafter=%v", added, removed, before, after)
		}
	})

	// --- Phase 3: the exact registered command strings, through the shell Step 3a identified ---

	t.Run("registered command strings via the POSIX shell", func(t *testing.T) {
		bashPath := resolveMingwBash(t)

		commands := loadRegisteredCommands(t, repoRoot)
		wantNames := []string{"session-start", "pretool-arm", "pretool-cd", "pretool-subagent", "turnend-guard", "stop-autoarm"}
		if len(commands) != len(wantNames) {
			t.Fatalf("cfo install registered %d recognizable hook commands, want %d: %v", len(commands), len(wantNames), commands)
		}
		for _, name := range wantNames {
			if _, ok := commands[name]; !ok {
				t.Fatalf("cfo install is missing a registered command for %q", name)
			}
		}

		// The brief calls out two fields as preserved verbatim from upstream:
		// SessionStart's 120s timeout, and stop-autoarm's asyncRewake plus its
		// 28800s (8h) timeout. Pin both so a future edit that drops either
		// fails here instead of silently shipping.
		if got := commands["session-start"].Timeout; got != 120 {
			t.Errorf("session-start hook: timeout = %d, want 120", got)
		}
		if got := commands["stop-autoarm"]; !got.AsyncRewake || got.Timeout != 28800 {
			t.Errorf("stop-autoarm hook: asyncRewake=%v timeout=%d, want true/28800", got.AsyncRewake, got.Timeout)
		}

		baseEnv := func(home string, extra map[string]string) []string {
			return buildEnv(mergeEnv(map[string]string{"CFO_HOME": home, "CLAUDE_PROJECT_DIR": buildDir}, extra))
		}

		t.Run("session-start", func(t *testing.T) {
			home := homeWithBinary(t, exe)
			res := runViaShell(t, bashPath, commands["session-start"].Command, sessionStartPayload, baseEnv(home, nil))
			assertExit(t, res, 0, "session-start via shell (primary)")
			assertEmptyStderr(t, res, "session-start via shell (primary)")
			assertHasHeaders(t, res.stdout, "session-start via shell (primary)")

			devHome := newDevHome(t)
			devRes := runViaShell(t, bashPath, commands["session-start"].Command, sessionStartPayload, baseEnv(devHome, nil))
			assertSilentZero(t, devRes, "session-start via shell (dev)")
		})

		t.Run("pretool-subagent", func(t *testing.T) {
			res := runViaShell(t, bashPath, commands["pretool-subagent"].Command, subagentPayload, baseEnv(sharedHome, nil))
			assertDeny(t, res, "", "pretool-subagent via shell (primary)")

			devHome := newDevHome(t)
			devRes := runViaShell(t, bashPath, commands["pretool-subagent"].Command, subagentPayload, baseEnv(devHome, nil))
			assertSilentZero(t, devRes, "pretool-subagent via shell (dev)")
		})

		t.Run("pretool-arm", func(t *testing.T) {
			res := runViaShell(t, bashPath, commands["pretool-arm"].Command, armDenyPayload, baseEnv(sharedHome, nil))
			assertDeny(t, res, "watcher-background", "pretool-arm via shell (primary)")

			devHome := newDevHome(t)
			devRes := runViaShell(t, bashPath, commands["pretool-arm"].Command, armDenyPayload, baseEnv(devHome, nil))
			assertSilentZero(t, devRes, "pretool-arm via shell (dev)")
		})

		t.Run("pretool-cd", func(t *testing.T) {
			res := runViaShell(t, bashPath, commands["pretool-cd"].Command, cdDenyPayload, baseEnv(sharedHome, nil))
			assertDeny(t, res, "cwd-relocation", "pretool-cd via shell (primary)")

			devHome := newDevHome(t)
			devRes := runViaShell(t, bashPath, commands["pretool-cd"].Command, cdDenyPayload, baseEnv(devHome, nil))
			assertSilentZero(t, devRes, "pretool-cd via shell (dev)")
		})

		t.Run("turnend-guard", func(t *testing.T) {
			home := homeWithBinary(t, exe)
			writeMetaFixture(t, filepath.Join(home, "state"), "g1.meta")
			extra := map[string]string{"CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS": "1"}
			res := runViaShell(t, bashPath, commands["turnend-guard"].Command, turnendPayload, baseEnv(home, extra))
			assertBlock(t, res, "TURN WOULD END BLIND", "turnend-guard via shell (primary)")

			devHome := newDevHome(t)
			devRes := runViaShell(t, bashPath, commands["turnend-guard"].Command, turnendPayload, baseEnv(devHome, extra))
			assertSilentZero(t, devRes, "turnend-guard via shell (dev)")
		})

		t.Run("stop-autoarm", func(t *testing.T) {
			home := homeWithBinary(t, exe)
			state := filepath.Join(home, "state")
			writeMetaFixture(t, state, "g1.meta")
			extra := map[string]string{
				"CFO_TEST_ANCESTOR_PID":       strconv.Itoa(os.Getpid()),
				"CFO_POLL":                    "1",
				"CFO_SIGNAL_GRACE":            "1",
				"CFO_HEARTBEAT":               "1",
				"CFO_CLAUDE_AUTOARM_ATTEMPTS": "1",
			}
			done := statusApendAfter(state, "g1.status", 300*time.Millisecond)
			res := runViaShell(t, bashPath, commands["stop-autoarm"].Command, stopAutoarmPayload, baseEnv(home, extra))
			<-done
			assertBlock(t, res, "cfo watcher wake", "stop-autoarm via shell (primary)")

			devHome := newDevHome(t)
			devRes := runViaShell(t, bashPath, commands["stop-autoarm"].Command, stopAutoarmPayload, baseEnv(devHome, extra))
			assertSilentZero(t, devRes, "stop-autoarm via shell (dev)")
		})

		// Important 1 (review): every one of these subtests so far resolves
		// cfo.exe successfully - the present-binary branch of every
		// "[ -x ... ] || exit 0" guard. That is NOT the state a fresh clone
		// is in: cfo.exe is git-ignored, so until it is built every session
		// takes the ABSENT branch of all six guards. This subtest proves
		// that branch directly: neither CFO_HOME nor CLAUDE_PROJECT_DIR
		// holds a binary, against a primary home that already has goblin
		// work in flight (so an un-guarded hook would visibly deny, block,
		// or rewake if the guard did not stop it first), and every one of
		// the six exact registered command strings must still exit 0 with
		// both streams empty.
		t.Run("absent binary guard (no cfo.exe to resolve)", func(t *testing.T) {
			emptyDir := t.TempDir()
			home := newPrimaryHome(t)
			state := filepath.Join(home, "state")
			writeMetaFixture(t, state, "g1.meta")
			env := func(extra map[string]string) []string {
				return buildEnv(mergeEnv(map[string]string{"CFO_HOME": home, "CLAUDE_PROJECT_DIR": emptyDir}, extra))
			}

			absentCases := []struct {
				name  string
				stdin string
				env   map[string]string
			}{
				{"session-start", sessionStartPayload, nil},
				{"pretool-subagent", subagentPayload, nil},
				{"pretool-arm", armDenyPayload, nil},
				{"pretool-cd", cdDenyPayload, nil},
				{"turnend-guard", turnendPayload, map[string]string{"CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS": "1"}},
				{"stop-autoarm", stopAutoarmPayload, map[string]string{
					"CFO_TEST_ANCESTOR_PID":       strconv.Itoa(os.Getpid()),
					"CFO_POLL":                    "1",
					"CFO_SIGNAL_GRACE":            "1",
					"CFO_HEARTBEAT":               "1",
					"CFO_CLAUDE_AUTOARM_ATTEMPTS": "1",
				}},
			}
			if len(absentCases) != 6 {
				t.Fatalf("absent-binary table has %d rows, want exactly 6 (one per registered command)", len(absentCases))
			}
			for _, c := range absentCases {
				res := runViaShell(t, bashPath, commands[c.name].Command, c.stdin, env(c.env))
				assertSilentZero(t, res, c.name+" via shell (cfo.exe absent)")
			}
		})

		// This is the whole point of installing at user scope: a session
		// opened in some OTHER repository - one with no cfo.exe of its own,
		// which is every repository - must still be supervised. Every hook
		// here has CLAUDE_PROJECT_DIR pointing at a project directory with
		// no binary in it, so the only thing that can resolve cfo.exe is
		// CFO_HOME, and each command must do the same thing it does inside
		// code-goblins.
		t.Run("a session in a different repo (no cfo.exe at CLAUDE_PROJECT_DIR)", func(t *testing.T) {
			otherRepo := t.TempDir()
			// One fresh home per hook, exactly as the subtests above do:
			// several of these hooks write supervision state, and reusing a
			// home would let one firing decide the next one's outcome.
			env := func(home string, extra map[string]string) []string {
				return buildEnv(mergeEnv(map[string]string{"CFO_HOME": home, "CLAUDE_PROJECT_DIR": otherRepo}, extra))
			}

			home := homeWithBinary(t, exe)
			res := runViaShell(t, bashPath, commands["session-start"].Command, sessionStartPayload, env(home, nil))
			assertExit(t, res, 0, "session-start in a different repo")
			assertHasHeaders(t, res.stdout, "session-start in a different repo")

			home = homeWithBinary(t, exe)
			writeMetaFixture(t, filepath.Join(home, "state"), "g1.meta")
			res = runViaShell(t, bashPath, commands["pretool-subagent"].Command, subagentPayload, env(home, nil))
			assertDeny(t, res, "", "pretool-subagent in a different repo")

			res = runViaShell(t, bashPath, commands["pretool-arm"].Command, armDenyPayload, env(home, nil))
			assertDeny(t, res, "watcher-background", "pretool-arm in a different repo")

			res = runViaShell(t, bashPath, commands["pretool-cd"].Command, cdDenyPayload, env(home, nil))
			assertDeny(t, res, "cwd-relocation", "pretool-cd in a different repo")

			home = homeWithBinary(t, exe)
			writeMetaFixture(t, filepath.Join(home, "state"), "g1.meta")
			res = runViaShell(t, bashPath, commands["turnend-guard"].Command, turnendPayload,
				env(home, map[string]string{"CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS": "1"}))
			assertBlock(t, res, "TURN WOULD END BLIND", "turnend-guard in a different repo")

			home = homeWithBinary(t, exe)
			state := filepath.Join(home, "state")
			writeMetaFixture(t, state, "g1.meta")
			autoarmExtra := map[string]string{
				"CFO_TEST_ANCESTOR_PID":       strconv.Itoa(os.Getpid()),
				"CFO_POLL":                    "1",
				"CFO_SIGNAL_GRACE":            "1",
				"CFO_HEARTBEAT":               "1",
				"CFO_CLAUDE_AUTOARM_ATTEMPTS": "1",
			}
			done := statusApendAfter(state, "g1.status", 300*time.Millisecond)
			res = runViaShell(t, bashPath, commands["stop-autoarm"].Command, stopAutoarmPayload, env(home, autoarmExtra))
			<-done
			assertBlock(t, res, "cfo watcher wake", "stop-autoarm in a different repo")
		})

		// The other side of moving the hooks to user scope: a goblin pane
		// now inherits them too, so the role stamp is the only thing keeping
		// the CFO's supervision out of the work it is supervising. Same
		// primary home and same in-flight goblin as the subtest above, where
		// every one of these six denied, blocked, or printed a digest.
		t.Run("a goblin pane receives no hook", func(t *testing.T) {
			home := homeWithBinary(t, exe)
			writeMetaFixture(t, filepath.Join(home, "state"), "g1.meta")
			goblinEnv := func(extra map[string]string) []string {
				return buildEnv(mergeEnv(map[string]string{
					"CFO_HOME":           home,
					"CLAUDE_PROJECT_DIR": home,
					"CFO_ROLE":           "goblin",
				}, extra))
			}

			goblinCases := []struct {
				name  string
				stdin string
				env   map[string]string
			}{
				{"session-start", sessionStartPayload, nil},
				{"pretool-subagent", subagentPayload, nil},
				{"pretool-arm", armDenyPayload, nil},
				{"pretool-cd", cdDenyPayload, nil},
				{"turnend-guard", turnendPayload, map[string]string{"CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS": "1"}},
				{"stop-autoarm", stopAutoarmPayload, map[string]string{
					"CFO_TEST_ANCESTOR_PID":       strconv.Itoa(os.Getpid()),
					"CFO_POLL":                    "1",
					"CFO_SIGNAL_GRACE":            "1",
					"CFO_HEARTBEAT":               "1",
					"CFO_CLAUDE_AUTOARM_ATTEMPTS": "1",
				}},
			}
			if len(goblinCases) != len(wantNames) {
				t.Fatalf("goblin table has %d rows, want one per registered command (%d)", len(goblinCases), len(wantNames))
			}
			for _, c := range goblinCases {
				res := runViaShell(t, bashPath, commands[c.name].Command, c.stdin, goblinEnv(c.env))
				assertSilentZero(t, res, c.name+" via shell (goblin pane)")
			}
		})
	})

	// --- Phase 4: timing sweep, Global Constraints budgets ---

	t.Run("timing budgets", func(t *testing.T) {
		timingHome := newPrimaryHome(t)
		env := buildEnv(map[string]string{"CFO_HOME": timingHome})

		preToolCases := []struct {
			hookName string
			stdin    string
		}{
			{"pretool-arm", armAllowPayload},
			{"pretool-cd", cdAllowPayload},
			{"pretool-subagent", subagentAllowPayload},
		}
		for _, c := range preToolCases {
			durs := make([]time.Duration, 20)
			for i := range durs {
				start := time.Now()
				runHookBinary(t, exe, c.hookName, c.stdin, env)
				durs[i] = time.Since(start)
			}
			min := minDuration(durs)
			t.Logf("%s: min=%v over %d runs (median %v) (%v)", c.hookName, min, len(durs), median(durs), durs)
			if min > 150*time.Millisecond {
				t.Errorf("%s min = %v, want <= 150ms (Global Constraints budget)", c.hookName, min)
			}
		}

		durs := make([]time.Duration, 20)
		for i := range durs {
			start := time.Now()
			runHookBinary(t, exe, "session-start", sessionStartPayload, env)
			durs[i] = time.Since(start)
		}
		min := minDuration(durs)
		t.Logf("session-start: min=%v over %d runs (median %v) (%v)", min, len(durs), median(durs), durs)
		if min > time.Second {
			t.Errorf("session-start min = %v, want <= 1s (Global Constraints budget)", min)
		}
	})
}

// --- fixtures ---

// newDevHome creates a bare dev checkout: AGENTS.md and a plain git init,
// deliberately no state\, mirroring this repo's own real dev-checkout shape
// (see CLAUDE.md / the task brief's environment note). IsPrimary is false
// here on the state\ conjunct alone.
func newDevHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# dev checkout"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// statusApendAfter starts a goroutine that writes a decision status line to
// state\<name> after delay, standing in for a goblin reporting a decision the
// CFO must wake for, and returns a channel closed once the write is done.
// Callers that start a hook invocation expecting to observe this write must
// receive from the channel before asserting, so the goroutine's write is never
// left racing test teardown.
func statusApendAfter(state, name string, delay time.Duration) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(delay)
		_ = os.WriteFile(filepath.Join(state, name), []byte("needs-decision: rewake\n"), 0o644)
	}()
	return done
}

// --- payload construction ---

type e2eToolInput struct {
	Command string `json:"command"`
}

type e2ePayload struct {
	SessionID string        `json:"session_id"`
	Source    string        `json:"source,omitempty"`
	ToolName  string        `json:"tool_name,omitempty"`
	ToolInput *e2eToolInput `json:"tool_input,omitempty"`
}

// hookPayload marshals Claude Code's hook JSON shape rather than building it
// by string concatenation, so a Windows command containing a backslash (case
// 4's `cd C:\`) is escaped correctly without hand-written JSON quoting.
func hookPayload(t *testing.T, sessionID, source, toolName, command string) string {
	t.Helper()
	p := e2ePayload{SessionID: sessionID, Source: source, ToolName: toolName}
	if command != "" {
		p.ToolInput = &e2eToolInput{Command: command}
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(data)
}

// --- binary build ---

// resolveGoBin locates the go toolchain at runtime.GOROOT()\bin\go.exe,
// skipping (not failing) only when the toolchain itself is absent - the
// brief's own carve-out, distinct from every other failure mode in this
// file, which fails the test.
func resolveGoBin(t *testing.T) string {
	t.Helper()
	goroot := runtime.GOROOT()
	if goroot == "" {
		t.Skip("runtime.GOROOT() is empty; cannot locate the go toolchain")
	}
	goBin := filepath.Join(goroot, "bin", "go.exe")
	if _, err := os.Stat(goBin); err != nil {
		t.Skip("go toolchain not found at " + goBin)
	}
	return goBin
}

// repoRootFromCmdCFO resolves the repository root from this test's working
// directory, cmd\cfo, per the brief's filepath.Join(wd, "..", "..") recipe.
func repoRootFromCmdCFO(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..")
}

// buildCFOBinary builds the real cfo.exe once via `go build -o <exe>
// ./cmd/cfo` with cmd.Dir set to repoRoot, exactly the package pattern the
// brief requires (a `./...` pattern is rejected outright: it matches many
// packages and -o cannot target a directory). A build failure fails the test
// with the compiler's own CombinedOutput text, not a confusing downstream
// assertion.
func buildCFOBinary(t *testing.T, goBin, repoRoot string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "cfo.exe")
	cmd := exec.Command(goBin, "build", "-o", exe, "./cmd/cfo")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build -o %s ./cmd/cfo failed: %v\n%s", exe, err, out)
	}
	return exe
}

// --- registered-command extraction (Step 3c) ---

type settingsHookEntry struct {
	Type        string `json:"type"`
	Command     string `json:"command"`
	Timeout     int    `json:"timeout"`
	AsyncRewake bool   `json:"asyncRewake"`
}

type settingsHookGroup struct {
	Matcher string              `json:"matcher"`
	Hooks   []settingsHookEntry `json:"hooks"`
}

type settingsFile struct {
	Hooks struct {
		SessionStart []settingsHookGroup `json:"SessionStart"`
		PreToolUse   []settingsHookGroup `json:"PreToolUse"`
		Stop         []settingsHookGroup `json:"Stop"`
	} `json:"hooks"`
}

// registeredHook is one settings.json hook entry, keyed by the cfo hook name
// its command string invokes. Timeout and AsyncRewake are carried through
// (not just Command) so a future edit that silently drops SessionStart's
// timeout or stop-autoarm's asyncRewake/28800s timeout - both called out as
// preserved verbatim by the brief - fails this test instead of going
// unnoticed.
type registeredHook struct {
	Command     string
	Timeout     int
	AsyncRewake bool
}

// inertEnvStore is the user-scope environment as a no-op, so building the
// settings file under test never reads or writes the machine's registry.
type inertEnvStore struct{}

func (inertEnvStore) Get(string) (string, bool, error) { return "", false, nil }
func (inertEnvStore) Set(string, string) error         { return nil }
func (inertEnvStore) Unset(string) error               { return nil }
func (inertEnvStore) Broadcast() error                 { return nil }

// loadRegisteredCommands runs the real installer into a temp settings file
// and reads the six cfo hook entries back out of it. Reading the file the
// installer actually writes (rather than re-deriving the six strings from a
// parallel constant in this test) means this step always exercises what is
// really wired, and an installer edit that drops or renames a hook fails
// here loudly instead of silently testing stale strings.
func loadRegisteredCommands(t *testing.T, repoRoot string) map[string]registeredHook {
	t.Helper()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	service := install.Service{
		Root:         repoRoot,
		UserSettings: settingsPath,
		RepoSettings: filepath.Join(t.TempDir(), "absent.json"),
		Env:          inertEnvStore{},
	}
	if err := service.Install(io.Discard); err != nil {
		t.Fatalf("install into %s: %v", settingsPath, err)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read the installed settings: %v", err)
	}
	var sf settingsFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("parse the installed settings: %v", err)
	}

	commands := make(map[string]registeredHook)
	names := []string{"session-start", "pretool-arm", "pretool-cd", "pretool-subagent", "turnend-guard", "stop-autoarm"}
	record := func(entry settingsHookEntry) {
		for _, name := range names {
			if strings.Contains(entry.Command, "hook "+name) {
				commands[name] = registeredHook{Command: entry.Command, Timeout: entry.Timeout, AsyncRewake: entry.AsyncRewake}
			}
		}
	}
	for _, g := range sf.Hooks.SessionStart {
		for _, h := range g.Hooks {
			record(h)
		}
	}
	for _, g := range sf.Hooks.PreToolUse {
		for _, h := range g.Hooks {
			record(h)
		}
	}
	for _, g := range sf.Hooks.Stop {
		for _, h := range g.Hooks {
			record(h)
		}
	}
	return commands
}

// homeWithBinary is a primary fleet home that also holds cfo.exe, which is
// what a real $CFO_HOME looks like and what lets a session in another
// repository resolve the binary at all.
func homeWithBinary(t *testing.T, exe string) string {
	t.Helper()
	home := newPrimaryHome(t)
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "cfo.exe"), data, 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

// --- subprocess execution ---

type hookResult struct {
	exit   int
	stdout string
	stderr string
}

// runCmd executes cmd (stdin/env already set by the caller), capturing
// stdout and stderr separately (never CombinedOutput, since the seven-row
// table and the inertness proof both assert the two streams independently).
func runCmd(t *testing.T, cmd *exec.Cmd) hookResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exit = exitErr.ExitCode()
		} else {
			t.Fatalf("exec failed (not a process exit): %v", err)
		}
	}
	return hookResult{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
}

// runHookBinary invokes the real cfo.exe directly: `cfo.exe hook <name>`.
// This is the artifact's binary contract, exercised without any shell layer.
func runHookBinary(t *testing.T, exe, hookName, stdin string, env []string) hookResult {
	t.Helper()
	cmd := exec.Command(exe, "hook", hookName)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = env
	return runCmd(t, cmd)
}

// resolveMingwBash finds a verified MSYS/MINGW bash.exe and refuses to run
// Phase 3 under anything else, because the POSIX-shell assumption behind
// every registered command string is load-bearing for this proof.
//
// exec.LookPath("bash") is not safe here: on a Windows box with WSL
// installed, "bash" on PATH frequently resolves to
// C:\WINDOWS\system32\bash.exe, which is WSL2 bash (uname -s reports
// "Linux", release "...microsoft-standard-WSL2"). WSL bash cannot resolve a
// Windows path in any form the hooks use - it needs /mnt/c/... - so every
// registered command's `[ -x "$CFO_ROOT"/cfo.exe ] || exit 0`
// guard is false, the guard takes its exit-0 branch, and all six hooks
// silently no-op. Every Phase 3 primary-home assertion then fails with
// "exit = 0, want 2" and empty streams, and depending on PATH order the
// suite passes or fails with no code change.
//
// That is a defect in this test's shell resolution, not in the production
// wiring. Under Git bash (MINGW64, the shell Claude Code actually invokes
// hooks through), CLAUDE_PROJECT_DIR passed as an ENVIRONMENT VARIABLE with
// a Windows backslash path resolves correctly, because MSYS auto-converts
// path-shaped env vars - confirmed empirically. The same path interpolated
// INLINE into script text instead of via an env var does not resolve, but
// that is a property of inline literals, not of the hooks, and is not a
// defect. So: pin bash to a verified MINGW/MSYS build here rather than
// trusting whatever LookPath("bash") happens to find first, and never
// reintroduce a bare LookPath("bash") for this proof.
func resolveMingwBash(t *testing.T) string {
	t.Helper()
	candidates := []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
	}
	if pathBash, err := exec.LookPath("bash"); err == nil {
		candidates = append(candidates, pathBash)
	}

	var disqualified []string
	for _, candidate := range candidates {
		out, err := exec.Command(candidate, "-c", "uname -s").CombinedOutput()
		if err != nil {
			disqualified = append(disqualified, candidate+" (could not run uname -s: "+err.Error()+")")
			continue
		}
		kernel := strings.TrimSpace(string(out))
		if strings.Contains(kernel, "MINGW") || strings.Contains(kernel, "MSYS") {
			t.Logf("resolved MINGW/MSYS bash for Phase 3: %s (uname -s = %q)", candidate, kernel)
			return candidate
		}
		disqualified = append(disqualified, candidate+" (uname -s = "+kernel+")")
	}

	t.Fatalf("no MINGW/MSYS bash found; every candidate was disqualified: %s. "+
		"The only bash on PATH is likely WSL, whose uname -s reports Linux; WSL bash "+
		"cannot resolve Windows paths, so the registered command strings' "+
		"`[ -x \"$CLAUDE_PROJECT_DIR\"/cfo.exe ] || exit 0` guard would silently take "+
		"its exit-0 branch and no-op all six hooks. Install Git for Windows, or put its "+
		"bash (bin\\bash.exe or usr\\bin\\bash.exe) ahead of System32 on PATH.",
		strings.Join(disqualified, "; "))
	return ""
}

// runViaShell invokes script through bashPath -c, the shell Step 3a proved
// hook commands actually run under. Stdin is written directly by exec, the
// same direct byte transfer runHookBinary uses; no PowerShell layer sits
// between this test and the child process at any point, so the BOM hazard
// documented elsewhere in this plan does not apply here.
func runViaShell(t *testing.T, bashPath, script, stdin string, env []string) hookResult {
	t.Helper()
	cmd := exec.Command(bashPath, "-c", script)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = env
	return runCmd(t, cmd)
}

// --- environment construction ---

// buildEnv starts from the current process's real environment (so PATH,
// SystemRoot, and everything else a spawned git.exe or bash.exe needs on
// Windows survives) and overrides only the keys named in overrides,
// case-insensitively, matching Windows' own case-insensitive environment
// block semantics.
func buildEnv(overrides map[string]string) []string {
	upper := make(map[string]bool, len(overrides))
	for k := range overrides {
		upper[strings.ToUpper(k)] = true
	}
	base := os.Environ()
	result := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if upper[strings.ToUpper(key)] {
			continue
		}
		result = append(result, kv)
	}
	for k, v := range overrides {
		result = append(result, k+"="+v)
	}
	return result
}

func mergeEnv(maps ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}

// --- assertions ---

var sevenDigestHeaders = []string{
	"== SESSION LOCK ==", "== WAKE QUEUE ==", "== SUPERVISION OPERATING INSTRUCTIONS ==",
	"== READ-ONCE CONTRACT ==", "== FLEET STATE ==", "== CONTEXT ==", "== NEXT STEP ==",
}

func assertHasHeaders(t *testing.T, stdout, context string) {
	t.Helper()
	for _, h := range sevenDigestHeaders {
		if !strings.Contains(stdout, h) {
			t.Errorf("%s: stdout missing header %q:\n%s", context, h, stdout)
		}
	}
}

func assertExit(t *testing.T, res hookResult, want int, context string) {
	t.Helper()
	if res.exit != want {
		t.Errorf("%s: exit = %d, want %d; stdout=%q stderr=%q", context, res.exit, want, res.stdout, res.stderr)
	}
}

func assertEmptyStdout(t *testing.T, res hookResult, context string) {
	t.Helper()
	if res.stdout != "" {
		t.Errorf("%s: stdout = %q, want empty", context, res.stdout)
	}
}

func assertEmptyStderr(t *testing.T, res hookResult, context string) {
	t.Helper()
	if res.stderr != "" {
		t.Errorf("%s: stderr = %q, want empty", context, res.stderr)
	}
}

// assertSilentZero asserts the shared inert/allow shape: exit 0, both
// streams empty.
func assertSilentZero(t *testing.T, res hookResult, context string) {
	t.Helper()
	assertExit(t, res, 0, context)
	assertEmptyStdout(t, res, context)
	assertEmptyStderr(t, res, context)
}

type denyEnvelope struct {
	HookSpecificOutput struct {
		HookEventName      string `json:"hookEventName"`
		PermissionDecision string `json:"permissionDecision"`
	} `json:"hookSpecificOutput"`
	SystemMessage string `json:"systemMessage"`
}

// assertDeny asserts the PreToolUse deny contract: exit 2, empty stdout, and
// a stderr JSON envelope with hookEventName/permissionDecision fixed and
// systemMessage containing wantSubstr (skipped when empty).
func assertDeny(t *testing.T, res hookResult, wantSubstr, context string) {
	t.Helper()
	assertExit(t, res, 2, context)
	assertEmptyStdout(t, res, context)
	trimmed := strings.TrimRight(res.stderr, "\r\n")
	var env denyEnvelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		t.Fatalf("%s: stderr is not the deny envelope: %v; stderr=%q", context, err, res.stderr)
	}
	if env.HookSpecificOutput.HookEventName != "PreToolUse" || env.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("%s: envelope = %+v, want PreToolUse/deny", context, env)
	}
	if wantSubstr != "" && !strings.Contains(env.SystemMessage, wantSubstr) {
		t.Errorf("%s: systemMessage = %q, want it to contain %q", context, env.SystemMessage, wantSubstr)
	}
}

// assertBlock asserts the Stop block/rewake contract: exit 2, empty stdout,
// plain-text stderr containing wantSubstr.
func assertBlock(t *testing.T, res hookResult, wantSubstr, context string) {
	t.Helper()
	assertExit(t, res, 2, context)
	assertEmptyStdout(t, res, context)
	if !strings.Contains(res.stderr, wantSubstr) {
		t.Errorf("%s: stderr = %q, want it to contain %q", context, res.stderr, wantSubstr)
	}
}

// --- recursive directory listing (inertness proof) ---

func recursiveListing(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

// diffListings reports the paths present only in after (added) and only in
// before (removed), so a failure names the exact offending path rather than
// only a count mismatch.
func diffListings(before, after []string) (added, removed []string) {
	beforeSet := make(map[string]bool, len(before))
	for _, p := range before {
		beforeSet[p] = true
	}
	afterSet := make(map[string]bool, len(after))
	for _, p := range after {
		afterSet[p] = true
	}
	for _, p := range after {
		if !beforeSet[p] {
			added = append(added, p)
		}
	}
	for _, p := range before {
		if !afterSet[p] {
			removed = append(removed, p)
		}
	}
	return added, removed
}

// --- timing ---

func median(durs []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), durs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// minDuration returns the fastest observed run, the uncontended cost of the
// hook independent of shared-runner load. Timing budgets gate on this rather
// than the median so a busy CI machine cannot inflate a real regression into
// a flaky failure, while a genuine slowdown still raises the fastest run.
func minDuration(durs []time.Duration) time.Duration {
	if len(durs) == 0 {
		return 0
	}
	min := durs[0]
	for _, d := range durs[1:] {
		if d < min {
			min = d
		}
	}
	return min
}
