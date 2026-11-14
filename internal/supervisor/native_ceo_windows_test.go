package supervisor

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/terminal"
)

// recordHost writes the record of a host for terminal ceo whose program is
// childPID and whose pipe nothing serves, as a host that was killed leaves
// behind.
func recordHost(t *testing.T, stateDir string, childPID int) {
	t.Helper()
	recordHostStarted(t, stateDir, childPID, time.Now().UTC())
}

// recordHostStarted records native terminal ceo's host as recordHost does,
// as one that started at started.
func recordHostStarted(t *testing.T, stateDir string, childPID int, started time.Time) {
	t.Helper()
	var name [16]byte
	if _, err := rand.Read(name[:]); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(host.Record{ID: "ceo", Pipe: `\\.\pipe\code-goblins-host-` + hex.EncodeToString(name[:]), Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: childPID, Started: started})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", "ceo.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// registerNatively is the environment of a program in native terminal ceo.
func registerNatively(t *testing.T) {
	t.Helper()
	t.Setenv(host.IDVariable, "ceo")
}

// primaryFixture registers this test process as the CEO in native terminal
// ceo. The terminal's program records what the board types into it, and its
// prompt hook reports each line taken, so a delivery is confirmed.
func primaryFixture(t *testing.T, store *Store) (primaryRegistration, string, hostedTerminal, *CEOConnection) {
	t.Helper()
	ceo := hostTerminal(t, store.Home.State, "ceo")
	ceo.typeLine(t, "hooked")
	ceo.drawsComposer(t, "codex")
	ceo.standIn(t)
	process, err := lock.Acquire(store.Home.State)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(store.Home.State) })
	primary := primaryRegistration{Host: "ceo", Agent: "codex", Process: *process}
	data, err := json.Marshal(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Home.State, "primary.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, identity, err := decodePrimary(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return primary, identity, ceo, &CEOConnection{State: store.Home.State}
}

// registerFixture is a home with no registration and native terminal ceo,
// whose program this test process stands in for.
func registerFixture(t *testing.T) (*Store, hostedTerminal, *CEOConnection) {
	t.Helper()
	store, _ := testStore(t)
	ceo := hostTerminal(t, store.Home.State, "ceo")
	ceo.standIn(t)
	registerNatively(t)
	return store, ceo, &CEOConnection{State: store.Home.State}
}

// nativePrimary registers this test process as a CEO in native terminal ceo.
func nativePrimary(t *testing.T, stateDir string) {
	nativePrimaryAs(t, stateDir, "claude")
}

// nativePrimaryAs registers the CEO's native terminal as running agent.
func nativePrimaryAs(t *testing.T, stateDir, agent string) {
	t.Helper()
	process, err := lock.Acquire(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(stateDir) })
	data, err := json.Marshal(primaryRegistration{Host: "ceo", Agent: agent, Process: *process})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "primary.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A native registration names its terminal alone: one that also names a
// Herdr pane, or names a terminal no host could record, is refused.
func TestANativeRegistrationNamesItsTerminalAlone(t *testing.T) {
	alive := lock.Info{PID: os.Getpid(), Start: time.Now().UTC()}
	for name, registration := range map[string]struct {
		primary primaryRegistration
		valid   bool
	}{
		"a native terminal":            {primaryRegistration{Host: "ceo", Agent: "claude", Process: alive}, true},
		"a native terminal and a pane": {primaryRegistration{Host: "ceo", Agent: "claude", Target: herdr.Target{Session: "s", Pane: "w1:p1"}, Workspace: "w1", Tab: "w1:t1", Terminal: "t1", Process: alive}, false},
		"a terminal id no host takes":  {primaryRegistration{Host: "../ceo", Agent: "claude", Process: alive}, false},
		"a native terminal, no agent":  {primaryRegistration{Host: "ceo", Process: alive}, false},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(registration.primary)
			if err != nil {
				t.Fatal(err)
			}

			_, _, err = decodePrimary(bytes.NewReader(data))

			if (err == nil) != registration.valid {
				t.Errorf("decodePrimary error = %v, want valid %v", err, registration.valid)
			}
		})
	}
}

// A host that was killed leaves its record behind, so registering in its
// terminal needs the host to answer on its pipe.
func TestRegisterRefusesANativeTerminalWhoseHostDoesNotAnswer(t *testing.T) {
	stateDir := t.TempDir()
	registerNatively(t)
	recordHost(t, stateDir, os.Getpid())

	_, err := Register(stateDir, "claude", "session-1")

	if err == nil || !strings.Contains(err.Error(), "does not answer") {
		t.Fatalf("Register error = %v, want the host that does not answer", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "primary.json")); !os.IsNotExist(err) {
		t.Errorf("primary.json stat = %v, want no registration written", err)
	}
}

// A variable a process merely inherited registers nothing: the terminal's
// program must be one of the registering process's own ancestors.
func TestRegisterRefusesANativeTerminalItDoesNotRunUnder(t *testing.T) {
	stateDir := t.TempDir()
	registerNatively(t)
	// The System process is no user process's ancestor.
	recordHost(t, stateDir, 4)

	_, err := Register(stateDir, "claude", "session-1")

	if err == nil || !strings.Contains(err.Error(), "does not run under it") {
		t.Fatalf("Register error = %v, want this process refused for not running under the terminal", err)
	}
}

