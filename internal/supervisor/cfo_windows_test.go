package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/herdr/herdrtest"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/terminal"
)

func TestObsoleteCFOMessageRejectionPreservesFullActionHistory(t *testing.T) {
	store, _ := testStore(t)
	for i := 0; i < maxActions; i++ {
		store.db.Actions = append(store.db.Actions, Action{ID: fmt.Sprintf("completed-%d", i), Kind: "cfo_message", Text: "retained", Status: "succeeded"})
	}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()
	disk, err := os.ReadFile(store.path())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(Action{ID: "new-message", Kind: "cfo_message", Text: "hello"}); err == nil {
		t.Fatal("obsolete CFO message accepted")
	}
	if !reflect.DeepEqual(before, store.Snapshot()) {
		t.Fatal("rejected queue changed in-memory history")
	}
	after, err := os.ReadFile(store.path())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(disk, after) {
		t.Fatal("rejected queue changed durable history")
	}
}

// cfoRunner is a fake Herdr for the board's views of a terminal an older
// build started there.
type cfoRunner struct {
	t          *testing.T
	pid        int
	terminal   string
	workerTree string
	calls      int
	// socket is the Herdr socket a live terminal view types into, which
	// records what it types; sizeless leaves the pane's size out of the
	// snapshot and resized grows it.
	socket   *herdrtest.Socket
	sizeless bool
	// focused records each workspace and tab brought to the front.
	focused []string
	resized atomic.Bool
	// holding makes each Herdr command wait for release, as a command slowed
	// by a loaded machine, and says so on held.
	holding atomic.Bool
	held    chan struct{}
	release chan struct{}
}

func (r *cfoRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	if r.holding.Load() {
		select {
		case r.held <- struct{}{}:
		default:
		}
		<-r.release
	}
	r.calls++
	a := req.Args
	var body string
	switch {
	case len(a) >= 2 && a[0] == "api" && a[1] == "snapshot":
		body = `{"result":{"type":"session_snapshot","snapshot":{"protocol":1,"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","terminal_id":"test-terminal"}],"agents":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","agent":"codex","agent_status":"idle"}],"layouts":[{"tab_id":"w1:t1","panes":[{"pane_id":"w1:p1","rect":{"x":0,"y":0,"width":132,"height":43}}]}]}}}`
		if r.sizeless {
			body = strings.Replace(body, `"layouts"`, `"unsized"`, 1)
		}
		if r.resized.Load() {
			body = strings.Replace(body, `"width":132,"height":43`, `"width":180,"height":50`, 1)
		}
		if r.terminal != "" {
			body = strings.ReplaceAll(body, "test-terminal", r.terminal)
		}
		if r.workerTree != "" {
			body = strings.ReplaceAll(strings.ReplaceAll(body, "w1:p1", "p1"), "w1:t1", "tab1")
			cwd, _ := json.Marshal(r.workerTree)
			body = strings.ReplaceAll(body, `"agent_status":"idle"`, `"agent_status":"idle","cwd":`+string(cwd))
		}
	case len(a) >= 2 && a[0] == "pane" && a[1] == "process-info":
		body = fmt.Sprintf(`{"result":{"process_info":{"shell_pid":1,"foreground_process_group_id":%d}}}`, r.pid)
	case len(a) >= 2 && a[0] == "pane" && a[1] == "get":
		body = `{"result":{"pane":{"pane_id":"w1:p1"}}}`
	case len(a) >= 3 && (a[0] == "workspace" || a[0] == "tab") && a[1] == "focus":
		r.focused = append(r.focused, strings.Join(a[:3], " "))
		body = `{"result":{}}`
	case len(a) >= 2 && a[0] == "status" && a[1] == "--json" && r.socket != nil:
		body = r.socket.Status()
	case len(a) >= 2 && a[0] == "pane" && (a[1] == "send-text" || a[1] == "send-keys"):
		r.t.Fatal("pane typing ran as a Herdr command")
	default:
		return execx.Result{}, fmt.Errorf("unexpected Herdr operation: %v", a)
	}
	return execx.Result{Stdout: []byte(body)}, nil
}

