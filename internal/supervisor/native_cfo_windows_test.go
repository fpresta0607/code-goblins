package supervisor

import (
	"bytes"
	"context"
	"crypto/rand"
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
// and reported delivered once its host confirms it wrote both into the
// terminal's input.
func TestADeliveryToANativeCFOIsTypedIntoItsTerminalOnce(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("HERDR_PANE_ID", "")
	cfo := hostTerminal(t, stateDir, "cfo")
	cfo.typeLine(t, "register")
	if lines := cfo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}

	result, err := (&CFOConnection{State: stateDir}).Send(context.Background(), registrationIdentity(t, stateDir), "hello")

	if err != nil || !strings.Contains(result.Reason, "native terminal took") {
		t.Errorf("Send = %+v, %v; want it delivered once the host wrote it", result, err)
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

	result, err := (&CFOConnection{State: stateDir}).Send(context.Background(), registrationIdentity(t, stateDir), "hello")

	if err != nil || !strings.Contains(result.Reason, "native terminal took") {
		t.Errorf("Send = %+v, %v; want it delivered once the host wrote it", result, err)
	}
	if typed := cfo.exit(t); len(typed) != 3 || typed[2] != "Overlord: hello" {
		t.Errorf("the terminal received %q, want its registration, the spill and then the message once", typed)
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

// A view of the CFO's terminal closes once the registration stops naming
// that terminal, so typing never reaches a terminal the CFO has left.
func TestACFOViewClosesWhenTheCFOLeavesItsTerminal(t *testing.T) {
	h, server := nativeBoard(t, "direct")
	h.terminalTick = 10 * time.Millisecond
	stateDir := h.Service.Store.Home.State
	nativePrimary(t, stateDir)
	hostTerminal(t, stateDir, "cfo")
	v := openNativeView(t, server, cfoQuery)
	v.waitFor(t, "program ready")

	// The relay reads the registration every tick through a handle that
	// refuses a delete while it is open, so the delete retries the few
	// microseconds a tick holds it, the way state.RemoveTaskMeta does.
	deadline := time.Now().Add(2 * time.Second)
	for err := os.Remove(filepath.Join(stateDir, "primary.json")); err != nil; err = os.Remove(filepath.Join(stateDir, "primary.json")) {
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}

	if closed := v.waitForClose(t); closed.Code != websocket.StatusPolicyViolation || !strings.Contains(closed.Reason, "CFO") {
		t.Errorf("the view closed with %d %q, want it closed because the CFO left the terminal", closed.Code, closed.Reason)
	}
}
