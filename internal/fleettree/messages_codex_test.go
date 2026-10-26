package fleettree

import (
	"path/filepath"
	"strings"
	"reflect"
	"testing"
	"time"
)

// The entries below are made up in the shape Codex 0.160 writes a rollout in:
// its opening session_meta, then one entry per event, the conversation items
// as each completes and the calls' own response_items around them.

func codexLine(kind string, payload object) object {
	return object{"timestamp": at.Format(time.RFC3339Nano), "type": kind, "payload": payload}
}

func codexMeta(session, cwd string) object {
	return codexLine("session_meta", object{"id": session, "session_id": session, "timestamp": at.Format(time.RFC3339Nano), "cwd": cwd, "source": "cli", "thread_source": "user"})
}

func codexTurnContext(model string) object {
	return codexLine("turn_context", object{"model": model, "cwd": workDir})
}

// codexCompleted is one item of Codex's conversation stream as it
// completes.
func codexCompleted(kind string, fields object) object {
	item := object{"type": kind, "id": kind + "-1"}
	for key, value := range fields {
		item[key] = value
	}
	return codexLine("event_msg", object{"type": "item_completed", "item": item})
}

func codexUserSaid(text string) object {
	return codexCompleted("UserMessage", object{"id": "user-1", "content": []object{{"type": "text", "text": text}}})
}

func codexAgentSaid(text string) object {
	return codexCompleted("AgentMessage", object{"content": []object{{"type": "Text", "text": text}}})
}

// codexReasoning is a reasoning item as Codex writes it: its summary empty and
// its reasoning encrypted.
func codexReasoning() object {
	return codexCompleted("Reasoning", object{"summary_text": []object{}, "raw_content": []object{}, "encrypted_content": "gAAAAA"})
}

// codexCall is the response_item Codex writes as a call starts.
func codexCall(callID, name, input string) object {
	return codexLine("response_item", object{"type": "custom_tool_call", "id": "ctc-" + callID, "call_id": callID, "name": name, "status": "completed", "input": input})
}

// codexOutput is the response_item Codex writes as that call's tool returns.
func codexOutput(callID, text string) object {
	return codexLine("response_item", object{"type": "custom_tool_call_output", "id": "ctco-" + callID, "call_id": callID, "output": []object{{"type": "input_text", "text": text}}})
}

// codexUsage is the usage record Codex writes as each model response ends.
func codexUsage() object {
	return codexLine("token_usage_record", object{"usage": object{"total_tokens": 12}})
}

// codexFileChange is a FileChange item: what a call's edits did to each
// file.
func codexFileChange(changes object) object {
	return codexCompleted("FileChange", object{"id": "exec-" + "7", "changes": changes})
}

// codexRollout writes a rollout for session under home and returns its path.
func codexRollout(t *testing.T, home, session string, entries ...any) string {
	t.Helper()
	path := filepath.Join(home, ".codex", "sessions", "2026", "10", "06", "rollout-2026-10-06T21-00-00-"+session+".jsonl")
	writeLines(t, path, at, entries...)
	return path
}