// herdrPrimaryFixture registers this test process as a CFO in Herdr pane
// w1:p1 of a fake Herdr, as an older build left one, for the board's view of
// that pane.
func herdrPrimaryFixture(t *testing.T, store *Store) (primaryRegistration, string, *cfoRunner, *CFOConnection) {
	t.Helper()
	process, err := lock.Acquire(store.Home.State)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(store.Home.State) })
	primary := primaryRegistration{Target: herdr.Target{Session: "isolated", Pane: "w1:p1"}, Workspace: "w1", Tab: "w1:t1", Agent: "codex", Terminal: "test-terminal", Process: *process}
	data, _ := json.Marshal(primary)
	if err := os.WriteFile(filepath.Join(store.Home.State, "primary.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	_, identity, err := decodePrimary(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	runner := &cfoRunner{t: t, pid: os.Getpid()}
	return primary, identity, runner, &CFOConnection{State: store.Home.State, Terminals: terminal.HerdrSessions(&herdr.Client{Commands: runner})}
}

func registrationExists(t *testing.T, store *Store) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(store.Home.State, "primary.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

// The live fleet's primary.json named a CFO process from six days earlier,
// and nothing could replace it: every delivery path failed with a generic
// error. Register replaces it with the live harness, and the board's own
// verification accepts the result.
func TestRegisterReplacesAStaleRegistrationWithOneTheBoardVerifies(t *testing.T) {
	store, _, cfo := registerFixture(t)
	hostname, _ := os.Hostname()
	stale := primaryRegistration{Host: "cfo", Agent: "claude", Process: lock.Info{PID: 37680, OwnerPID: 37680, Start: time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC), Hostname: hostname}}
	data, _ := json.Marshal(stale)
	if err := os.WriteFile(filepath.Join(store.Home.State, "primary.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var problem registrationProblem
	if err := cfo.check(); !errors.As(err, &problem) || !strings.Contains(err.Error(), "pid 37680, started 2026-09-15 09:00 UTC, is no longer running; run cfo register in the CFO session") {
		t.Fatalf("stale registration reads as %v, want one registration problem naming the fix", err)
	}

	described, err := Register(store.Home.State, "claude", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := "claude pid " + strconv.Itoa(os.Getpid()) + " in native terminal cfo"; described != want {
		t.Errorf("described %q, want %q", described, want)
	}
	if err := cfo.check(); err != nil {
		t.Fatalf("the board cannot verify the new registration: %v", err)
	}
	if !lock.HeldBy(store.Home.State, os.Getpid()) {
		t.Error("registering a free home did not take its session lock")
	}
}

// A compact, clear or resume of the same CFO registers again. primary.json's
// hash is the identity a pending question is bound to, so rewriting it would
// supersede the question the user has not answered yet.
func TestRegisterKeepsTheIdentityOfTheSameProcess(t *testing.T) {
	store, _, cfo := registerFixture(t)
	if _, err := Register(store.Home.State, "claude", "session-1"); err != nil {
		t.Fatal(err)
	}
	servePipe(t, store, cfo)
	path := filepath.Join(store.Home.State, "primary.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfo.PublishQuestion("question-1", "Pick a layout", []string{"Board", "Tree"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(store.Home.State, "claude", "session-2"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("registering the same process again rewrote primary.json")
	}
	if err := store.supersedeQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	if questions := store.Snapshot().Questions; len(questions) != 1 || questions[0].Status != "pending" {
		t.Fatalf("the question after registering again: %+v", questions)
	}
}

// A recycled pid is a different process, so its registration is replaced.
func TestRegisterReplacesARegistrationOfAnotherProcessWithTheSamePID(t *testing.T) {
	store, _, cfo := registerFixture(t)
	if _, err := Register(store.Home.State, "claude", ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Home.State, "primary.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var recycled primaryRegistration
	if err := json.Unmarshal(data, &recycled); err != nil {
		t.Fatal(err)
	}
	recycled.Process.Start = recycled.Process.Start.Add(-time.Hour)
	data, _ = json.Marshal(recycled)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(store.Home.State, "claude", ""); err != nil {
		t.Fatal(err)
	}
	if err := cfo.check(); err != nil {
		t.Fatalf("the registration of a recycled pid was kept: %v", err)
	}
}

func TestRegisterRefusesWhatItCannotProve(t *testing.T) {
	for _, c := range []struct {
		name, harness, want string
		arrange             func(*testing.T, *Store)
	}{
		{"outside a native terminal", "claude", "runs in no native terminal", func(t *testing.T, _ *Store) {
			t.Setenv(host.IDVariable, "")
		}},
		{"another live session holding the home", "claude", "another live session holds this home", func(t *testing.T, store *Store) {
			other := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL")
			if err := other.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = other.Process.Kill(); _, _ = other.Process.Wait() })
			if _, err := lock.AcquireOwner(store.Home.State, other.Process.Pid, "other"); err != nil {
				t.Fatal(err)
			}
		}},
		// With no harness named, the terminal's program has to be one.
		{"a program that is no harness", "", "is not a harness the board delivers to", func(*testing.T, *Store) {}},
	} {
		t.Run(c.name, func(t *testing.T) {
			store, _, _ := registerFixture(t)
			c.arrange(t, store)
			_, err := Register(store.Home.State, c.harness, "")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Register: %v, want a refusal containing %q", err, c.want)
			}
			if registrationExists(t, store) {
				t.Fatal("a refused registration wrote primary.json")
			}
		})
	}
}

// A CFO an older build registered in a Herdr pane is no longer reachable:
// the board says so with its own fix, nothing is delivered to it, and it
// proves nobody's identity.
func TestARegistrationInAHerdrPaneIsNotReachable(t *testing.T) {
	store, _ := testStore(t)
	_, identity, runner, cfo := herdrPrimaryFixture(t, store)

	checked := cfo.check()
	_, sent := cfo.Send(context.Background(), identity, "hello")
	_, _, proven := cfo.CallerIdentity()

	for name, err := range map[string]error{"check": checked, "Send": sent, "CallerIdentity": proven} {
		if err == nil || !strings.Contains(err.Error(), "registered in a Herdr pane, which this build cannot reach; start the CFO again in a native terminal") {
			t.Errorf("%s = %v, want the Herdr registration refused with its fix", name, err)
		}
	}
	if !errors.Is(sent, ErrRejected) {
		t.Errorf("Send = %v, want a refusal with nothing sent", sent)
	}
	if runner.calls != 0 {
		t.Errorf("Herdr was asked %d times, want none", runner.calls)
	}
}

func TestBoardShowsTheRegistrationAsOneStateWithItsFix(t *testing.T) {
	store, _ := testStore(t)
	// The terminal is not named cfo: one of that name that is up reads as a
	// CFO still starting, never as an unregistered one.
	hostTerminal(t, store.Home.State, "desk").standIn(t)
	t.Setenv(host.IDVariable, "desk")
	cfo := &CFOConnection{State: store.Home.State}
	service := &Service{Store: store, Options: Options{CFO: cfo}}
	service.checkRegistration()
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Registration != "The CFO is not registered; run cfo register in the CFO session" {
		t.Fatalf("unregistered board shows %q", snapshot.Registration)
	}
	if _, err := Register(store.Home.State, "claude", ""); err != nil {
		t.Fatal(err)
	}
	service.checkRegistration()
	if snapshot, _ = service.Snapshot(); snapshot.Registration != "" {
		t.Fatalf("registered board still shows %q", snapshot.Registration)
	}
}

func TestCFOTerminalReportsAStaleRegistrationAsItsOwnState(t *testing.T) {
	_, server, _, runner := terminalHTTPFixture(t, newTestTerminal())
	runner.terminal = "replacement-terminal"
	response := terminalPost(t, server, "/api/terminal/stream", `{"cols":80,"rows":24}`)
	defer response.Body.Close()
	var failure struct{ Error, Code string }
	if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 409 || failure.Code != "registration_stale" || !strings.HasSuffix(failure.Error, "run cfo register in the CFO session") {
		t.Fatalf("stale CFO terminal: %d %+v", response.StatusCode, failure)
	}
}

// The CFO's conversation is recorded each time it registers, its latest one
// included when the same process registers again after a compact, clear or
// resume, without rewriting the registration questions are bound to.
func TestRegisterRecordsTheCFOsLatestConversation(t *testing.T) {
	// Arrange
	store, _, _ := registerFixture(t)
	if _, err := Register(store.Home.State, "claude", "session-1"); err != nil {
		t.Fatal(err)
	}
	first, err := ReadCFOConversation(store.Home.State)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(store.Home.State, "primary.json"))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	if _, err := Register(store.Home.State, "claude", "session-2"); err != nil {
		t.Fatal(err)
	}
	latest, err := ReadCFOConversation(store.Home.State)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if first.Session != "session-1" || latest.Session != "session-2" || latest.Harness != "claude" || latest.PID != os.Getpid() {
		t.Errorf("conversations recorded %+v then %+v, want session-1 then session-2 of this claude process", first, latest)
	}
	if after, err := os.ReadFile(filepath.Join(store.Home.State, "primary.json")); err != nil || !bytes.Equal(before, after) {
		t.Errorf("registering the same process again rewrote primary.json: %v", err)
	}
}

// A registration that names no session leaves the last conversation as it was.
func TestRegisterWithoutASessionKeepsTheLastConversation(t *testing.T) {
	// Arrange
	store, _, _ := registerFixture(t)
	if _, err := Register(store.Home.State, "claude", "session-1"); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err := Register(store.Home.State, "claude", "")
	conversation, readErr := ReadCFOConversation(store.Home.State)

	// Assert
	if err != nil || readErr != nil || conversation.Session != "session-1" {
		t.Errorf("after a registration with no session: %+v, %v, %v; want session-1 kept", conversation, err, readErr)
	}
}
