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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/terminal"
)

// recordHost writes the record of a host for terminal cfo whose program is
// childPID and whose pipe nothing serves, as a host that was killed leaves
// behind.
func recordHost(t *testing.T, stateDir string, childPID int) {
	t.Helper()
	var name [16]byte
	if _, err := rand.Read(name[:]); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(host.Record{ID: "cfo", Pipe: `\\.\pipe\code-goblins-host-` + hex.EncodeToString(name[:]), Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: childPID, Started: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", "cfo.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// registerNatively is the environment of a program in native terminal cfo,
// outside any Herdr pane.
func registerNatively(t *testing.T) {
	t.Helper()
	t.Setenv(host.IDVariable, "cfo")
	t.Setenv("HERDR_PANE_ID", "")
}

// nativePrimary registers this test process as a CFO in native terminal cfo.
func nativePrimary(t *testing.T, stateDir string) {
	t.Helper()
	process, err := lock.Acquire(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(stateDir) })
	data, err := json.Marshal(primaryRegistration{Host: "cfo", Agent: "claude", Process: *process})
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
		"a native terminal":            {primaryRegistration{Host: "cfo", Agent: "claude", Process: alive}, true},
		"a native terminal and a pane": {primaryRegistration{Host: "cfo", Agent: "claude", Target: herdr.Target{Session: "s", Pane: "w1:p1"}, Workspace: "w1", Tab: "w1:t1", Terminal: "t1", Process: alive}, false},
		"a terminal id no host takes":  {primaryRegistration{Host: "../cfo", Agent: "claude", Process: alive}, false},
		"a native terminal, no agent":  {primaryRegistration{Host: "cfo", Process: alive}, false},
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

	_, err := Register(context.Background(), stateDir, nil, "claude", "session-1")

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

	_, err := Register(context.Background(), stateDir, nil, "claude", "session-1")

	if err == nil || !strings.Contains(err.Error(), "does not run under it") {
		t.Fatalf("Register error = %v, want this process refused for not running under the terminal", err)
	}
}

// A native registration stays verified while its terminal's host record names
// the registered process as the terminal's program, and not after.
func TestANativeRegistrationIsVerifiedOnlyWhileItsTerminalRunsIt(t *testing.T) {
	stateDir := t.TempDir()
	nativePrimary(t, stateDir)
	c := &CFOConnection{State: stateDir}

	recordHost(t, stateDir, os.Getpid())
	if err := c.check(context.Background()); err != nil {
		t.Fatalf("check with the terminal running the CFO = %v, want nil", err)
	}
	recordHost(t, stateDir, 4)
	if err := c.check(context.Background()); err == nil || !strings.Contains(err.Error(), "ended or runs another program") {
		t.Errorf("check with the terminal running another program = %v, want the registration refused", err)
	}
	if err := os.Remove(filepath.Join(stateDir, "hosts", "cfo.json")); err != nil {
		t.Fatal(err)
	}
	if err := c.check(context.Background()); err == nil || !strings.Contains(err.Error(), "ended or runs another program") {
		t.Errorf("check with the terminal ended = %v, want the registration refused", err)
	}
}

// The program in a live native terminal registers as the CFO: its host told
// it which terminal it runs in, the terminal's program is its own process,
// and the host answers.
func TestAProgramInALiveNativeTerminalRegistersAsTheCFO(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("HERDR_PANE_ID", "")
	cfo := hostTerminal(t, stateDir, "cfo")
	record, err := host.ReadRecord(stateDir, "cfo")
	if err != nil {
		t.Fatal(err)
	}

	cfo.typeLine(t, "register")

	want := fmt.Sprintf("registered claude pid %d in native terminal cfo", record.ChildPID)
	if lines := cfo.waitForLines(t, 1); len(lines) != 1 || lines[0] != want {
		t.Fatalf("the program recorded %q, want %q", lines, want)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "primary.json"))
	if err != nil {
		t.Fatal(err)
	}
	primary, _, err := decodePrimary(bytes.NewReader(data))
	if err != nil || primary.Host != "cfo" || primary.Agent != "claude" || primary.Process.PID != record.ChildPID {
		t.Errorf("primary.json = %s, %v; want the terminal's program registered in native terminal cfo", data, err)
	}
}

// A delivery to a native CFO is typed into its terminal and submitted once,
// and reported delivered once the CFO's own prompt hook, naming the terminal
// it runs in, reports taking it.
func TestADeliveryToANativeCFOIsTypedIntoItsTerminalOnce(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("HERDR_PANE_ID", "")
	cfo := hostTerminal(t, stateDir, "cfo")
	cfo.typeLine(t, "register")
	if lines := cfo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}
	cfo.typeLine(t, "hooked")

	result, err := (&CFOConnection{State: stateDir}).Send(context.Background(), registrationIdentity(t, stateDir), "hello")

	if err != nil || !strings.Contains(result.Reason, "its hook reported") {
		t.Errorf("Send = %+v, %v; want it delivered once the CFO's hook reported taking it", result, err)
	}
	if typed := cfo.exit(t); len(typed) != 2 || typed[1] != "Overlord: hello" {
		t.Errorf("the terminal received %q, want its registration and then the message once", typed)
	}
}

