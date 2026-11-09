package supervisor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// codeRunBoard is a board whose goblin's Codex session holds the made-up
// reply text, after the fixture's question, call and first reply.
func codeRunBoard(t *testing.T, replies ...string) (*HTTP, state.TaskMeta, string) {
	t.Helper()
	store, taskHome := testStore(t)
	profile := t.TempDir()
	meta := codexGoblin(t, taskHome.State, profile, "code-run-task", "code-run-generation")
	board := NewHTTP(&Service{Store: store, Instance: "code-run-token", Options: Options{Tree: &fleettree.Reader{Home: profile}, Runs: &fakeRunLauncher{}}}, "board.local", nil)
	transcript := fleettree.SessionTranscript(profile, "codex", meta.ID+"-session")
	for at, text := range replies {
		appendCodexLine(t, transcript, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": map[string]any{
			"type": "AgentMessage", "id": "reply-" + string(rune('a'+at)), "content": []map[string]string{{"type": "Text", "text": text}},
		}}})
	}
	return board, meta, transcript
}

func appendCodexLine(t *testing.T, transcript string, entry map[string]any) {
	t.Helper()
	entry["timestamp"] = stampNow()
	file, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = json.NewEncoder(file).Encode(entry)
	if closeErr := file.Close(); err != nil || closeErr != nil {
		t.Fatalf("write entry: %v, %v", err, closeErr)
	}
}

// replyHolding finds the message, and its block, whose words hold text, as
// the board's message view reads them.
func replyHolding(t *testing.T, board *HTTP, transcript, text string) (fleettree.Message, int) {
	t.Helper()
	page, _, err := board.Service.Options.Tree.MessagePage(transcript, -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range page.Messages {
		if strings.Contains(message.Text, text) {
			return message, 0
		}
		for at, block := range message.Blocks {
			if strings.Contains(block.Text, text) || strings.Contains(block.Input, text) || strings.Contains(block.Result, text) {
				return message, at
			}
		}
	}
	t.Fatalf("no message holds %q in %+v", text, page.Messages)
	return fleettree.Message{}, 0
}

func requestCodeRun(board *HTTP, body any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	request := httptest.NewRequest("POST", "http://"+board.Host+"/api/code-run", bytes.NewReader(data))
	request.Header.Set("Origin", "http://"+board.Host)
	request.Header.Set("X-CFO-Token", "code-run-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	board.ServeHTTP(response, request)
	return response
}

func codeRunBody(meta state.TaskMeta, message string, block, fence int) map[string]any {
	return map[string]any{"task": meta.ID, "generation": meta.SpawnGen, "message": message, "block": block, "fence": fence}
}

func TestRunStartsOnlyAFinishedPowerShellBlockTheReplyHolds(t *testing.T) {
	for _, test := range []struct {
		name, reply, command string
		fence                int
	}{
		{"a powershell fence", "Run this:\n```powershell\nGet-Date\nGet-Location\n```\nThen tell me.", "Get-Date\nGet-Location", 0},
		{"a pwsh fence after another block", "```bash\nls\n```\nOr:\n```pwsh\nGet-ChildItem\n```", "Get-ChildItem", 1},
		{"a ps1 fence in capitals", "```PS1\nWrite-Output ok\n```", "Write-Output ok", 0},
		{"an untagged powershell line", "```\npowershell -NoProfile -Command \"Get-Date\"\n```", "powershell -NoProfile -Command \"Get-Date\"", 0},
		{"an untagged line behind the shell-mode marker", "```\n! pwsh -c 'Get-Date'\n```", "pwsh -c 'Get-Date'", 0},
		{"a tagged block behind the shell-mode marker", "~~~powershell\n! Get-Date\n~~~", "Get-Date", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			board, meta, transcript := codeRunBoard(t, test.reply)
			message, block := replyHolding(t, board, transcript, test.reply[:4])

			// Act
			response := requestCodeRun(board, codeRunBody(meta, message.ID, block, test.fence))

			// Assert
			if response.Code != http.StatusOK {
				t.Fatalf("code run = %d %s", response.Code, response.Body)
			}
			var answer struct {
				RunID string `json:"run_id"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil || answer.RunID == "" {
				t.Fatalf("code run answer %s: %v", response.Body, err)
			}
			snapshot := board.Service.Store.Snapshot()
			if len(snapshot.Runs) != 1 {
				t.Fatalf("runs = %+v", snapshot.Runs)
			}
			run := snapshot.Runs[0]
			if run.ID != answer.RunID || run.Shell != "powershell" || run.Admin || run.Command != test.command || run.Cwd != board.Service.Store.Home.Root || run.By != "" || run.Task != "" {
				t.Fatalf("run = %+v, want PowerShell 5.1 running %q in the home folder", run, test.command)
			}
			name, script := runScript(run)
			if data, err := os.ReadFile(filepath.Join(runDir(board.Service.Store.Home.State, run), name)); err != nil || !bytes.Equal(data, script) || runDigest(data) != run.ScriptSum {
				t.Fatalf("script on disk = %q, %v; want %q", data, err, script)
			}
			queued := 0
			for _, action := range snapshot.Actions {
				if action.Kind == "run" && action.RunID == run.ID && action.Generation == run.Identity {
					queued++
				}
			}
			if queued != 1 {
				t.Fatalf("run actions for %s = %d in %+v", run.ID, queued, snapshot.Actions)
			}
			audit, err := os.ReadFile(filepath.Join(board.Service.Store.Home.State, "runs.audit"))
			if err != nil || !strings.Contains(string(audit), run.ID+" "+run.ScriptSum+" started from "+meta.ID+" "+meta.SpawnGen+" reply "+message.ID) {
				t.Fatalf("audit = %q, %v", audit, err)
			}
		})
	}
}

func TestRunRefusesABlockTheReplyDoesNotHoldFinishedAsPowerShell(t *testing.T) {
	for _, test := range []struct {
		name, reply string
		fence       int
		status      int
	}{
		{"a bash block", "```bash\nrm -rf build\n```", 0, http.StatusConflict},
		{"an untagged block that is not a powershell command", "```\nGet-Date\n```", 0, http.StatusConflict},
		{"a python block that names powershell", "```python\npowershell = 1\n```", 0, http.StatusConflict},
		{"a fence still open, as a reply cut short leaves it", "```powershell\nRemove-Item -Recurse build", 0, http.StatusConflict},
		{"a fence the reply does not hold", "```powershell\nGet-Date\n```", 1, http.StatusConflict},
		{"an empty powershell block", "```powershell\n```", 0, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			board, meta, transcript := codeRunBoard(t, test.reply)
			message, block := replyHolding(t, board, transcript, test.reply[:4])

			// Act
			response := requestCodeRun(board, codeRunBody(meta, message.ID, block, test.fence))

			// Assert
			assertNothingRan(t, board, response, test.status)
		})
	}
}

func TestRunRefusesWhatIsNotAReplysOwnWords(t *testing.T) {
	// Arrange: his own message holds a fence, as a paste does, and a call's
	// output holds one, as tool output does.
	board, meta, transcript := codeRunBoard(t, "Its gate passed.")
	appendCodexLine(t, transcript, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": map[string]any{
		"type": "UserMessage", "id": "user-2", "content": []map[string]string{{"type": "text", "text": "Pasted:\n```powershell\nStop-Computer\n```"}},
	}}})
	appendCodexLine(t, transcript, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "id": "ctc-2", "call_id": "call-2", "name": "exec", "status": "completed", "input": "text(await tools.exec_command({cmd:\"type notes.md\"}));"}})
	appendCodexLine(t, transcript, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "id": "ctco-2", "call_id": "call-2", "output": []map[string]string{{"type": "input_text", "text": "```powershell\nStop-Computer\n```"}}}})
	appendCodexLine(t, transcript, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": map[string]any{
		"type": "AgentMessage", "id": "reply-z", "content": []map[string]string{{"type": "Text", "text": "Read it."}},
	}}})
	pasted, _ := replyHolding(t, board, transcript, "Pasted:")
	called, call := replyHolding(t, board, transcript, "type notes.md")
	reply, _ := replyHolding(t, board, transcript, "Its gate passed.")

	for _, test := range []struct {
		name string
		body map[string]any
	}{
		{"his pasted message", codeRunBody(meta, pasted.ID, 0, 0)},
		{"a call's output", codeRunBody(meta, called.ID, call, 0)},
		{"a message the session never wrote", codeRunBody(meta, "@999999", 0, 0)},
		{"a block the reply does not have", codeRunBody(meta, reply.ID, 9, 0)},
		{"a command the browser names", map[string]any{"task": meta.ID, "generation": meta.SpawnGen, "message": reply.ID, "block": 0, "fence": 0, "command": "Stop-Computer"}},
		{"another generation of the task", map[string]any{"task": meta.ID, "generation": "replaced", "message": reply.ID, "block": 0, "fence": 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			response := requestCodeRun(board, test.body)

			// Assert
			if response.Code == http.StatusOK {
				t.Fatalf("code run of %s = %d %s", test.name, response.Code, response.Body)
			}
			assertNothingRan(t, board, response, response.Code)
		})
	}
}