// A native registration stays verified while its terminal's host record names
// the registered process as the terminal's program, and not after.
func TestANativeRegistrationIsVerifiedOnlyWhileItsTerminalRunsIt(t *testing.T) {
	stateDir := t.TempDir()
	nativePrimary(t, stateDir)
	c := &CEOConnection{State: stateDir}

	recordHost(t, stateDir, os.Getpid())
	if err := c.check(); err != nil {
		t.Fatalf("check with the terminal running the CEO = %v, want nil", err)
	}
	recordHost(t, stateDir, 4)
	if err := c.check(); err == nil || !strings.Contains(err.Error(), "ended or runs another program") {
		t.Errorf("check with the terminal running another program = %v, want the registration refused", err)
	}
	if err := os.Remove(filepath.Join(stateDir, "hosts", "ceo.json")); err != nil {
		t.Fatal(err)
	}
	if err := c.check(); err == nil || !strings.Contains(err.Error(), "ended or runs another program") {
		t.Errorf("check with the terminal ended = %v, want the registration refused", err)
	}
}

// The program in a live native terminal registers as the CEO: its host told
// it which terminal it runs in, the terminal's program is its own process,
// and the host answers.
func TestAProgramInALiveNativeTerminalRegistersAsTheCEO(t *testing.T) {
	stateDir := t.TempDir()
	ceo := hostTerminal(t, stateDir, "ceo")
	record, err := host.ReadRecord(stateDir, "ceo")
	if err != nil {
		t.Fatal(err)
	}

	ceo.typeLine(t, "register")

	want := fmt.Sprintf("registered claude pid %d in native terminal ceo", record.ChildPID)
	if lines := ceo.waitForLines(t, 1); len(lines) != 1 || lines[0] != want {
		t.Fatalf("the program recorded %q, want %q", lines, want)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "primary.json"))
	if err != nil {
		t.Fatal(err)
	}
	primary, _, err := decodePrimary(bytes.NewReader(data))
	if err != nil || primary.Host != "ceo" || primary.Agent != "claude" || primary.Process.PID != record.ChildPID {
		t.Errorf("primary.json = %s, %v; want the terminal's program registered in native terminal ceo", data, err)
	}
}

// Codex and pi install as npm script shims, which a native terminal runs
// through cmd /c by their paths, so cmd is the terminal's program: plain goblins
// register, as a CEO runs it, names the harness from the base name of the
// script cmd runs, and refuses any name that is not a harness.
func TestAHarnessRunThroughItsShimRegistersByTheNameCmdRuns(t *testing.T) {
	for name, want := range map[string]string{
		"codex":       "registered codex pid %d in native terminal ceo",
		"pi":          "registered pi pid %d in native terminal ceo",
		"goblin-tool": "register error: native terminal ceo runs cmd.exe /c %s, which is not a harness the board delivers to",
	} {
		t.Run(name, func(t *testing.T) {
			stateDir := t.TempDir()
			ceo, record := shimTerminal(t, stateDir, name)

			ceo.typeLine(t, "register by program")

			if strings.Contains(want, "%d") {
				want = fmt.Sprintf(want, record.ChildPID)
			} else if shim, err := exec.LookPath(name); err != nil {
				t.Fatal(err)
			} else {
				want = fmt.Sprintf(want, shim)
			}
			if lines := ceo.waitForLines(t, 1); len(lines) != 1 || lines[0] != want {
				t.Fatalf("the program recorded %q, want %q", lines, want)
			}
		})
	}
}

// shimTerminal runs name, an npm script shim on PATH, in native terminal ceo
// of stateDir through cmd /c, as a native terminal runs Codex and pi.
func shimTerminal(t *testing.T, stateDir, name string) (hostedTerminal, host.Record) {
	t.Helper()
	t.Setenv("HERDR_PANE_ID", "")
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shims := t.TempDir()
	typed := filepath.Join(t.TempDir(), "typed.txt")
	shim := fmt.Sprintf("@\"%s\" \"-test.run=^TestNativeTerminalProgram$\" -- native-terminal-program \"%s\" \"%s\"\r\n", program, typed, stateDir)
	if err := os.WriteFile(filepath.Join(shims, name+".cmd"), []byte(shim), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shims+string(os.PathListSeparator)+os.Getenv("PATH"))
	command, err := spawn.NativeProgram(name)
	if err != nil {
		t.Fatal(err)
	}
	ceo := hostProgram(t, stateDir, "ceo", typed, command...)
	record, err := host.ReadRecord(stateDir, "ceo")
	if err != nil {
		t.Fatal(err)
	}
	return ceo, record
}

// Plain goblins register names no session, and it is how a Codex or pi CEO that
// goblins starts registers, by its first prompt. Each gives every command it
// runs its own conversation, Codex its thread and pi its session, so the
// registration records that as the CEO's conversation, and a closed CEO comes
// back on it. No harness takes another's variable for its own: a pi CEO
// started from inside a Codex session inherits the thread.
func TestACEORegisteredByHandRecordsTheConversationItsHarnessNames(t *testing.T) {
	const thread, session = "019a2b3c-4d5e-7f60-8a9b-thread", "019a2b3c-4d5e-7f60-8a9b-session"
	for _, c := range []struct{ name, harness, piSession, want string }{
		{"codex", "codex", session, thread},
		{"pi", "pi", session, session},
		{"pi with only an inherited Codex thread", "pi", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			t.Setenv("CODEX_THREAD_ID", thread)
			t.Setenv("PI_SESSION_ID", c.piSession)
			ceo, record := shimTerminal(t, stateDir, c.harness)

			// Act
			ceo.typeLine(t, "register by hand")

			// Assert
			registered := fmt.Sprintf("registered %s pid %d in native terminal ceo", c.harness, record.ChildPID)
			if lines := ceo.waitForLines(t, 1); len(lines) != 1 || lines[0] != registered {
				t.Fatalf("the program recorded %q, want %q", lines, registered)
			}
			conversation, err := ReadCEOConversation(stateDir)
			if c.want == "" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("a %s CEO recorded the conversation %+v, %v; want none, since the thread is Codex's", c.harness, conversation, err)
				}
				return
			}
			if err != nil || conversation.Harness != c.harness || conversation.Session != c.want || conversation.Host != "ceo" || conversation.PID != record.ChildPID {
				t.Fatalf("conversation = %+v, %v; want the %s session %s recorded for native terminal ceo", conversation, err, c.harness, c.want)
			}
		})
	}
}