// A delivery to a native CFO whose terminal has shown more than the host's
// pipe holds is still typed into it and submitted once.
func TestADeliveryToANativeCFOWithALongHistoryIsTypedIntoItsTerminalOnce(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("HERDR_PANE_ID", "")
	cfo := hostTerminal(t, stateDir, "cfo")
	cfo.typeLine(t, "register")
	cfo.typeLine(t, "spill")
	if lines := cfo.waitForLines(t, 2); len(lines) != 2 || !strings.HasPrefix(lines[0], "registered ") || lines[1] != "spilled" {
		t.Fatalf("the program recorded %q, want its registration and then the spill", lines)
	}
	cfo.typeLine(t, "hooked")

	result, err := (&CFOConnection{State: stateDir}).Send(context.Background(), registrationIdentity(t, stateDir), "hello")

	if err != nil || !strings.Contains(result.Reason, "its hook reported") {
		t.Errorf("Send = %+v, %v; want it delivered once the CFO's hook reported taking it", result, err)
	}
	if typed := cfo.exit(t); len(typed) != 3 || typed[2] != "Overlord: hello" {
		t.Errorf("the terminal received %q, want its registration, the spill and then the message once", typed)
	}
}

// A native CFO presents from its own terminal without naming a task, proven as
// the registered primary CFO the way its questions and answers are.
func TestANativeCFOPresentsWithoutATask(t *testing.T) {
	store, h := testStore(t)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PANE_ID", "")
	cfo := hostTerminal(t, h.State, "cfo")
	cfo.typeLine(t, "register")
	cfo.typeLine(t, "present")

	lines := cfo.waitForLines(t, 2)

	if len(lines) != 2 || !strings.HasPrefix(lines[0], "registered ") || lines[1] != "presented" {
		t.Fatalf("the program recorded %q, want its registration and then its presentation reported", lines)
	}
	if err := store.ingestActivity(); err != nil {
		t.Fatal(err)
	}
	if activity := store.Snapshot().Activity; len(activity) != 1 || activity[0].ID != "cfo-walkthrough" || activity[0].TaskID != "" || activity[0].Target != "primary-cfo" {
		t.Fatalf("activity = %+v, want the CFO's own walkthrough, borrowing no task", activity)
	}
}

// A native CFO's command whose parent has exited, as Git Bash leaves one run
// under timeout, is still proven the registered CFO's by the proof value its
// terminal carries.
func TestANativeCFOPresentsFromAProcessWhoseParentHasExited(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PANE_ID", "")
	cfo := hostTerminal(t, h.State, "cfo")
	cfo.typeLine(t, "register")
	if lines := cfo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}

	// Act
	cfo.typeLine(t, "orphaned present")

	// Assert
	if lines := cfo.waitForLines(t, 2); len(lines) != 2 || lines[1] != "presented" {
		t.Fatalf("the program recorded %q, want its registration and then its presentation reported", lines)
	}
}

