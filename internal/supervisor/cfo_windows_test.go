package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

func TestCFORejectionPreservesFullActionHistory(t *testing.T) {
	for _, shape := range []string{"missing", "changed"} {
		t.Run(shape, func(t *testing.T) {
			store, _ := testStore(t)
			primary, identity, _, _ := primaryFixture(t, store)
			for i := 0; i < maxActions; i++ {
				store.db.Actions = append(store.db.Actions, Action{ID: fmt.Sprintf("completed-%d", i), Kind: "cfo_message", Generation: identity, Text: "retained", Status: "succeeded"})
			}
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			before := store.Snapshot()
			disk, err := os.ReadFile(store.path())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.Home.State, "primary.json")
			if shape == "missing" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				primary.Target.Pane = "w2:p2"
				data, _ := json.Marshal(primary)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.Queue(Action{ID: "new-question", Kind: "cfo_message", Generation: identity, Text: "hello"}); err == nil {
				t.Fatal("invalid primary accepted")
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
		})
	}
}

type cfoRunner struct {
	t                 *testing.T
	pid               int
	prompts           []string
	unregistered      bool
	replaceAtBaseline bool
	beforePrompt      func()
	terminal          string
	workerTree        string
}

func (r *cfoRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	a := req.Args
	var body string
	switch {
	case len(a) >= 2 && a[0] == "api" && a[1] == "snapshot":
		body = `{"result":{"type":"session_snapshot","snapshot":{"protocol":1,"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","terminal_id":"test-terminal"}],"agents":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","agent":"codex","agent_status":"idle"}]}}}`
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
		if r.unregistered {
			return execx.Result{ExitCode: 1, Stdout: []byte(`{"error":{"code":"agent_not_found"}}`)}, nil
		}
		if r.replaceAtBaseline {
			r.pid = 1
		}
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
	case len(a) >= 2 && a[0] == "pane" && (a[1] == "read" || a[1] == "capture"):
		return execx.Result{Stdout: []byte("Native CFO response\nSecond line\n")}, nil
	case len(a) >= 2 && a[0] == "pane" && (a[1] == "send-text" || a[1] == "send-keys"):
		r.t.Fatal("CFO message reached raw pane typing")
	default:
		return execx.Result{}, fmt.Errorf("unexpected Herdr operation: %v", a)
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
	return primary, identity, runner, &CFOConnection{State: store.Home.State, Herdr: &herdr.Client{Commands: runner}}
}

func TestCFOMessageBindsRegistrationAndNeverReplays(t *testing.T) {
	store, _ := testStore(t)
	primary, identity, runner, connection := primaryFixture(t, store)
	service := &Service{Store: store, Options: Options{CFO: connection}}
	a := Action{ID: "cfo-question", Kind: "cfo_message", Generation: identity, Text: "Which task needs my attention?"}
	if _, err := store.Queue(a); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(a); err != nil {
		t.Fatal(err)
	}
	// Windows holds the exact registration file stable for the whole native
	// operation. A concurrent primary replacement cannot silently retarget it.
	runner.beforePrompt = func() {
		primary.Target.Pane = "w2:p2"
		data, _ := json.Marshal(primary)
		if err := os.WriteFile(filepath.Join(store.Home.State, "primary.json"), data, 0600); err == nil {
			t.Fatal("primary changed during delivery")
		}
	}
	if err := store.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	if len(runner.prompts) != 1 || store.Snapshot().Actions[0].Status != "succeeded" {
		t.Fatalf("outcome: %+v", store.Snapshot().Actions)
	}
	data, _ := json.Marshal(primary)
	if err := os.WriteFile(filepath.Join(store.Home.State, "primary.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(a); err != nil {
		t.Fatal("known outcome should remain idempotent", err)
	}
	newAction := a
	newAction.ID = "old-recipient"
	if _, err := store.Queue(newAction); err == nil {
		t.Fatal("stale primary accepted")
	}
	reopened, err := Open(store.Home)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	if len(runner.prompts) != 1 {
		t.Fatal("CFO question replayed")
	}
}

func TestCFOMessageRefusesLostAgentAndChangedProcess(t *testing.T) {
	for _, shape := range []string{"missing", "changed-process", "changed-registration"} {
		t.Run(shape, func(t *testing.T) {
			store, _ := testStore(t)
			primary, identity, runner, connection := primaryFixture(t, store)
			service := &Service{Store: store, Options: Options{CFO: connection}}
			if _, err := store.Queue(Action{ID: "cfo-refuse", Kind: "cfo_message", Generation: identity, Text: "hello"}); err != nil {
				t.Fatal(err)
			}
			switch shape {
			case "missing":
				runner.unregistered = true
			case "changed-process":
				runner.replaceAtBaseline = true
			case "changed-registration":
				primary.Target.Pane = "w2:p2"
				data, _ := json.Marshal(primary)
				if err := os.WriteFile(filepath.Join(store.Home.State, "primary.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.ProcessOne(context.Background(), service.execute); err != nil {
				t.Fatal(err)
			}
			if len(runner.prompts) != 0 || store.Snapshot().Actions[0].Status == "succeeded" {
				t.Fatal("changed recipient received a message")
			}
		})
	}
}