// A delivery to a native CEO is typed into its terminal and submitted once,
// and reported delivered once the CEO's own prompt hook, naming the terminal
// it runs in, reports taking it.
func TestADeliveryToANativeCEOIsTypedIntoItsTerminalOnce(t *testing.T) {
	stateDir := t.TempDir()
	ceo := hostTerminal(t, stateDir, "ceo")
	ceo.typeLine(t, "register")
	if lines := ceo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}
	ceo.typeLine(t, "hooked")
	ceo.showsComposer(t, "claude")

	result, err := (&CEOConnection{State: stateDir}).Send(context.Background(), registrationIdentity(t, stateDir), "hello")

	if err != nil || !strings.Contains(result.Reason, "its hook reported") {
		t.Errorf("Send = %+v, %v; want it delivered once the CEO's hook reported taking it", result, err)
	}
	if typed := ceo.exit(t); len(typed) != 2 || typed[1] != "Overlord: hello" {
		t.Errorf("the terminal received %q, want its registration and then the message once", typed)
	}
}

// A Claude Code CEO's footer names the permission mode its session runs in,
// which is the Overlord's to choose, so an answer to an idle CEO is typed at
// once in each mode Claude Code 2.1.302 draws, not queued until something
// else ends its turn.
func TestADeliveryToAnIdleNativeCEOIsTypedInEveryPermissionMode(t *testing.T) {
	for mode, footer := range map[string]string{
		"manual":             "⏸ manual mode on · ? for shortcuts",
		"plan":               "⏸ plan mode on (shift+tab to cycle)",
		"accept edits":       "⏵⏵ accept edits on (shift+tab to cycle)",
		"bypass permissions": "⏵⏵ bypass permissions on (shift+tab to cycle)",
		"don't ask":          "⏵⏵ don't ask on (shift+tab to cycle)",
		"auto":               "⏵⏵ auto mode on (shift+tab to cycle)",
	} {
		t.Run(mode, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			ceo := hostTerminal(t, stateDir, "ceo")
			ceo.typeLine(t, "register")
			if lines := ceo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
				t.Fatalf("the program recorded %q, want its registration", lines)
			}
			ceo.typeLine(t, "hooked")
			ceo.typeLine(t, "mode "+footer)
			screenShows(t, stateDir, "ceo", footer, false)

			// Act
			result, err := (&CEOConnection{State: stateDir}).Send(context.Background(), registrationIdentity(t, stateDir), "hello")

			// Assert
			if err != nil || !strings.Contains(result.Reason, "its hook reported") {
				t.Errorf("Send = %+v, %v; want it delivered once the CEO's hook reported taking it", result, err)
			}
			if typed := ceo.exit(t); len(typed) != 2 || typed[1] != "Overlord: hello" {
				t.Errorf("the terminal received %q, want its registration and then the message once", typed)
			}
		})
	}
}

// A delivery to a native CEO whose terminal has shown more than the host's
// pipe holds is still typed into it and submitted once.
func TestADeliveryToANativeCEOWithALongHistoryIsTypedIntoItsTerminalOnce(t *testing.T) {
	stateDir := t.TempDir()
	ceo := hostTerminal(t, stateDir, "ceo")
	ceo.typeLine(t, "register")
	ceo.typeLine(t, "spill")
	if lines := ceo.waitForLines(t, 2); len(lines) != 2 || !strings.HasPrefix(lines[0], "registered ") || lines[1] != "spilled" {
		t.Fatalf("the program recorded %q, want its registration and then the spill", lines)
	}
	ceo.typeLine(t, "hooked")
	ceo.showsComposer(t, "claude")

	result, err := (&CEOConnection{State: stateDir}).Send(context.Background(), registrationIdentity(t, stateDir), "hello")

	if err != nil || !strings.Contains(result.Reason, "its hook reported") {
		t.Errorf("Send = %+v, %v; want it delivered once the CEO's hook reported taking it", result, err)
	}
	if typed := ceo.exit(t); len(typed) != 3 || typed[2] != "Overlord: hello" {
		t.Errorf("the terminal received %q, want its registration, the spill and then the message once", typed)
	}
}

// A delivery to a native CEO whose terminal does not show its composer, as
// after it has printed a screenful over it, is queued with nothing typed, for
// the board to offer again. Sent again once the composer shows, it is typed
// and submitted once.
func TestADeliveryToANativeCEOWhoseComposerDoesNotShowIsQueuedAndTypedOnceItDoes(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	ceo := hostTerminal(t, stateDir, "ceo")
	ceo.typeLine(t, "register")
	ceo.typeLine(t, "spill")
	if lines := ceo.waitForLines(t, 2); len(lines) != 2 || !strings.HasPrefix(lines[0], "registered ") || lines[1] != "spilled" {
		t.Fatalf("the program recorded %q, want its registration and then the spill", lines)
	}
	connection := &CEOConnection{State: stateDir}
	identity := registrationIdentity(t, stateDir)

	// Act
	queued, queuedErr := connection.Send(context.Background(), identity, "hello")
	typedWhileQueued := ceo.lines(t)
	ceo.typeLine(t, "hooked")
	ceo.showsComposer(t, "claude")
	delivered, err := connection.Send(context.Background(), identity, "hello")

	// Assert
	if !errors.Is(queuedErr, ErrDeferred) || queued.Awaiting != nil || len(typedWhileQueued) != 2 {
		t.Errorf("with no composer showing, Send = %+v, %v and the terminal received %q; want it queued with nothing typed", queued, queuedErr, typedWhileQueued)
	}
	if err != nil || !strings.Contains(delivered.Reason, "its hook reported") {
		t.Errorf("once the composer showed, Send = %+v, %v; want it delivered once the CEO's hook reported taking it", delivered, err)
	}
	if typed := ceo.exit(t); len(typed) != 3 || typed[2] != "Overlord: hello" {
		t.Errorf("the terminal received %q, want its registration, the spill and then the message once", typed)
	}
}