// The proof value the CFO's terminal carries proves only a process in that
// terminal. A Herdr server started from it hands the value to every pane it
// opens, and a process in such a pane is not the CFO.
func TestTheCFOsTerminalProofProvesNothingInAHerdrPane(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PANE_ID", "")
	cfo := hostTerminal(t, h.State, "cfo")
	cfo.typeLine(t, "register")
	if lines := cfo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}

	// Act
	cfo.typeLine(t, "orphaned-in-herdr present")

	// Assert
	if lines := cfo.waitForLines(t, 2); len(lines) != 2 || !strings.HasPrefix(lines[1], "present error: ") {
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
	env := []string{host.IDVariable + "=cfo", host.ProofVariable + "=terminal-proof"}
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
			record := host.Record{ID: "cfo", ChildPID: os.Getpid(), ChildStart: c.childStart, ProofSum: hex.EncodeToString(sum[:])}

			// Act
			program, err := terminalProgram(record, env)

			// Assert
			if proven := err == nil && program.PID == os.Getpid(); proven != c.wantProven {
				t.Errorf("terminalProgram = %+v, %v; want proven %t", program, err, c.wantProven)
			}
		})
	}
}

// A native CFO's send from a process whose parent has exited still names the
// CFO as the receipt's sender, proven by the proof value its terminal carries.
func TestANativeCFOsSendFromAProcessWhoseParentHasExitedNamesItAsSender(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	if err := store.Accept(event(t, h, "SessionStart", "worker", "", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	id := store.db.TaskSessions["task-1"]
	node := store.db.Sessions[id]
	node.Parent = "claude/cfo-1"
	store.db.Sessions[id] = node
	store.db.Sessions[node.Parent] = Session{ID: node.Parent, NativeID: "cfo-1", Harness: "claude", Role: "cfo", Phase: "active"}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("CFO_SESSION_ID", "cfo-1")
	t.Setenv("CFO_SESSION_HARNESS", "claude")
	cfo := hostTerminal(t, h.State, "cfo")
	cfo.typeLine(t, "register")
	if lines := cfo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}

	// Act
	cfo.typeLine(t, "orphaned send task-1")

	// Assert
	if lines := cfo.waitForLines(t, 2); len(lines) != 2 || lines[1] != "sent" {
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
		t.Errorf("message receipt sources = %q, want the CFO named as its sender", sources)
	}
}

// A delivery to a native CFO whose screen turns to work but whose prompt hook
// has not reported taking it is sent, not delivered, since Enter may have
// chosen a dialog's option instead: it waits for the hook's report.
func TestADeliveryToANativeCFOWhoseScreenTurnsToWorkWithoutItsHookIsSentNotDelivered(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("HERDR_PANE_ID", "")
	cfo := hostTerminal(t, stateDir, "cfo")
	cfo.typeLine(t, "register")
	cfo.typeLine(t, "harness")
	if lines := cfo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}

	result, err := (&CFOConnection{State: stateDir}).Send(context.Background(), registrationIdentity(t, stateDir), "hello")

	if err != nil || result.Awaiting == nil || result.Awaiting.Host != "cfo" || result.Reason != sentToCFO {
		t.Errorf("Send = %+v, %v; want it sent and awaiting the CFO's hook, not an error", result, err)
	}
	if typed := cfo.exit(t); len(typed) != 2 || typed[1] != "Overlord: hello" {
		t.Errorf("the terminal received %q, want its registration and then the message once", typed)
	}
}

// A delivery to a native CFO already in a turn waits behind that turn: it is
// typed and submitted once and reported sent, not delivered, while no hook
// reports it taken.
func TestADeliveryToANativeCFOInATurnWaitsBehindIt(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("HERDR_PANE_ID", "")
	cfo := hostTerminal(t, stateDir, "cfo")
	cfo.typeLine(t, "register")
	cfo.typeLine(t, "harness")
	cfo.typeLine(t, "busy")
	if lines := cfo.waitForLines(t, 2); len(lines) != 2 || lines[1] != "busy" {
		t.Fatalf("the program recorded %q, want its registration and then a turn under way", lines)
	}

	result, err := (&CFOConnection{State: stateDir}).Send(context.Background(), registrationIdentity(t, stateDir), "hello")

	if err != nil || result.Awaiting == nil || result.Awaiting.Host != "cfo" || result.Reason != sentToCFO {
		t.Errorf("Send = %+v, %v; want it sent and awaiting the CFO's hook, not an error", result, err)
	}
	if typed := cfo.exit(t); len(typed) != 3 || typed[2] != "Overlord: hello" {
		t.Errorf("the terminal received %q, want the message typed once", typed)
	}
}

