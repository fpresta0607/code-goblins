package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// piGoblin is a task running pi whose conversation the board recorded, with
// the session file pi writes as it works.
func piGoblin(t *testing.T, stateDir, userHome, id, generation string) state.TaskMeta {
	t.Helper()
	meta := state.TaskMeta{
		ID: id, Project: t.TempDir(), Worktree: filepath.Join(t.TempDir(), id),
		Harness: "pi", Mode: "direct-PR", Kind: "ship", Backend: "native", SpawnGen: generation,
	}
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	session := id + "-session"
	if err := state.WriteConversation(stateDir, state.Conversation{
		ID: id, Generation: generation, Harness: "pi", Session: session, ProvedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(userHome, ".pi", "agent", "sessions", "--C--work-"+id+"--")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(folder, "20261006_213000_"+session+".jsonl"), strings.Join([]string{
		`{"type":"session","version":3,"id":"` + session + `","timestamp":"` + stampNow() + `","cwd":"C:\\work\\` + id + `"}`,
		`{"type":"message","id":"u1","parentId":null,"timestamp":"` + stampNow() + `","message":{"role":"user","content":"Why is the card red?"}}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"` + stampNow() + `","message":{"role":"assistant","provider":"acme","model":"thinker-1","stopReason":"toolUse","content":[{"type":"thinking","thinking":"Its gate failed."},{"type":"toolCall","id":"call-1","name":"read","arguments":{"path":"cards.ts"}}]}}`,
		`{"type":"message","id":"t1","parentId":"a1","timestamp":"` + stampNow() + `","message":{"role":"toolResult","toolCallId":"call-1","toolName":"read","isError":false,"content":[{"type":"text","text":"export const red = 1"}]}}`,
		`{"type":"message","id":"a2","parentId":"t1","timestamp":"` + stampNow() + `","message":{"role":"assistant","provider":"acme","model":"thinker-1","stopReason":"stop","content":[{"type":"text","text":"Its gate failed."}]}}`,
	}, "\n")+"\n")
	return meta
}

func stampNow() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// The board reads a pi session as messages too: a page of the branch its file
// holds, and the messages written after a read, while a session of a harness
// the board does not read is refused rather than shown as an empty one.
func TestMessagesServeAPiSessionsConversation(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	meta := piGoblin(t, h.State, userHome, "pipester", "s1")
	service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	handler := NewHTTP(service, "127.0.0.1:50000", nil)
	get := func(query string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "http://127.0.0.1:50000/api/messages?"+query, nil)
		request.RemoteAddr = "127.0.0.1:50001"
		handler.ServeHTTP(response, request)
		return response
	}
	task := "task=" + meta.ID + "&generation=" + meta.SpawnGen + "&token=board-token"

	// Act
	response := get(task)

	// Assert: a page of the conversation, thinking and the call's result with
	// it, which the shared view draws without knowing the harness.
	if response.Code != 200 {
		t.Fatalf("the page answered %d: %s", response.Code, response.Body)
	}
	var page fleettree.MessagePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("the page is not a message page: %v", err)
	}
	if len(page.Messages) != 3 {
		t.Fatalf("the page holds %d messages, want the prompt and both replies", len(page.Messages))
	}
	if page.Messages[0].Kind != fleettree.MessagePrompt || page.Messages[0].Source != fleettree.FromOverlord {
		t.Fatalf("the prompt reads as %+v", page.Messages[0])
	}
	blocks := page.Messages[1].Blocks
	if len(blocks) != 2 || blocks[0].Kind != fleettree.BlockThinking || blocks[1].State != "done" || blocks[1].Result != "export const red = 1" {
		t.Fatalf("the reply reads as %+v", blocks)
	}

	// Act and Assert: a task of a harness the board does not read is refused.
	codex := state.TaskMeta{ID: "codgester", Project: t.TempDir(), Worktree: t.TempDir(), Harness: "codex", Mode: "direct-PR", Kind: "ship", Backend: "native", SpawnGen: "s2"}
	if err := state.WriteTaskMeta(h.State, codex); err != nil {
		t.Fatal(err)
	}
	refused := get("task=" + codex.ID + "&generation=" + codex.SpawnGen + "&token=board-token")
	if refused.Code != 404 || !strings.Contains(refused.Body.String(), "Claude Code and pi") {
		t.Fatalf("a Codex task answered %d: %s", refused.Code, refused.Body)
	}

	// Act: the stream, as a view that stays open reads it.
	// A hang bound on the stream: long enough that no ordinary wait ends it.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	server := httptest.NewUnstartedServer(handler)
	handler.Host = server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/messages?"+task+"&stream=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.RemoteAddr = "127.0.0.1:50001"
	streamed, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer streamed.Body.Close()
	events := bufio.NewReader(streamed.Body)
	first, err := events.ReadString('\n')
	if err != nil || !strings.Contains(first, "event: page") {
		t.Fatalf("the stream opens with %q, %v", first, err)
	}
}

// A pi session pf the CFO's is read from the terminal it names, and a session
// of another harness the CFO runs is refused.
func TestMessagesReadAPiCFOAndRefuseAnotherHarness(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	folder := filepath.Join(userHome, ".pi", "agent", "sessions", "--C--work--")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(folder, "20261006_213000_cfo-session.jsonl"), strings.Join([]string{
		`{"type":"session","version":3,"id":"cfo-session","timestamp":"` + stampNow() + `","cwd":"C:\\work"}`,
		`{"type":"message","id":"u1","parentId":null,"timestamp":"` + stampNow() + `","message":{"role":"user","content":"Ship it?"}}`,
	}, "\n")+"\n")
	registerNativeCFO(t, h.State, "pi", "cfo-session")
	service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	handler := NewHTTP(service, "127.0.0.1:50000", nil)
	get := func(query string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "http://127.0.0.1:50000/api/messages?"+query, nil)
		request.RemoteAddr = "127.0.0.1:50001"
		handler.ServeHTTP(response, request)
		return response
	}

	// Act
	response := get("cfo=" + NativeCFOTerminal + "&token=board-token")

	// Assert
	if response.Code != 200 {
		t.Fatalf("the CFO's page answered %d: %s", response.Code, response.Body)
	}
	var page fleettree.MessagePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 1 || page.Messages[0].Text != "Ship it?" {
		t.Fatalf("the CFO's page reads %+v", page.Messages)
	}
}