// A native CEO presents from its own terminal without naming a task, proven as
// the registered primary CEO the way its questions and answers are.
func TestANativeCEOPresentsWithoutATask(t *testing.T) {
	store, h := testStore(t)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	ceo := hostTerminal(t, h.State, "ceo")
	ceo.typeLine(t, "register")
	ceo.typeLine(t, "present")

	lines := ceo.waitForLines(t, 2)

	if len(lines) != 2 || !strings.HasPrefix(lines[0], "registered ") || lines[1] != "presented" {
		t.Fatalf("the program recorded %q, want its registration and then its presentation reported", lines)
	}
	if err := store.ingestActivity(); err != nil {
		t.Fatal(err)
	}
	if activity := store.Snapshot().Activity; len(activity) != 1 || activity[0].ID != "ceo-walkthrough" || activity[0].TaskID != "" || activity[0].Target != "primary-ceo" {
		t.Fatalf("activity = %+v, want the CEO's own walkthrough, borrowing no task", activity)
	}
}

// A native CEO's command whose parent has exited, as Git Bash leaves one run
// under timeout, is still proven the registered CEO's by the proof value its
// terminal carries.
func TestANativeCEOPresentsFromAProcessWhoseParentHasExited(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PANE_ID", "")
	ceo := hostTerminal(t, h.State, "ceo")
	ceo.typeLine(t, "register")
	if lines := ceo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}

	// Act
	ceo.typeLine(t, "orphaned present")

	// Assert
	if lines := ceo.waitForLines(t, 2); len(lines) != 2 || lines[1] != "presented" {
		t.Fatalf("the program recorded %q, want its registration and then its presentation reported", lines)
	}
}

// The proof value the CEO's terminal carries proves only a process in that
// terminal. A Herdr server started from it hands the value to every pane it
// opens, and a process in such a pane is not the CEO.
func TestTheCEOsTerminalProofProvesNothingInAHerdrPane(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PANE_ID", "")
	ceo := hostTerminal(t, h.State, "ceo")
	ceo.typeLine(t, "register")
	if lines := ceo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}

	// Act
	ceo.typeLine(t, "orphaned-in-herdr present")

	// Assert
	if lines := ceo.waitForLines(t, 2); len(lines) != 2 || !strings.HasPrefix(lines[1], "present error: ") {
		t.Fatalf("the program recorded %q, want the presentation from a Herdr pane refused", lines)
	}
}

// A terminal's proof proves its program only while the process at the
// program's pid is the one the host started: once Windows gives that pid to a
// later process, the proof proves nothing about it.
func TestATerminalProofProvesOnlyTheProgramItsHostStarted(t *testing.T) {
	started, ok := proc.StartTime(os.Getpid())
	if !ok {
		t.Fatal("this process has no start time")
	}
	sum := sha256.Sum256([]byte("terminal-proof"))
	env := []string{host.IDVariable + "=ceo", host.ProofVariable + "=terminal-proof"}
	for _, c := range []struct {
		name       string
		childStart time.Time
		wantProven bool
	}{
		{"the program the host started", started, true},
		{"a later process with the program's pid", started.Add(-time.Hour), false},
		{"a record that never named the program's start", time.Time{}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			record := host.Record{ID: "ceo", ChildPID: os.Getpid(), ChildStart: c.childStart, ProofSum: hex.EncodeToString(sum[:])}

			// Act
			program, err := terminalProgram(record, env)

			// Assert
			if proven := err == nil && program.PID == os.Getpid(); proven != c.wantProven {
				t.Errorf("terminalProgram = %+v, %v; want proven %t", program, err, c.wantProven)
			}
		})
	}
}

// A native CEO's send from a process whose parent has exited still names the
// CEO as the receipt's sender, proven by the proof value its terminal carries.
func TestANativeCEOsSendFromAProcessWhoseParentHasExitedNamesItAsSender(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	makeNative(t, h.State, "task-1")
	if err := store.Accept(event(t, h, "SessionStart", "worker", "", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	id := store.db.TaskSessions["task-1"]
	node := store.db.Sessions[id]
	node.Parent = "claude/ceo-1"
	store.db.Sessions[id] = node
	store.db.Sessions[node.Parent] = Session{ID: node.Parent, NativeID: "ceo-1", Harness: "claude", Role: "ceo", Phase: "active"}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("GOBLINS_SESSION_ID", "ceo-1")
	t.Setenv("GOBLINS_SESSION_HARNESS", "claude")
	ceo := hostTerminal(t, h.State, "ceo")
	ceo.typeLine(t, "register")
	if lines := ceo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}

	// Act
	ceo.typeLine(t, "orphaned send task-1")

	// Assert
	if lines := ceo.waitForLines(t, 2); len(lines) != 2 || lines[1] != "sent" {
		t.Fatalf("the program recorded %q, want its registration and then its send", lines)
	}
	if err := store.ingestActivity(); err != nil {
		t.Fatal(err)
	}
	var sources []string
	for _, a := range store.Snapshot().Activity {
		if a.Kind == "message" {
			sources = append(sources, a.Source)
		}
	}
	if len(sources) != 1 || sources[0] != node.Parent {
		t.Errorf("message receipt sources = %q, want the CEO named as its sender", sources)
	}
}

// A delivery to a native CEO whose screen turns to work but whose prompt hook
// has not reported taking it, its input still showing the message, is sent,
// not delivered, since Enter may have chosen a dialog's option instead: it
// waits for the hook's report. A hook that reports it while the message still
// sits alone on the prompt line proves nothing either, since Claude Code runs
// that hook as it queues a line into a turn, as one its stop hook opened as
// the message arrived (#2541). Typed, it is the harness's, so it offers no
// Send now.
func TestADeliveryToANativeCEOWhoseScreenTurnsToWorkWithoutItsHookIsSentNotDelivered(t *testing.T) {
	for _, test := range []struct {
		name string
		// lines set up the terminal's program: a harness that keeps each line
		// it takes on its prompt line under a working row, and, hooked, whose
		// prompt hook reports each line it takes.
		lines []string
	}{
		{name: "its hook silent", lines: []string{"harness", "keep"}},
		{name: "its hook reporting it", lines: []string{"harness", "keep", "hooked"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			ceo := hostTerminal(t, stateDir, "ceo")
			ceo.typeLine(t, "register")
			for _, line := range test.lines {
				ceo.typeLine(t, line)
			}
			if lines := ceo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
				t.Fatalf("the program recorded %q, want its registration", lines)
			}
			ceo.showsComposer(t, "claude")

			// Act
			result, err := (&CEOConnection{State: stateDir}).Send(context.Background(), registrationIdentity(t, stateDir), "hello")

			// Assert
			if err != nil || result.Awaiting == nil || result.Awaiting.Host != "ceo" || result.Reason != sentToCEO || result.CanSendNow {
				t.Errorf("Send = %+v, %v; want it sent and awaiting the CEO's take, with no Send now, not an error", result, err)
			}
			if typed := ceo.exit(t); len(typed) != 2 || typed[1] != "Overlord: hello" {
				t.Errorf("the terminal received %q, want its registration and then the message once", typed)
			}
		})
	}
}