// The Overlord, 2026-10-01: "Delivery unconfirmed ... I get these command
// center blips and errors, fix". A CFO that takes a typed answer later than
// the confirmation window is not an error: the delivery is typed and
// submitted once and reported sent, awaiting the hook, never typed again.
func TestADeliveryTheNativeCFOHasNotTakenYetIsSentNotAnError(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("HERDR_PANE_ID", "")
	cfo := hostTerminal(t, stateDir, "cfo")
	cfo.typeLine(t, "register")
	if lines := cfo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}

	result, err := (&CFOConnection{State: stateDir}).Send(context.Background(), registrationIdentity(t, stateDir), "hello")

	if err != nil || result.Awaiting == nil || result.Awaiting.Host != "cfo" || result.Reason != sentToCFO {
		t.Errorf("Send = %+v, %v; want it sent and awaiting the CFO's hook, not an error", result, err)
	}
	if typed := cfo.exit(t); len(typed) != 2 || typed[1] != "Overlord: hello" {
		t.Errorf("the terminal received %q, want the message typed once", typed)
	}
}

// A delivery to a native CFO whose host does not answer is refused with
// nothing sent, so the board may offer it again.
func TestADeliveryToANativeCFOWhoseHostDoesNotAnswerIsRefused(t *testing.T) {
	stateDir := t.TempDir()
	nativePrimary(t, stateDir)
	recordHost(t, stateDir, os.Getpid())
	identity := registrationIdentity(t, stateDir)

	_, err := (&CFOConnection{State: stateDir}).Send(context.Background(), identity, "hello")

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

// The board's Herdr view of a CFO registered in a native terminal says it
// cannot show it, rather than opening a pane the registration does not name.
func TestTheHerdrViewOfANativeCFOSaysItCannotShowIt(t *testing.T) {
	store, _ := testStore(t)
	nativePrimary(t, store.Home.State)
	s := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: store.Home.State, Terminals: terminal.HerdrSessions(&herdr.Client{})}}}

	_, err := s.resolveTerminal(context.Background(), terminalSelection{}, false)

	var unavailable unavailableTerminal
	if !errors.As(err, &unavailable) || !strings.Contains(err.Error(), "native terminal") {
		t.Errorf("resolveTerminal error = %v, want the view refused as unavailable for a native CFO", err)
	}
}

// The launcher reads a live native CFO as native, never as a Herdr CFO it
// could bring to the front.
func TestALiveNativeCFOIsNotAHerdrCFO(t *testing.T) {
	stateDir := t.TempDir()
	nativePrimary(t, stateDir)

	endpoint, herdrLive := LiveCFO(stateDir)
	id, nativeLive := NativeCFO(stateDir)

	if herdrLive {
		t.Errorf("LiveCFO = %+v, true; want a native CFO left out of Herdr", endpoint)
	}
	if !nativeLive || id != "cfo" {
		t.Errorf("NativeCFO = %q, %v; want cfo, true", id, nativeLive)
	}
}

// cfoQuery opens the registered CFO's native terminal, cfo, with the board's
// token.
const cfoQuery = "cfo=cfo&token=instance"

// The snapshot names the native terminal the registered CFO runs in, so the
// board opens that terminal for the CFO instead of a Herdr view.
func TestTheSnapshotNamesTheNativeTerminalTheCFORunsIn(t *testing.T) {
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
	if before.CFOTerminal != "" || after.CFOTerminal != "cfo" {
		t.Errorf("CFOTerminal = %q before the CFO registered and %q after, want empty and cfo", before.CFOTerminal, after.CFOTerminal)
	}
}

// The snapshot names the harness the registered CFO runs, so the board can
// show its mark beside the CFO.
func TestTheSnapshotNamesTheHarnessTheCFORuns(t *testing.T) {
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
	if before.CFOHarness != "" || after.CFOHarness != "claude" {
		t.Errorf("CFOHarness = %q before the CFO registered and %q after, want empty and claude", before.CFOHarness, after.CFOHarness)
	}
}

