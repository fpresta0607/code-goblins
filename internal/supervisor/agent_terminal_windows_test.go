package supervisor

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
)

// A sub-agent's terminal on the board is its own record, read by the
// supervisor for a sub-agent the goblin's tree holds and drawn as lines.
func TestAgentTerminalServesASubagentsOwnRecord(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	meta := treeGoblin(t, h.State, userHome, "running", true)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	writeFile(t, filepath.Join(userHome, ".claude", "projects", "C--work-running", "running-conversation", "subagents", "agent-a1.jsonl"),
		`{"type":"assistant","timestamp":"`+stamp+`","message":{"role":"assistant","content":[{"type":"text","text":"Reading the monitor"}]}}`+"\n")
	service := &Service{Store: store, Instance: "tree", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	service.readTrees(context.Background())
	handler := NewHTTP(service, "board.local", nil)
	get := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local"+path, nil))
		return response
	}

	// Act
	response := get("/api/tasks/" + meta.ID + "/agent?node=" + url.QueryEscape("subagent:toolu_A"))

	// Assert
	var body struct {
		Lines []string `json:"lines"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Lines) != 1 || body.Lines[0] != "● Reading the monitor" {
		t.Fatalf("agent terminal = %d %s, want the sub-agent's own lines", response.Code, response.Body)
	}
	for _, tc := range []struct {
		name, path string
		code       int
	}{
		{"a node the tree does not hold", "/api/tasks/" + meta.ID + "/agent?node=" + url.QueryEscape("subagent:toolu_X"), 404},
		{"no node", "/api/tasks/" + meta.ID + "/agent", 404},
		{"an unknown task", "/api/tasks/nobody/agent?node=" + url.QueryEscape("subagent:toolu_A"), 404},
		{"an invalid task", "/api/tasks/..%5Cx/agent?node=" + url.QueryEscape("subagent:toolu_A"), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if response := get(tc.path); response.Code != tc.code {
				t.Errorf("GET %s = %d %s, want %d", tc.path, response.Code, response.Body, tc.code)
			}
		})
	}
}
