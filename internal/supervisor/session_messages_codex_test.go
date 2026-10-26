package supervisor

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// codexGoblin is a task running Codex whose conversation the board recorded,
// with the rollout Codex writes as it works.
func codexGoblin(t *testing.T, stateDir, userHome, id, generation string) state.TaskMeta {
	t.Helper()
	meta := state.TaskMeta{
		ID: id, Project: t.TempDir(), Worktree: filepath.Join(t.TempDir(), id),
		Harness: "codex", Mode: "direct-PR", Kind: "ship", Backend: "native", SpawnGen: generation,
	}
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	session := id + "-session"
	if err := state.WriteConversation(stateDir, state.Conversation{
		ID: id, Generation: generation, Harness: "codex", Session: session, ProvedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(userHome, ".codex", "sessions", "2026", "10", "06")
	writeFile(t, filepath.Join(folder, "rollout-2026-10-06T21-30-00-"+session+".jsonl"), strings.Join([]string{
		`{"timestamp":"` + stampNow() + `","type":"session_meta","payload":{"id":"` + session + `","cwd":"C:\\work\\` + id + `","source":"cli","thread_source":"user"}}`,
		`{"timestamp":"` + stampNow() + `","type":"turn_context","payload":{"model":"gpt-6-astra"}}`,
		`{"timestamp":"` + stampNow() + `","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"user-1","content":[{"type":"text","text":"Why is the card red?"}]}}}`,
		`{"timestamp":"` + stampNow() + `","type":"response_item","payload":{"type":"custom_tool_call","id":"ctc-1","call_id":"call-1","name":"exec","status":"completed","input":"text(await tools.exec_command({cmd:\"git log -1\"}));"}}`,
		`{"timestamp":"` + stampNow() + `","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"agent-1","content":[{"type":"Text","text":"Its gate failed."}]}}}`,
		`{"timestamp":"` + stampNow() + `","type":"token_usage_record","payload":{"usage":{"total_tokens":12}}}`,
		`{"timestamp":"` + stampNow() + `","type":"response_item","payload":{"type":"custom_tool_call_output","id":"ctco-1","call_id":"call-1","output":[{"type":"input_text","text":"Script completed"}]}}`,
	}, "\n")+"\n")
	if err := os.Chtimes(filepath.Join(folder, "rollout-2026-10-06T21-30-00-"+session+".jsonl"), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	return meta
}

// The board reads a Codex rollout as messages too, through the same view a
// Claude Code conversation and a pi session are read through.
func TestMessagesServeACodexSessionsConversation(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	meta := codexGoblin(t, h.State, userHome, "codgester", "s1")
	service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	handler := NewHTTP(service, "127.0.0.1:50000", nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "http://127.0.0.1:50000/api/messages?task="+meta.ID+"&generation="+meta.SpawnGen+"&token=board-token", nil)
	request.RemoteAddr = "127.0.0.1:50001"

	// Act
	handler.ServeHTTP(response, request)

	// Assert: the prompt, the reply with its call and that call's result.
	if response.Code != 200 {
		t.Fatalf("the page answered %d: %s", response.Code, response.Body)
	}
	var page fleettree.MessagePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("the page is not a message page: %v", err)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("the page holds %d messages, want the prompt and the reply: %+v", len(page.Messages), page.Messages)
	}
	if page.Messages[0].Kind != fleettree.MessagePrompt || page.Messages[0].Text != "Why is the card red?" {
		t.Fatalf("the prompt reads as %+v", page.Messages[0])
	}
	blocks := page.Messages[1].Blocks
	if page.Messages[1].Model != "gpt-6-astra" || len(blocks) != 2 || blocks[0].State != "done" || blocks[0].Result != "Script completed" || blocks[1].Text != "Its gate failed." {
		t.Fatalf("the reply reads as %+v", page.Messages[1])
	}
}