// The board shows the registered native CFO's terminal as it shows a
// task's: its output reaches the view and typing reaches the CFO.
func TestTheBoardShowsANativeCFOsTerminalAndTypesIntoIt(t *testing.T) {
	h, server := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	nativePrimary(t, stateDir)
	terminal := hostTerminal(t, stateDir, "cfo")

	v := openNativeView(t, server, cfoQuery)
	v.waitFor(t, "program ready")
	v.send(t, websocket.MessageBinary, "for the cfo\r")

	if lines := terminal.waitForLines(t, 1); len(lines) != 1 || lines[0] != "for the cfo" {
		t.Errorf("the CFO's terminal got %q, want the line typed in the board", lines)
	}
}

// A view of the CFO's terminal is refused while the CFO runs in Herdr or not
// at all, or in another terminal than the one the view names, rather than
// showing some other terminal.
func TestACFOViewIsRefusedUnlessTheCFORunsInTheTerminalItNames(t *testing.T) {
	h, server := nativeBoard(t, "direct")

	closed := openNativeView(t, server, cfoQuery).waitForClose(t)

	if closed.Code != websocket.StatusPolicyViolation || !strings.Contains(closed.Reason, "native terminal") {
		t.Errorf("with no native CFO the view closed with %d %q, want it refused because the CFO has no native terminal", closed.Code, closed.Reason)
	}
	nativePrimary(t, h.Service.Store.Home.State)
	hostTerminal(t, h.Service.Store.Home.State, "cfo")

	closed = openNativeView(t, server, "cfo=elsewhere&token=instance").waitForClose(t)

	if closed.Code != websocket.StatusPolicyViolation || !strings.Contains(closed.Reason, "another terminal") {
		t.Errorf("naming another terminal the view closed with %d %q, want it refused because the CFO runs in another terminal", closed.Code, closed.Reason)
	}
}

// With no CFO registered, the board shows native terminal cfo while its host
// answers, as goblins does: a CFO the first-run page started registers only
// once it runs, and may first need an answer typed into it there.
func TestTheBoardShowsTheCFOsTerminalBeforeTheCFORegisters(t *testing.T) {
	// Arrange
	h, server := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	terminal := hostTerminal(t, stateDir, NativeCFOTerminal)

	// Act
	snapshot, err := h.Service.Snapshot()
	v := openNativeView(t, server, cfoQuery)
	v.waitFor(t, "program ready")
	v.send(t, websocket.MessageBinary, "yes, trust this folder\r")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CFOTerminal != NativeCFOTerminal || !snapshot.CFORuns {
		t.Errorf("CFOTerminal = %q, CFORuns = %v with terminal cfo up and no CFO registered, want cfo and true", snapshot.CFOTerminal, snapshot.CFORuns)
	}
	if lines := terminal.waitForLines(t, 1); len(lines) != 1 || lines[0] != "yes, trust this folder" {
		t.Errorf("the CFO's terminal got %q, want the line typed in the board", lines)
	}
}

// A CFO started in native terminal cfo registers only after Claude Code's
// own onboarding and sign-in, so until then the snapshot says it is starting
// and carries no registration problem: the board opens its terminal for the
// sign-in instead of telling the Overlord to run cfo register. With no
// terminal cfo up, the problem stands.
func TestAStartingCFOIsNotReportedAsUnregistered(t *testing.T) {
	// Arrange
	h, _ := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	h.Service.Options.CFO = &CFOConnection{State: stateDir}
	h.Service.checkRegistration(context.Background())
	absent, err := h.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	hostTerminal(t, stateDir, NativeCFOTerminal)

	// Act
	starting, err := h.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if absent.CFOStarting || !strings.Contains(absent.Registration, "not registered") {
		t.Errorf("with no CFO and no terminal cfo: starting %v, registration %q; want not starting and the registration problem", absent.CFOStarting, absent.Registration)
	}
	if !starting.CFOStarting || starting.Registration != "" {
		t.Errorf("with terminal cfo up and no CFO registered: starting %v, registration %q; want starting and no registration problem", starting.CFOStarting, starting.Registration)
	}
}