// A delivery to a native CEO already in a turn waits for its next tool call:
// it is typed and submitted once and reported queued in the CEO's session,
// not delivered, while nothing shows it taken.
func TestADeliveryToANativeCEOInATurnWaitsBehindIt(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, NATIVE_EXIT_PHASES_FILE), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ceo := hostTerminal(t, stateDir, "ceo")
	ceo.typeLine(t, "register")
	ceo.typeLine(t, "harness")
	ceo.typeLine(t, "busy")
	if lines := ceo.waitForLines(t, 2); len(lines) != 2 || lines[1] != "busy" {
		t.Fatalf("the program recorded %q, want its registration and then a turn under way", lines)
	}
	ceo.showsTurn(t)

	result, err := (&CEOConnection{State: stateDir}).Send(context.Background(), registrationIdentity(t, stateDir), "hello")

	if err != nil || result.Awaiting == nil || result.Awaiting.Host != "ceo" || result.Reason != heldByCEO {
		t.Errorf("Send = %+v, %v; want it queued in the CEO's session and awaiting the CEO's hook, not an error", result, err)
	}
	if typed := ceo.exit(t); len(typed) != 3 || typed[2] != "Overlord: hello" {
		t.Errorf("the terminal received %q, want the message typed once", typed)
	}
}

// The Overlord, 2026-10-28: "It should just be a direct message that gets
// queued up and sent with the clawed code session. The terminal already
// handles this." His board message to a CEO in a turn is typed into it at
// once, exactly as he wrote it, for its harness to take at its next tool
// call: never held on the board until the turn ends, and never stamped.
func TestHisMessageToANativeCEOInATurnIsTypedAtOnceAsHeWroteIt(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	if err := os.WriteFile(filepath.Join(h.State, NATIVE_EXIT_PHASES_FILE), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ceo := hostTerminal(t, h.State, "ceo")
	ceo.typeLine(t, "register")
	ceo.typeLine(t, "harness")
	ceo.typeLine(t, "busy")
	if lines := ceo.waitForLines(t, 2); len(lines) != 2 || lines[1] != "busy" {
		t.Fatalf("the program recorded %q, want its registration and then a turn under way", lines)
	}
	ceo.showsTurn(t)
	service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: h.State}}}

	// Act
	result, err := service.deliverMessage(context.Background(), Action{Kind: "message", Text: " unpause all work and complete!! "})

	// Assert
	if err != nil || result.Awaiting == nil || result.Reason != heldByCEO {
		t.Errorf("deliverMessage = %+v, %v; want it typed into the turn and awaiting the CEO's next tool call", result, err)
	}
	if typed := ceo.exit(t); len(typed) != 3 || typed[2] != "unpause all work and complete!!" {
		t.Errorf("the terminal received %q, want his words once, as he wrote them", typed)
	}
}

// His board message to an idle CEO goes in as he wrote it too, with no
// stamp before it.
func TestHisMessageToAnIdleNativeCEOIsTypedAsHeWroteIt(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	ceo := hostTerminal(t, h.State, "ceo")
	ceo.typeLine(t, "register")
	if lines := ceo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}
	ceo.typeLine(t, "hooked")
	ceo.showsComposer(t, "claude")
	service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: h.State}}}

	// Act
	result, err := service.deliverMessage(context.Background(), Action{Kind: "message", Text: "continue and complete all goblins"})

	// Assert
	if err != nil || !strings.Contains(result.Reason, "its hook reported") {
		t.Errorf("deliverMessage = %+v, %v; want it delivered once the CEO's hook reported taking it", result, err)
	}
	if typed := ceo.exit(t); len(typed) != 2 || typed[1] != "continue and complete all goblins" {
		t.Errorf("the terminal received %q, want its registration and then his words as he wrote them", typed)
	}
}

