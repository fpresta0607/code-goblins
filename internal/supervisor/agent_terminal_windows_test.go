package supervisor

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
)

// A child's terminal on the board is its own output, read by the supervisor
// for a child the goblin's tree holds and drawn as lines: a sub-agent's own
// record, and a background shell's command and what it wrote, never the
// goblin's terminal and never another child's.
func TestAgentTerminalServesEachChildsOwnOutput(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	meta := treeGoblin(t, h.State, userHome, "running", true)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	writeFile(t, filepath.Join(userHome, ".claude", "projects", "C--work-running", "running-conversation", "subagents", "agent-a1.jsonl"),
		`{"type":"assistant","timestamp":"`+stamp+`","message":{"role":"assistant","content":[{"type":"text","text":"Reading the monitor"}]}}`+"\n")
	output := filepath.Join(userHome, "tasks", "b1.output")
	writeFile(t, output, "ok  internal/monitor 41.2s\n")
	quoted, err := json.Marshal("Command running in background with ID: b1. Output is being written to: " + output)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := os.OpenFile(filepath.Join(userHome, ".claude", "projects", "C--work-running", "running-conversation.jsonl"), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conversation.WriteString(`{"type":"assistant","timestamp":"` + stamp + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_S","name":"Bash","input":{"command":"go test ./internal/monitor/","description":"Run the monitor tests","run_in_background":true}}]}}` + "\n" +
		`{"type":"user","timestamp":"` + stamp + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_S","content":` + string(quoted) + `}]},"toolUseResult":{"backgroundTaskId":"b1"}}` + "\n")
	if closeErr := conversation.Close(); err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	service := &Service{Store: store, Instance: "tree", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	service.readTrees(context.Background())
	handler := NewHTTP(service, "board.local", nil)
	get := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local"+path, nil))
		return response
	}

	for _, tc := range []struct {
		node string
		want []string
	}{
		{"subagent:toolu_A", []string{"● Reading the monitor"}},
		{"shell:b1", []string{"> go test ./internal/monitor/", "ok  internal/monitor 41.2s"}},
	} {
		t.Run(tc.node, func(t *testing.T) {
			// Act
			response := get("/api/tasks/" + meta.ID + "/agent?node=" + url.QueryEscape(tc.node))

			// Assert
			var body struct {
				Lines []string `json:"lines"`
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || !slices.Equal(body.Lines, tc.want) {
				t.Fatalf("terminal of %s = %d %s, want %q", tc.node, response.Code, response.Body, tc.want)
			}
		})
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
