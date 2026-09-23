package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
		r.t.Fatal("CFO message reached raw pane typing")
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
	return primary, identity, runner, &CFOConnection{State: store.Home.State, Herdr: &herdr.Client{Commands: runner}}
}