// The Overlord, 2026-10-01: "Delivery unconfirmed ... I get these command
// center blips and errors, fix". A CEO that takes a typed answer later than
// the confirmation window is not an error: the delivery is typed and
// submitted once and reported sent, awaiting the hook, never typed again.
func TestADeliveryTheNativeCEOHasNotTakenYetIsSentNotAnError(t *testing.T) {
	stateDir := t.TempDir()
	ceo := hostTerminal(t, stateDir, "ceo")
	ceo.typeLine(t, "register")
	if lines := ceo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}
	ceo.showsComposer(t, "claude")

	result, err := (&CEOConnection{State: stateDir}).Send(context.Background(), registrationIdentity(t, stateDir), "hello")

	if err != nil || result.Awaiting == nil || result.Awaiting.Host != "ceo" || result.Reason != sentToCEO {
		t.Errorf("Send = %+v, %v; want it sent and awaiting the CEO's hook, not an error", result, err)
	}
	if typed := ceo.exit(t); len(typed) != 2 || typed[1] != "Overlord: hello" {
		t.Errorf("the terminal received %q, want the message typed once", typed)
	}
}

// A delivery to a native CEO whose host does not answer is refused with
// nothing sent, so the board may offer it again.
func TestADeliveryToANativeCEOWhoseHostDoesNotAnswerIsRefused(t *testing.T) {
	stateDir := t.TempDir()
	nativePrimary(t, stateDir)
	recordHost(t, stateDir, os.Getpid())
	identity := registrationIdentity(t, stateDir)

	_, err := (&CEOConnection{State: stateDir}).Send(context.Background(), identity, "hello")

	if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), "nothing was sent") {
		t.Errorf("Send error = %v, want a refusal with nothing sent", err)
	}
}

// registrationIdentity is the fingerprint deliveries bind to.
func registrationIdentity(t *testing.T, stateDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(stateDir, "primary.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, identity, err := decodePrimary(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

// The board's Herdr view of a CEO registered in a native terminal says it
// cannot show it, rather than opening a pane the registration does not name.
func TestTheHerdrViewOfANativeCEOSaysItCannotShowIt(t *testing.T) {
	store, _ := testStore(t)
	nativePrimary(t, store.Home.State)
	s := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: store.Home.State, Terminals: terminal.HerdrSessions(&herdr.Client{})}}}

	_, err := s.resolveTerminal(context.Background(), terminalSelection{}, false)

	var unavailable unavailableTerminal
	if !errors.As(err, &unavailable) || !strings.Contains(err.Error(), "native terminal") {
		t.Errorf("resolveTerminal error = %v, want the view refused as unavailable for a native CEO", err)
	}
}

// The launcher reads a live native CEO as native, never as a Herdr CEO it
// could bring to the front.
func TestALiveNativeCEOIsNotAHerdrCEO(t *testing.T) {
	stateDir := t.TempDir()
	nativePrimary(t, stateDir)

	endpoint, herdrLive := LiveCEO(stateDir)
	id, nativeLive := NativeCEO(stateDir)

	if herdrLive {
		t.Errorf("LiveCEO = %+v, true; want a native CEO left out of Herdr", endpoint)
	}
	if !nativeLive || id != "ceo" {
		t.Errorf("NativeCEO = %q, %v; want ceo, true", id, nativeLive)
	}
}

// ceoQuery opens the registered CEO's native terminal, ceo, with the board's
// token.
const ceoQuery = "ceo=ceo&token=instance"

// The snapshot names the native terminal the registered CEO runs in, so the
// board opens that terminal for the CEO instead of a Herdr view.
func TestTheSnapshotNamesTheNativeTerminalTheCEORunsIn(t *testing.T) {
	h, _ := nativeBoard(t, "direct")
	before, err := h.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	nativePrimary(t, h.Service.Store.Home.State)

	after, err := h.Service.Snapshot()

	if err != nil {
		t.Fatal(err)
	}
	if before.CEOTerminal != "" || after.CEOTerminal != "ceo" {
		t.Errorf("CEOTerminal = %q before the CEO registered and %q after, want empty and ceo", before.CEOTerminal, after.CEOTerminal)
	}
}

// The snapshot names the harness the registered CEO runs, so the board can
// show its mark beside the CEO.
func TestTheSnapshotNamesTheHarnessTheCEORuns(t *testing.T) {
	// Arrange
	h, _ := nativeBoard(t, "direct")
	before, err := h.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	nativePrimary(t, h.Service.Store.Home.State)

	// Act
	after, err := h.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if before.CEOHarness != "" || after.CEOHarness != "claude" {
		t.Errorf("CEOHarness = %q before the CEO registered and %q after, want empty and claude", before.CEOHarness, after.CEOHarness)
	}
}

// The snapshot names the registered CEO's session, the one every surface
// reads the CEO's status from, and no session while no CEO is registered: an
// older CEO's session, which may have ended, never stands in for it. On
// 2026-10-17 the CEO panel read Session ended from a Pi CEO that had ended,
// while a Claude Code CEO worked.
func TestTheSnapshotNamesTheRegisteredCEOsSession(t *testing.T) {
	// Arrange
	h, _ := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	before, err := h.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	process, err := lock.Acquire(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(stateDir) })
	process.Session = "live-ceo-session"
	data, err := json.Marshal(primaryRegistration{Host: "ceo", Agent: "claude", Process: *process})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "primary.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	after, err := h.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if before.CEOSession != "" || after.CEOSession != "claude/live-ceo-session" {
		t.Errorf("CEOSession = %q before the CEO registered and %q after, want empty and claude/live-ceo-session", before.CEOSession, after.CEOSession)
	}
}

// The board shows the registered native CEO's terminal as it shows a
// task's: its output reaches the view and typing reaches the CEO.
func TestTheBoardShowsANativeCEOsTerminalAndTypesIntoIt(t *testing.T) {
	h, server := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	nativePrimary(t, stateDir)
	terminal := hostTerminal(t, stateDir, "ceo")

	v := openNativeView(t, server, ceoQuery)
	v.waitFor(t, "program ready")
	v.send(t, websocket.MessageBinary, "for the ceo\r")

	if lines := terminal.waitForLines(t, 1); len(lines) != 1 || lines[0] != "for the ceo" {
		t.Errorf("the CEO's terminal got %q, want the line typed in the board", lines)
	}
}

