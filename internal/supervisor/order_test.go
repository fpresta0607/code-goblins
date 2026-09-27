package supervisor

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const orderToken = "instance-token"

func orderBoard(t *testing.T) (*HTTP, home.Home) {
	t.Helper()
	store, h := testStore(t)
	if err := os.MkdirAll(h.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	return NewHTTP(&Service{Store: store, Instance: orderToken}, "board.local", nil), h
}

func postOrder(handler *HTTP, body, origin, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "http://board.local/api/order", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if token != "" {
		request.Header.Set("X-CFO-Token", token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func snapshotIDs(t *testing.T, handler *HTTP, phase func(Task) bool) []string {
	t.Helper()
	snapshot, err := handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, task := range snapshot.Tasks {
		if phase(task) {
			ids = append(ids, task.ID)
		}
	}
	return ids
}

func queued(task Task) bool { return task.Phase == "queued" }

func TestOrderQueuedSavesTheBacklogOrderTheCFODispatchesIn(t *testing.T) {
	// Arrange
	handler, h := orderBoard(t)
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- **first** - First (repo: code-goblins)\n  detail: kept with its row\n- **second** - Second (repo: code-goblins)\n")
	writeFile(t, filepath.Join(h.Data, "briefed", "brief.md"), "# Brief briefed\n\n## Project\n\nC:\\dev\\PrecisionDocs-AI\n\n## Task\n\nDo it.\n")

	// Act
	response := postOrder(handler, `{"list":"queued","order":["briefed","second","first"]}`, "http://board.local", orderToken)

	// Assert
	if response.Code != 200 {
		t.Fatalf("order = %d %s", response.Code, response.Body)
	}
	data, err := os.ReadFile(filepath.Join(h.Data, "backlog.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "## Queued\n- **briefed** - briefed (repo: PrecisionDocs-AI)\n- **second** - Second (repo: code-goblins)\n- **first** - First (repo: code-goblins)\n  detail: kept with its row\n"; got != want {
		t.Fatalf("backlog.md = %q, want %q", got, want)
	}
	if got := snapshotIDs(t, handler, queued); !reflect.DeepEqual(got, []string{"briefed", "second", "first"}) {
		t.Fatalf("Tasks lists %v, want the saved order", got)
	}
}

func TestOrderQueuedRefusesAnOrderTheBacklogNoLongerHolds(t *testing.T) {
	// Arrange
	handler, h := orderBoard(t)
	const content = "## Queued\n- **first** - First\n- **second** - Second\n- **third** - Added by the CFO meanwhile\n"
	writeFile(t, filepath.Join(h.Data, "backlog.md"), content)

	// Act
	response := postOrder(handler, `{"list":"queued","order":["second","first"]}`, "http://board.local", orderToken)

	// Assert
	if response.Code != 409 || !strings.Contains(response.Body.String(), "The queue changed") {
		t.Fatalf("stale order = %d %s, want 409 naming the change", response.Code, response.Body)
	}
	if data, _ := os.ReadFile(filepath.Join(h.Data, "backlog.md")); string(data) != content {
		t.Fatalf("a refused order rewrote backlog.md: %q", data)
	}
}

func TestOrderInProgressSetsTheAttentionOrderTheSnapshotLists(t *testing.T) {
	// Arrange
	handler, h := orderBoard(t)
	for _, id := range []string{"task-2", "task-3"} {
		if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: id, Project: h.Root, Worktree: h.Root, Harness: "claude", Mode: "no-mistakes", Kind: "ship", Backend: "native", SpawnGen: "g1"}); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	response := postOrder(handler, `{"list":"progress","order":["task-3","task-1","task-2"]}`, "http://board.local", orderToken)

	// Assert
	if response.Code != 200 {
		t.Fatalf("order = %d %s", response.Code, response.Body)
	}
	if saved, err := fleet.ReadAttention(h); err != nil || !reflect.DeepEqual(saved, []string{"task-3", "task-1", "task-2"}) {
		t.Fatalf("attention order = %v, %v", saved, err)
	}
	if got := snapshotIDs(t, handler, func(task Task) bool { return !queued(task) }); !reflect.DeepEqual(got, []string{"task-3", "task-1", "task-2"}) {
		t.Fatalf("In progress lists %v, want the saved order", got)
	}
}

func TestOrderRefusesBadRequests(t *testing.T) {
	tests := []struct {
		name, body, origin, token string
		code                      int
	}{
		{name: "no token", body: `{"list":"queued","order":[]}`, origin: "http://board.local", code: 403},
		{name: "wrong token", body: `{"list":"queued","order":[]}`, origin: "http://board.local", token: "stale", code: 403},
		{name: "no origin", body: `{"list":"queued","order":[]}`, token: orderToken, code: 403},
		{name: "another origin", body: `{"list":"queued","order":[]}`, origin: "https://evil.invalid", token: orderToken, code: 403},
		{name: "unknown list", body: `{"list":"done","order":["task-1"]}`, origin: "http://board.local", token: orderToken, code: 400},
		{name: "invalid task id", body: `{"list":"progress","order":["../task"]}`, origin: "http://board.local", token: orderToken, code: 400},
		{name: "unknown field", body: `{"list":"progress","order":[],"extra":1}`, origin: "http://board.local", token: orderToken, code: 400},
		{name: "a goblin not in progress", body: `{"list":"progress","order":["task-9"]}`, origin: "http://board.local", token: orderToken, code: 409},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			handler, h := orderBoard(t)
			writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n")

			// Act
			response := postOrder(handler, test.body, test.origin, test.token)

			// Assert
			if response.Code != test.code {
				t.Fatalf("order = %d %s, want %d", response.Code, response.Body, test.code)
			}
			if saved, _ := fleet.ReadAttention(h); saved != nil {
				t.Fatalf("a refused order saved %v", saved)
			}
		})
	}
}

func TestQueuedBriefNamesItsProject(t *testing.T) {
	// Arrange
	_, h := orderBoard(t)
	writeFile(t, filepath.Join(h.Data, "briefed", "brief.md"), "# Brief briefed\r\n\r\n## Project\r\n\r\nC:\\dev\\code-goblins\r\n")

	// Act
	briefs := queuedBriefs(h)

	// Assert
	if len(briefs) != 1 || briefs[0].Project != "code-goblins" {
		t.Fatalf("queued briefs = %+v, want briefed in code-goblins", briefs)
	}
}