// A CFO that registers after sign-in is not reported as unregistered until
// the next registration check: the check that found none no longer stands.
func TestACFOThatRegisteredIsNotReportedAsUnregisteredBeforeTheNextCheck(t *testing.T) {
	// Arrange
	h, _ := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	h.Service.Options.CFO = &CFOConnection{State: stateDir}
	h.Service.checkRegistration(context.Background())
	nativePrimary(t, stateDir)
	recordHost(t, stateDir, os.Getpid())

	// Act
	snapshot, err := h.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.CFORuns || snapshot.Registration != "" {
		t.Errorf("with a CFO registered since the last check: runs %v, registration %q; want it running and no registration problem", snapshot.CFORuns, snapshot.Registration)
	}
}

// The CFO was closed and opened again in its terminal. The recovery cycle that
// ran while it was closed found its process gone, and that finding is about
// the registration it read: once the new CFO registers, the board says it runs
// and shows no problem, never the closed one's pid until the next cycle.
func TestAReopenedCFOIsNotReportedWithTheProblemOfTheOneItReplaced(t *testing.T) {
	// Arrange
	h, _ := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	h.Service.Options.CFO = &CFOConnection{State: stateDir}
	t.Setenv("HERDR_PANE_ID", "")
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	exited, err := json.Marshal(primaryRegistration{Host: NativeCFOTerminal, Agent: "claude", Process: lock.Info{PID: 37680, OwnerPID: 37680, Start: time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC), Hostname: hostname}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "primary.json"), exited, 0o600); err != nil {
		t.Fatal(err)
	}
	h.Service.checkRegistration(context.Background())
	closed, err := h.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	terminal := hostTerminal(t, stateDir, NativeCFOTerminal)
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
	if closed.CFORuns || !strings.Contains(closed.Registration, "pid 37680") {
		t.Errorf("with the CFO closed: runs %v, registration %q; want it not running and the problem naming its process", closed.CFORuns, closed.Registration)
	}
	if !starting.CFOStarting || starting.Registration != "" {
		t.Errorf("with terminal cfo up again and no CFO registered: starting %v, registration %q; want starting and no registration problem", starting.CFOStarting, starting.Registration)
	}
	if !reopened.CFORuns || reopened.CFOStarting || reopened.Registration != "" {
		t.Errorf("with the reopened CFO registered: runs %v, starting %v, registration %q; want it running and no registration problem", reopened.CFORuns, reopened.CFOStarting, reopened.Registration)
	}
}

// A CFO whose process is gone is reported by the read that finds it gone: the
// board never says no CFO runs while giving no reason until the next cycle.
func TestAnExitedCFOIsReportedByTheReadThatFindsItGone(t *testing.T) {
	// Arrange
	h, _ := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	h.Service.Options.CFO = &CFOConnection{State: stateDir}
	nativePrimary(t, stateDir)
	recordHost(t, stateDir, os.Getpid())
	h.Service.checkRegistration(context.Background())
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	exited, err := json.Marshal(primaryRegistration{Host: NativeCFOTerminal, Agent: "claude", Process: lock.Info{PID: 37680, OwnerPID: 37680, Start: time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC), Hostname: hostname}})
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
	if snapshot.CFORuns || snapshot.Registration == "" {
		t.Errorf("with the registered process gone since the last check: runs %v, registration %q; want it not running and the reason", snapshot.CFORuns, snapshot.Registration)
	}
}

// A view of the CFO's terminal closes once the CFO registers in another
// terminal, so typing never reaches a terminal the CFO has left.
func TestACFOViewClosesWhenTheCFOLeavesItsTerminal(t *testing.T) {
	h, server := nativeBoard(t, "direct")
	h.terminalTick = 10 * time.Millisecond
	stateDir := h.Service.Store.Home.State
	nativePrimary(t, stateDir)
	hostTerminal(t, stateDir, "cfo")
	v := openNativeView(t, server, cfoQuery)
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
	deadline := time.Now().Add(2 * time.Second)
	for err := os.WriteFile(filepath.Join(stateDir, "primary.json"), data, 0o600); err != nil; err = os.WriteFile(filepath.Join(stateDir, "primary.json"), data, 0o600) {
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}

	if closed := v.waitForClose(t); closed.Code != websocket.StatusPolicyViolation || !strings.Contains(closed.Reason, "CFO") {
		t.Errorf("the view closed with %d %q, want it closed because the CFO left the terminal", closed.Code, closed.Reason)
	}
}