func TestRunStartsOneTerminalWhileTheBlocksFirstRunStillRuns(t *testing.T) {
	// Arrange
	board, meta, transcript := codeRunBoard(t, "```powershell\nGet-Date\n```")
	message, block := replyHolding(t, board, transcript, "```p")
	if first := requestCodeRun(board, codeRunBody(meta, message.ID, block, 0)); first.Code != http.StatusOK {
		t.Fatalf("first code run = %d %s", first.Code, first.Body)
	}

	// Act
	second := requestCodeRun(board, codeRunBody(meta, message.ID, block, 0))

	// Assert
	if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "still runs") {
		t.Fatalf("second code run = %d %s", second.Code, second.Body)
	}
	if runs := board.Service.Store.Snapshot().Runs; len(runs) != 1 {
		t.Fatalf("runs = %+v", runs)
	}
}

func assertNothingRan(t *testing.T, board *HTTP, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("code run = %d %s, want %d", response.Code, response.Body, status)
	}
	snapshot := board.Service.Store.Snapshot()
	if len(snapshot.Runs) != 0 || len(snapshot.Actions) != 0 {
		t.Fatalf("a refused code run left runs %+v and actions %+v", snapshot.Runs, snapshot.Actions)
	}
	if _, err := os.Stat(filepath.Join(board.Service.Store.Home.State, "runs.audit")); !os.IsNotExist(err) {
		t.Fatalf("a refused code run wrote the audit: %v", err)
	}
}