// A view of the CEO's terminal closes as ended while the CEO runs in Herdr or
// not at all, or in another terminal than the one the view names, rather than
// showing some other terminal: no Reconnect brings the CEO back to it, and the
// board opens the CEO's terminal again by itself once it runs in one.
func TestACEOViewEndsUnlessTheCEORunsInTheTerminalItNames(t *testing.T) {
	h, server := nativeBoard(t, "direct")

	closed := openNativeView(t, server, ceoQuery).waitForClose(t)

	if closed.Code != websocket.StatusNormalClosure || !strings.Contains(closed.Reason, "native terminal") {
		t.Errorf("with no native CEO the view closed with %d %q, want it ended because the CEO has no native terminal", closed.Code, closed.Reason)
	}
	nativePrimary(t, h.Service.Store.Home.State)
	hostTerminal(t, h.Service.Store.Home.State, "ceo")

	closed = openNativeView(t, server, "ceo=elsewhere&token=instance").waitForClose(t)

	if closed.Code != websocket.StatusNormalClosure || !strings.Contains(closed.Reason, "another terminal") {
		t.Errorf("naming another terminal the view closed with %d %q, want it ended because the CEO runs in another terminal", closed.Code, closed.Reason)
	}
}

// With no CEO registered, the board shows native terminal ceo while its host
// answers, as goblins does: a CEO the first-run page started registers only
// once it runs, and may first need an answer typed into it there.
func TestTheBoardShowsTheCEOsTerminalBeforeTheCEORegisters(t *testing.T) {
	// Arrange
	h, server := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	terminal := hostTerminal(t, stateDir, NativeCEOTerminal)

	// Act
	snapshot, err := h.Service.Snapshot()
	v := openNativeView(t, server, ceoQuery)
	v.waitFor(t, "program ready")
	v.send(t, websocket.MessageBinary, "yes, trust this folder\r")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CEOTerminal != NativeCEOTerminal || !snapshot.CEORuns {
		t.Errorf("CEOTerminal = %q, CEORuns = %v with terminal ceo up and no CEO registered, want goblins and true", snapshot.CEOTerminal, snapshot.CEORuns)
	}
	if lines := terminal.waitForLines(t, 1); len(lines) != 1 || lines[0] != "yes, trust this folder" {
		t.Errorf("the CEO's terminal got %q, want the line typed in the board", lines)
	}
}

// A CEO started in native terminal ceo has not registered yet: Claude Code
// registers through its SessionStart hook after its onboarding and sign-in, a
// Codex or pi CEO when its first prompt runs goblins register. Until then the
// snapshot says it is starting and carries no registration problem: the board
// opens its terminal for whatever it asks there instead of telling the
// Overlord to run goblins register. With no terminal ceo up, the problem stands.
func TestAStartingCEOIsNotReportedAsUnregistered(t *testing.T) {
	// Arrange
	h, _ := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	h.Service.Options.CEO = &CEOConnection{State: stateDir}
	h.Service.checkRegistration()
	absent, err := h.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	hostTerminal(t, stateDir, NativeCEOTerminal)

	// Act
	starting, err := h.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if absent.CEOStarting || !strings.Contains(absent.Registration, "not registered") {
		t.Errorf("with no CEO and no terminal ceo: starting %v, registration %q; want not starting and the registration problem", absent.CEOStarting, absent.Registration)
	}
	if !starting.CEOStarting || starting.Registration != "" {
		t.Errorf("with terminal ceo up and no CEO registered: starting %v, registration %q; want starting and no registration problem", starting.CEOStarting, starting.Registration)
	}
}

// A CEO that registers after starting is not reported as unregistered until
// the next registration check: the check that found none no longer stands.
func TestACEOThatRegisteredIsNotReportedAsUnregisteredBeforeTheNextCheck(t *testing.T) {
	// Arrange
	h, _ := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	h.Service.Options.CEO = &CEOConnection{State: stateDir}
	h.Service.checkRegistration()
	nativePrimary(t, stateDir)
	recordHost(t, stateDir, os.Getpid())

	// Act
	snapshot, err := h.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.CEORuns || snapshot.Registration != "" {
		t.Errorf("with a CEO registered since the last check: runs %v, registration %q; want it running and no registration problem", snapshot.CEORuns, snapshot.Registration)
	}
}

// The CEO was closed and opened again in its terminal. While it is closed the
// board says so and names no pid: closing the CEO is no problem to report.
// The recovery cycle that ran meanwhile found its process gone, and that
// finding is about the registration it read: once the new CEO registers, the
// board says it runs and shows no problem, never the closed one's pid until
// the next cycle.
func TestAReopenedCEOIsNotReportedWithTheProblemOfTheOneItReplaced(t *testing.T) {
	// Arrange
	h, _ := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	h.Service.Options.CEO = &CEOConnection{State: stateDir}
	t.Setenv("HERDR_PANE_ID", "")
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	exited, err := json.Marshal(primaryRegistration{Host: NativeCEOTerminal, Agent: "claude", Process: lock.Info{PID: 37680, OwnerPID: 37680, Start: time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC), Hostname: hostname}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "primary.json"), exited, 0o600); err != nil {
		t.Fatal(err)
	}
	h.Service.checkRegistration()
	closed, err := h.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	terminal := hostTerminal(t, stateDir, NativeCEOTerminal)
	starting, err := h.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	terminal.typeLine(t, "register")
	if lines := terminal.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}
	reopened, err := h.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if closed.CEORuns || !closed.CEOClosed || closed.Registration != "" {
		t.Errorf("with the CEO closed: runs %v, closed %v, registration %q; want it closed, not running, and no problem naming its process", closed.CEORuns, closed.CEOClosed, closed.Registration)
	}
	if !starting.CEOStarting || starting.CEOClosed || starting.Registration != "" {
		t.Errorf("with terminal ceo up again and no CEO registered: starting %v, closed %v, registration %q; want starting, not closed, and no registration problem", starting.CEOStarting, starting.CEOClosed, starting.Registration)
	}
	if !reopened.CEORuns || reopened.CEOStarting || reopened.CEOClosed || reopened.Registration != "" {
		t.Errorf("with the reopened CEO registered: runs %v, starting %v, closed %v, registration %q; want it running and no registration problem", reopened.CEORuns, reopened.CEOStarting, reopened.CEOClosed, reopened.Registration)
	}
}

