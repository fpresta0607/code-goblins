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

type cfoRunner struct {
	t            *testing.T
	pid          int
	prompts      []string
	beforePrompt func()
	terminal     string
	workerTree   string
	harness      string
	calls        int
	offline      bool
	// typing lets a live terminal view type into the pane, recorded in typed;
	// sizeless leaves the pane's size out of the snapshot and resized grows it.
	typing   bool
	typed    [][]string
	sizeless bool
	resized  atomic.Bool
}

func (r *cfoRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.calls++
	if r.offline {
		return execx.Result{}, errors.New("Herdr is not running")
	}
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
	case len(a) >= 2 && a[0] == "agent" && a[1] == "get":
		body = fmt.Sprintf(`{"result":{"agent":{"agent":"codex","agent_status":"idle","revision":%d,"state_change_seq":%d}}}`, 10+len(r.prompts), 10+len(r.prompts))
	case len(a) >= 4 && a[0] == "agent" && a[1] == "prompt":
		if r.beforePrompt != nil {
			r.beforePrompt()
		}
		if a[2] != "w1:p1" {
			r.t.Fatalf("wrong pane: %s", a[2])
		}
		r.prompts = append(r.prompts, a[3])
		body = `{"result":{}}`
	case len(a) >= 2 && a[0] == "pane" && (a[1] == "send-text" || a[1] == "send-keys"):
		if !r.typing {
			r.t.Fatal("CFO message reached raw pane typing")
		}
		r.typed = append(r.typed, a)
		body = `{"result":{}}`
	default:
		return execx.Result{}, fmt.Errorf("unexpected Herdr operation: %v", a)
	}
	if r.harness != "" {
		body = strings.ReplaceAll(body, `"agent":"codex"`, `"agent":"`+r.harness+`"`)
	}
	return execx.Result{Stdout: []byte(body)}, nil
}

func primaryFixture(t *testing.T, store *Store) (primaryRegistration, string, *cfoRunner, *CFOConnection) {
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

// registerFixture is a home with no registration and a fake Herdr in which
// this test process is the foreground harness of pane w1:p1.
func registerFixture(t *testing.T) (*Store, *cfoRunner, *CFOConnection) {
	t.Helper()
	store, _ := testStore(t)
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	runner := &cfoRunner{t: t, pid: os.Getpid()}
	return store, runner, &CFOConnection{State: store.Home.State, Terminals: terminal.HerdrSessions(&herdr.Client{Commands: runner, Session: "isolated"})}
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
	stale := primaryRegistration{Target: herdr.Target{Session: "isolated", Pane: "w0:p0"}, Workspace: "w0", Tab: "w0:t0", Agent: "claude", Terminal: "old-terminal", Process: lock.Info{PID: 37680, OwnerPID: 37680, Start: time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC), Hostname: hostname}}
	data, _ := json.Marshal(stale)
	if err := os.WriteFile(filepath.Join(store.Home.State, "primary.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var problem registrationProblem
	if err := cfo.check(ctx); !errors.As(err, &problem) || !strings.Contains(err.Error(), "pid 37680, started 2026-09-15 09:00 UTC, is no longer running; run cfo register in the CFO session") {
		t.Fatalf("stale registration reads as %v, want one registration problem naming the fix", err)
	}

	described, err := Register(ctx, store.Home.State, cfo.Terminals, "", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := "codex pid " + strconv.Itoa(os.Getpid()) + " in Herdr pane isolated:w1:p1"; described != want {
		t.Errorf("described %q, want %q", described, want)
	}
	if err := cfo.check(ctx); err != nil {
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
	ctx := context.Background()
	if _, err := Register(ctx, store.Home.State, cfo.Terminals, "", "session-1"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Home.State, "primary.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfo.PublishQuestion(ctx, "question-1", "Pick a layout", []string{"Board", "Tree"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(ctx, store.Home.State, cfo.Terminals, "", "session-2"); err != nil {
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
	ctx := context.Background()
	if _, err := Register(ctx, store.Home.State, cfo.Terminals, "", ""); err != nil {
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
	if _, err := Register(ctx, store.Home.State, cfo.Terminals, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := cfo.check(ctx); err != nil {
		t.Fatalf("the registration of a recycled pid was kept: %v", err)
	}
}

func TestRegisterRefusesWhatItCannotProve(t *testing.T) {
	for _, c := range []struct {
		name, want string
		arrange    func(*testing.T, *Store, *cfoRunner)
	}{
		{"outside Herdr and native terminals", "neither a Herdr pane nor a native terminal", func(t *testing.T, _ *Store, _ *cfoRunner) {
			t.Setenv("HERDR_PANE_ID", "")
			t.Setenv(host.IDVariable, "")
		}},
		// The System process is live and never the ancestor of a test, so an
		// inherited HERDR_PANE_ID cannot register another session's pane.
		{"a pane this process does not run under", "does not run under it", func(_ *testing.T, _ *Store, runner *cfoRunner) {
			runner.pid = 4
		}},
		{"a shell in the foreground", "no harness in its foreground", func(_ *testing.T, _ *Store, runner *cfoRunner) {
			runner.pid = 1
		}},
		{"another live session holding the home", "another live session holds this home", func(t *testing.T, store *Store, _ *cfoRunner) {
			other := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL")
			if err := other.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = other.Process.Kill(); _, _ = other.Process.Wait() })
			if _, err := lock.AcquireOwner(store.Home.State, other.Process.Pid, "other"); err != nil {
				t.Fatal(err)
			}
		}},
		{"a different agent than the hook names", "Herdr detects codex in pane w1:p1, not pi", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			store, runner, cfo := registerFixture(t)
			harness := ""
			if c.arrange != nil {
				c.arrange(t, store, runner)
			} else {
				harness = "pi"
			}
			_, err := Register(context.Background(), store.Home.State, cfo.Terminals, harness, "")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Register: %v, want a refusal containing %q", err, c.want)
			}
			if registrationExists(t, store) {
				t.Fatal("a refused registration wrote primary.json")
			}
		})
	}
}

func TestBoardShowsTheRegistrationAsOneStateWithItsFix(t *testing.T) {
	store, _, cfo := registerFixture(t)
	service := &Service{Store: store, Options: Options{CFO: cfo}}
	ctx := context.Background()
	service.checkRegistration(ctx)
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Registration != "The CFO is not registered; run cfo register in the CFO session" {
		t.Fatalf("unregistered board shows %q", snapshot.Registration)
	}
	if _, err := Register(ctx, store.Home.State, cfo.Terminals, "", ""); err != nil {
		t.Fatal(err)
	}
	service.checkRegistration(ctx)
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
