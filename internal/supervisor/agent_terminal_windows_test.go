package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
)

// A child's terminal on the board is its own output, read by the supervisor
// for a child the goblin's tree holds: a sub-agent's own record as the steps
// Claude Code draws, and a background shell's command and what it wrote as
// lines, never the goblin's terminal and never another child's.
func TestAgentTerminalServesEachChildsOwnOutput(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	meta := treeGoblin(t, h.State, userHome, "running", true)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	record := filepath.Join(userHome, ".claude", "projects", "C--work-running", "running-conversation", "subagents", "agent-a1.jsonl")
	said := func(text string) string {
		return `{"type":"assistant","timestamp":"` + stamp + `","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
	}
	writeFile(t, record, said("Reading the monitor"))
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
	handler := NewHTTP(service, "127.0.0.1:50000", nil)
	get := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "http://127.0.0.1:50000"+path, nil)
		request.RemoteAddr = "127.0.0.1:50001"
		handler.ServeHTTP(response, request)
		return response
	}

	for _, tc := range []struct {
		node string
		want fleettree.Output
	}{
		{"subagent:toolu_A", fleettree.Output{Entries: []fleettree.AgentEntry{{Kind: fleettree.EntryReply, Text: "Reading the monitor"}}}},
		{"shell:b1", fleettree.Output{Lines: []string{"> go test ./internal/monitor/", "ok  internal/monitor 41.2s", ""}}},
	} {
		t.Run(tc.node, func(t *testing.T) {
			// Act
			response := get("/api/tasks/" + meta.ID + "/agent?node=" + url.QueryEscape(tc.node))

			// Assert
			var body fleettree.Output
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || !reflect.DeepEqual(body, tc.want) {
				t.Fatalf("terminal of %s = %d %s, want %+v", tc.node, response.Code, response.Body, tc.want)
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
	t.Run("refuses remote and proxied output reads", func(t *testing.T) {
		for _, proxied := range []bool{false, true} {
			request := httptest.NewRequest("GET", "http://127.0.0.1:50000/api/tasks/"+meta.ID+"/agent?node=shell:b1&stream=1", nil)
			request.RemoteAddr = "192.0.2.2:50001"
			if proxied {
				request.RemoteAddr = "127.0.0.1:50001"
				request.Header.Set("X-Forwarded-For", "192.0.2.2")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "41.2s") {
				t.Fatalf("exposed output: %d %s", response.Code, response.Body)
			}
		}
	})
	server := httptest.NewUnstartedServer(handler)
	handler.Host = server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	stream := func(t *testing.T, node string) func(want string) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		t.Cleanup(cancel)
		request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/tasks/"+meta.ID+"/agent?node="+url.QueryEscape(node)+"&stream=1", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		if response.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("not streaming: %s", response.Header.Get("Content-Type"))
		}
		scanner := bufio.NewScanner(response.Body)
		return func(want string) {
			t.Helper()
			for scanner.Scan() {
				if strings.HasPrefix(scanner.Text(), "data: ") && strings.Contains(scanner.Text(), want) {
					return
				}
			}
			t.Fatalf("stream did not send %q: %v", want, scanner.Err())
		}
	}
	t.Run("streams a working sub-agent's new steps as it writes them", func(t *testing.T) {
		read := stream(t, "subagent:toolu_A")
		read(`{"entries":[{"kind":"reply","text":"Reading the monitor"}],"lines":null}`)
		writeFile(t, record, said("Reading the monitor")+said("Found the stall rule"))
		read(`"text":"Found the stall rule"`)
	})
	t.Run("streams new output without another tree read", func(t *testing.T) {
		read := stream(t, "shell:b1")
		read("41.2s")
		writeFile(t, output, "ok  internal/monitor 41.2s\nnext result\n")
		read("next result")
		service.mu.Lock()
		delete(service.trees, meta.ID)
		service.mu.Unlock()
		read("no longer available")
	})
}