// A CEO whose process is gone is said to be closed by the read that finds it
// gone, whatever the last recovery cycle found for the one before it: the
// board never waits for the next cycle to say so.
func TestAnExitedCEOIsSaidClosedByTheReadThatFindsItGone(t *testing.T) {
	// Arrange
	h, _ := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	h.Service.Options.CEO = &CEOConnection{State: stateDir}
	h.Service.Options.FirstRun = &FirstRun{SavedAgent: func() string { return "codex" }}
	nativePrimary(t, stateDir)
	recordHost(t, stateDir, os.Getpid())
	h.Service.checkRegistration()
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	exited, err := json.Marshal(primaryRegistration{Host: NativeCEOTerminal, Agent: "claude", Process: lock.Info{PID: 37680, OwnerPID: 37680, Start: time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC), Hostname: hostname}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "primary.json"), exited, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	snapshot, err := h.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CEORuns || !snapshot.CEOClosed || !snapshot.CEOConfigured || snapshot.Registration != "" {
		t.Errorf("with the registered process gone: runs %v, closed %v, configured %v, registration %q, want it configured and closed with no problem", snapshot.CEORuns, snapshot.CEOClosed, snapshot.CEOConfigured, snapshot.Registration)
	}
}

// A CEO restarted in its terminal runs under a new host there. The snapshot
// says when the host of the CEO's terminal started, so the board opens its
// view of the CEO again when that changes, as it does for a goblin's new
// generation, rather than leave it on the old host's ended screen.
func TestTheSnapshotSaysWhenTheCEOsTerminalHostStarted(t *testing.T) {
	// Arrange
	h, _ := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	nativePrimary(t, stateDir)
	before, after := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC), time.Date(2026, 10, 5, 9, 5, 0, 0, time.UTC)
	recordHostStarted(t, stateDir, os.Getpid(), before)
	first, err := h.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	recordHostStarted(t, stateDir, os.Getpid(), after)

	// Act
	second, err := h.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !first.CEOTerminalSince.Equal(before) || !second.CEOTerminalSince.Equal(after) {
		t.Errorf("the CEO's terminal host started %v, then %v; want %v, then %v", first.CEOTerminalSince, second.CEOTerminalSince, before, after)
	}
}

// Only a registration that is sound and whose process ended is a closed CEO.
// A home no CEO ever registered in is not one, even with a saved harness.
// A registration nothing can read is wrong and
// stays wrong, so it keeps its problem.
func TestOnlyASoundRegistrationWhoseProcessEndedIsAClosedCEO(t *testing.T) {
	for _, tc := range []struct {
		name, primary, problem string
	}{
		{"no CEO ever registered", "", "The CEO is not registered"},
		{"a registration nothing can read", "{not json", "The CEO registration is invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			h, _ := nativeBoard(t, "direct")
			stateDir := h.Service.Store.Home.State
			h.Service.Options.CEO = &CEOConnection{State: stateDir}
			h.Service.Options.FirstRun = &FirstRun{SavedAgent: func() string { return "codex" }}
			if tc.primary != "" {
				if err := os.WriteFile(filepath.Join(stateDir, "primary.json"), []byte(tc.primary), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			snapshot, err := h.Service.Snapshot()

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.CEOClosed || snapshot.CEORuns || !snapshot.CEOConfigured || !strings.Contains(snapshot.Registration, tc.problem) {
				t.Errorf("closed %v, runs %v, configured %v, registration %q, want only configured and a problem naming %q", snapshot.CEOClosed, snapshot.CEORuns, snapshot.CEOConfigured, snapshot.Registration, tc.problem)
			}
		})
	}
}

// A view of the CEO's terminal closes as ended once the CEO registers in
// another terminal, so typing never reaches a terminal the CEO has left.
func TestACEOViewClosesWhenTheCEOLeavesItsTerminal(t *testing.T) {
	h, server := nativeBoard(t, "direct")
	h.terminalTick = 10 * time.Millisecond
	stateDir := h.Service.Store.Home.State
	nativePrimary(t, stateDir)
	hostTerminal(t, stateDir, "ceo")
	v := openNativeView(t, server, ceoQuery)
	v.waitFor(t, "program ready")

	data, err := os.ReadFile(filepath.Join(stateDir, "primary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var moved primaryRegistration
	if err := json.Unmarshal(data, &moved); err != nil {
		t.Fatal(err)
	}
	moved.Host = "elsewhere"
	if data, err = json.Marshal(moved); err != nil {
		t.Fatal(err)
	}

	// The relay reads the registration every tick through a handle that
	// refuses a write while it is open, so the write retries the few
	// microseconds a tick holds it, the way state.RemoveTaskMeta does.
	deadline := time.Now().Add(time.Minute)
	for err := os.WriteFile(filepath.Join(stateDir, "primary.json"), data, 0o600); err != nil; err = os.WriteFile(filepath.Join(stateDir, "primary.json"), data, 0o600) {
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}

	if closed := v.waitForClose(t); closed.Code != websocket.StatusNormalClosure || !strings.Contains(closed.Reason, "CEO") {
		t.Errorf("the view closed with %d %q, want it closed because the CEO left the terminal", closed.Code, closed.Reason)
	}
}