func readCodex(t *testing.T, reader *Reader, path string) MessagePage {
	t.Helper()
	page, _, err := reader.MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

// The board reads a Codex rollout as the conversation Codex draws on its own
// screen: the prompts it was given, an answer per model response with the
// model that wrote it, and each call with the result that answered it.
func TestACodexRolloutIsReadAsItsConversation(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := codexRollout(t, home, "01a16c37-1768-7270-8f61-76f09c77c962",
		codexMeta("01a16c37-1768-7270-8f61-76f09c77c962", workDir),
		codexTurnContext("gpt-6-astra"),
		codexUserSaid("Read the brief at C:\\state\\brief.md and follow it exactly: it is your instruction from the CFO."),
		codexReasoning(),
		codexAgentSaid("I will read the brief first."),
		codexCall("call-1", "exec", "text(await tools.exec_command({cmd:\"Get-Content brief.md\"}));"),
		codexUsage(),
		codexOutput("call-1", "Script completed\nWall time 0.4 seconds\nOutput:\n{\"exit_code\":0}"),
		codexUserSaid("CFO: steer left"),
		codexTurnContext("gpt-6-luna"),
		codexAgentSaid("Both done."),
		codexUsage(),
	)
	reader := Reader{Home: home}

	// Act
	page := readCodex(t, &reader, path)

	// Assert: the brief as the CFO's own words, then a reply per model
	// response with its model, its call and that call's result.
	want := []Message{
		{ID: "user-1", Seq: 0, Kind: MessagePrompt, At: at, Source: FromCFO, Text: "Read the brief at C:\\state\\brief.md and follow it exactly: it is your instruction from the CFO."},
		{ID: "@", Seq: 1, Kind: MessageReply, At: at, Model: "gpt-6-astra", Blocks: []Block{
			{Kind: BlockText, Text: "I will read the brief first."},
			{Kind: BlockCall, CallID: "call-1", Tool: "exec", Subject: "text(await tools.exec_command({cmd:\"Get-Content brief.md\"}));", State: "done", Result: "Script completed\nWall time 0.4 seconds\nOutput:\n{\"exit_code\":0}"},
		}},
		{ID: "user-1", Seq: 2, Kind: MessagePrompt, At: at, Source: FromCFO, Text: "steer left"},
		{ID: "@", Seq: 3, Kind: MessageReply, At: at, Model: "gpt-6-luna", Blocks: []Block{{Kind: BlockText, Text: "Both done."}}},
	}
	assertMessages(t, page.Messages, want)
}

// A call Codex has started is shown while it runs, and its result fills it in
// as the output lands, which a view that stays open reads as a changed message
// rather than a new one.
func TestACodexCallIsReadRunningUntilItsOutputLands(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := codexRollout(t, home, "01a16c37-1768-7270-8f61-76f09c77c962",
		codexMeta("01a16c37-1768-7270-8f61-76f09c77c962", workDir),
		codexTurnContext("gpt-6-astra"),
		codexUserSaid("Run the tests."),
		codexCall("call-1", "exec", "text(await tools.exec_command({cmd:\"go test ./...\"}));"),
	)
	reader := Reader{Home: home}
	page, cursor, err := reader.MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	appendLines(t, path, codexOutput("call-1", "Script completed\nWall time 30.0 seconds\nOutput:\n{\"exit_code\":0}"))
	changed, _, isAnew, err := reader.MessagesSince(path, cursor)

	// Assert: the call ran while its output was unwritten, and the same
	// message is what the output changed.
	block := page.Messages[1].Blocks[0]
	if block.State != CallRunning || block.Result != "" {
		t.Fatalf("the call before its output reads as %+v, want it running", block)
	}
	if err != nil || isAnew || len(changed) != 1 || changed[0].Seq != 1 {
		t.Fatalf("MessagesSince = %+v, anew %t, %v; want the reply alone, changed", changed, isAnew, err)
	}
	if got := changed[0].Blocks[0]; got.State != "done" || got.Result == "" {
		t.Fatalf("the call after its output reads as %+v, want it done with its result", got)
	}
}

// A Codex edit shows the diff Codex recorded for it, as a Claude Code edit
// does, for a file it changed and one it wrote.
func TestACodexEditShowsTheDiffItRecorded(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := codexRollout(t, home, "01a16c37-1768-7270-8f61-76f09c77c962",
		codexMeta("01a16c37-1768-7270-8f61-76f09c77c962", workDir),
		codexTurnContext("gpt-6-astra"),
		codexUserSaid("Fix the count."),
		codexCall("call-1", "exec", "text(await tools.apply_patch(\"*** Begin Patch\"));"),
		codexFileChange(object{
			"C:\\work\\repo\\count.go": object{"type": "update", "unified_diff": "@@ -1,2 +1,2 @@\n-const n = 1\n+const n = 2\n  \n"},
			"C:\\work\\repo\\notes.md":  object{"type": "add", "content": "It counts.\n"},
		}),
		codexUsage(),
		codexOutput("call-1", "Script completed\nWall time 1.0 seconds\nOutput:\n{}"),
	)
	reader := Reader{Home: home}

	// Act
	page := readCodex(t, &reader, path)

	// Assert
	edit := page.Messages[1].Blocks[0].Edit
	if edit.Added != 2 || edit.Removed != 1 {
		t.Fatalf("the edit counts %d added and %d removed, want two and one", edit.Added, edit.Removed)
	}
	want := []string{"@@ -1,2 +1,2 @@", "-const n = 1", "+const n = 2", "  ", "+It counts."}
	if len(edit.Diff) != len(want) {
		t.Fatalf("the edit's diff is %q, want %q", edit.Diff, want)
	}
	for i, line := range want {
		if edit.Diff[i] != line {
			t.Fatalf("the edit's diff is %q, want %q", edit.Diff, want)
		}
	}
}

// A compaction is shown once, though Codex writes it as a record and again as
// an item of its own conversation stream.
func TestACodexCompactionIsShownOnce(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := codexRollout(t, home, "01a16c37-1768-7270-8f61-76f09c77c962",
		codexMeta("01a16c37-1768-7270-8f61-76f09c77c962", workDir),
		codexTurnContext("gpt-6-astra"),
		codexUserSaid("Carry on."),
		codexLine("compacted", object{"message": ""}),
		codexCompleted("ContextCompaction", object{}),
		codexAgentSaid("Carrying on."),
		codexUsage(),
	)
	reader := Reader{Home: home}

	// Act
	page := readCodex(t, &reader, path)

	// Assert
	var compactions int
	for _, message := range page.Messages {
		if message.Kind == MessageCompaction {
			compactions++
		}
	}
	if compactions != 1 {
		t.Fatalf("the conversation holds %d compactions, want the one Codex recorded: %+v", compactions, page.Messages)
	}
}

// A child agent's message reaches the board as a notice, as a Claude Code
// goblin reads one's task notification.
func TestACodexChildAgentsMessageIsShownAsANotice(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := codexRollout(t, home, "01a16c37-1768-7270-8f61-76f09c77c962",
		codexMeta("01a16c37-1768-7270-8f61-76f09c77c962", workDir),
		codexTurnContext("gpt-6-astra"),
		codexUserSaid("Review the diff."),
		codexLine("response_item", object{"type": "agent_message", "id": "amsg-1", "author": "/root/reviewer", "recipient": "/root", "content": []object{{"type": "input_text", "text": "Message Type: FINAL_ANSWER\nSender: /root/reviewer\nPayload:\nNo findings."}}}),
	)
	reader := Reader{Home: home}

	// Act
	page := readCodex(t, &reader, path)

	// Assert
	if len(page.Messages) != 2 || page.Messages[1].Source != FromNotice || page.Messages[1].ID != "amsg-1" {
		t.Fatalf("the conversation reads as %+v, want the prompt and the child agent's notice", page.Messages)
	}
	if text := page.Messages[1].Text; !strings.Contains(text, "Payload:\nNo findings.") {
		t.Fatalf("the notice reads %q", text)
	}
}

// assertMessages compares the messages a read returned with what is wanted,
// ignoring the id of a message Codex gave none of its own.
func assertMessages(t *testing.T, got, want []Message) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("the conversation holds %d messages, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if want[i].ID == "@" {
			got[i].ID = "@"
		}
		if got[i].Seq != want[i].Seq || got[i].Kind != want[i].Kind || !got[i].At.Equal(want[i].At) {
			t.Fatalf("message %d reads as %+v, want %+v", i, got[i], want[i])
		}
		if got[i].ID != want[i].ID || got[i].Source != want[i].Source || got[i].Text != want[i].Text || got[i].Model != want[i].Model {
			t.Fatalf("message %d reads as %+v, want %+v", i, got[i], want[i])
		}
		if len(got[i].Blocks) != len(want[i].Blocks) {
			t.Fatalf("message %d holds %+v, want %+v", i, got[i].Blocks, want[i].Blocks)
		}
		for j := range want[i].Blocks {
			if !reflect.DeepEqual(got[i].Blocks[j], want[i].Blocks[j]) {
				t.Fatalf("message %d's block %d reads as %+v, want %+v", i, j, got[i].Blocks[j], want[i].Blocks[j])
			}
		}
	}
}
