package fleettree

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The entries below are made up in the shape Claude Code 2.1.308 writes a
// conversation in: one entry per line, each block of an answer an entry of
// its own under the answer's id.

const workDir = `C:\work\repo`

func stamped(entry object) object {
	entry["timestamp"] = at.Format(time.RFC3339Nano)
	entry["cwd"] = workDir
	return entry
}

// prompted is a prompt as Claude Code records one it took.
func prompted(uuid string, content any) object {
	return stamped(object{"type": "user", "uuid": uuid, "message": object{"role": "user", "content": content}})
}

// answer is one block of the answer id.
func answer(uuid, id string, block object) object {
	return stamped(object{"type": "assistant", "uuid": uuid, "message": object{"id": id, "model": "claude-opus-5-5", "role": "assistant", "content": []object{block}}})
}

// answered is a call's result and what Claude Code recorded of it, with
// fields of the entry.
func answered(uuid, call string, content any, recorded any, fields object) object {
	entry := stamped(object{"type": "user", "uuid": uuid, "message": object{"role": "user", "content": []object{{"type": "tool_result", "tool_use_id": call, "content": content}}}, "toolUseResult": recorded})
	for key, value := range fields {
		entry[key] = value
	}
	return entry
}

func failedResult(uuid, call, content string) object {
	entry := answered(uuid, call, content, "Error: "+content, nil)
	entry["message"].(object)["content"].([]object)[0]["is_error"] = true
	return entry
}

func textBlock(text string) object { return object{"type": "text", "text": text} }

func thinkingBlock(text string) object {
	return object{"type": "thinking", "thinking": text, "signature": "c2lnbmF0dXJl"}
}

func useBlock(id, name string, input object) object {
	return object{"type": "tool_use", "id": id, "name": name, "input": input}
}

func imageBlock(data []byte) object {
	return object{"type": "image", "source": object{"type": "base64", "media_type": "image/png", "data": base64.StdEncoding.EncodeToString(data)}}
}

func system(uuid, subtype string, fields object) object {
	entry := stamped(object{"type": "system", "subtype": subtype, "uuid": uuid})
	for key, value := range fields {
		entry[key] = value
	}
	return entry
}

func attachment(uuid string, fields object) object {
	return stamped(object{"type": "attachment", "uuid": uuid, "attachment": fields})
}

// conversation writes entries as a Claude Code conversation under home and
// returns its path.
func conversation(t *testing.T, home string, entries ...any) string {
	t.Helper()
	path := filepath.Join(home, ".claude", "projects", "C--work-repo", "session-1.jsonl")
	writeLines(t, path, at, entries...)
	return path
}

func prompt(seq int, id, source, text string) Message {
	return Message{ID: id, Seq: seq, Kind: MessagePrompt, At: at, Source: source, Text: text}
}

func reply(seq int, id string, blocks ...Block) Message {
	return Message{ID: id, Seq: seq, Kind: MessageReply, At: at, Model: "claude-opus-5-5", Blocks: blocks}
}

func callBlock(id, tool, subject, state, result string) Block {
	return Block{Kind: BlockCall, CallID: id, Tool: tool, Subject: subject, State: state, Result: result}
}

// Each kind of entry a Claude Code conversation holds reads as the message
// the message view shows: prompts with where they came from, answers
// grouped by their id, calls with their outcome and what came back, edits,
// sub-agents, commands, compactions and interrupts, and nothing of what
// Claude Code records for its model alone.
func TestAnAnswerSaysWhetherItCameFromTheBoardOrItsPage(t *testing.T) {
	for _, test := range []struct{ text, source string }{
		{"The Overlord answered your question on the board. Answer: yes", FromAnswer},
		{"The Overlord answered your question on its page. Answer: yes", FromPage},
		{"CFO: answered on your review page https://example/page: Build it as shown", FromPage},
	} {
		source, _ := promptSource(test.text)
		if source != test.source {
			t.Errorf("promptSource(%q) = %q, want %q", test.text, source, test.source)
		}
	}
}

func TestMessagesReadEachKindOfEntry(t *testing.T) {
	session := func(home string) string {
		return filepath.Join(home, ".claude", "projects", "C--work-repo", "session-1")
	}
	for _, tc := range []struct {
		name    string
		entries func(t *testing.T, home string) []any
		want    []Message
	}{
		{
			name: "an answer's blocks group under its id, a result written between them",
			entries: func(*testing.T, string) []any {
				return []any{
					prompted("u1", "Read the readme"),
					answer("a1", "msg_1", thinkingBlock("")),
					answer("a2", "msg_1", thinkingBlock("The readme says what the repo is.")),
					answer("a3", "msg_1", useBlock("toolu_1", "Read", object{"file_path": workDir + `\README.md`})),
					answered("u2", "toolu_1", "1\t# Repo", object{"type": "text"}, nil),
					answer("a4", "msg_1", textBlock("It names the repo.")),
					answer("a5", "msg_2", textBlock("Done.")),
				}
			},
			want: []Message{
				prompt(0, "u1", FromOverlord, "Read the readme"),
				reply(1, "msg_1", Block{Kind: BlockThinking, Text: "The readme says what the repo is."}, callBlock("toolu_1", "Read", "README.md", CallDone, "1\t# Repo"), Block{Kind: BlockText, Text: "It names the repo."}),
				reply(2, "msg_2", Block{Kind: BlockText, Text: "Done."}),
			},
		},
		{
			name: "prompts name where they came from",
			entries: func(*testing.T, string) []any {
				return []any{
					prompted("u1", `Read C:\work\state\instruction.md and follow it exactly: it is your instruction from the CFO.`),
					prompted("u2", "CFO: merge origin/main first"),
					prompted("u3", "CFO: The Overlord answered your question on the board. Question: Which first? Answer: Fix it next"),
					prompted("u4", "User answer to CFO question q-1. Question: Ship it? Answer: Yes"),
					prompted("u5", "Overlord: keep the tests\n<system-reminder>Claude Code's own note</system-reminder>"),
				}
			},
			want: []Message{
				prompt(0, "u1", FromCFO, `Read C:\work\state\instruction.md and follow it exactly: it is your instruction from the CFO.`),
				prompt(1, "u2", FromCFO, "merge origin/main first"),
				prompt(2, "u3", FromAnswer, "The Overlord answered your question on the board. Question: Which first? Answer: Fix it next"),
				prompt(3, "u4", FromAnswer, "User answer to CFO question q-1. Question: Ship it? Answer: Yes"),
				prompt(4, "u5", FromOverlord, "Overlord: keep the tests"),
			},
		},
		{
			name: "a queued prompt is marked queued, taken mid-turn or as the next turn",
			entries: func(*testing.T, string) []any {
				queuedPrompt := prompted("u2", "check the phone too")
				queuedPrompt["promptSource"] = "queued"
				return []any{
					attachment("q1", object{"type": "queued_command", "commandMode": "prompt", "prompt": `CFO: also run the specs <pasted_content id="07">now</pasted_content id="07">`}),
					queuedPrompt,
					attachment("q2", object{"type": "queued_command", "commandMode": "prompt", "prompt": []object{textBlock("look at this"), imageBlock([]byte("png"))}}),
				}
			},
			want: []Message{
				{ID: "q1", Seq: 0, Kind: MessagePrompt, At: at, Source: FromCFO, IsQueued: true, Text: "also run the specs now"},
				{ID: "u2", Seq: 1, Kind: MessagePrompt, At: at, Source: FromOverlord, IsQueued: true, Text: "check the phone too"},
				{ID: "q2", Seq: 2, Kind: MessagePrompt, At: at, Source: FromOverlord, IsQueued: true, Text: "look at this"},
			},
		},
		{
			name: "a task notification reads as its summary",
			entries: func(*testing.T, string) []any {
				return []any{
					prompted("u1", notification("b1", "toolu_9", "completed", `Background command "Run the suite" completed (exit code 0)`, "")+"\n<system-reminder>Claude Code's own note</system-reminder>"),
					attachment("q1", object{"type": "queued_command", "commandMode": "task-notification", "prompt": notification("m1", "", "", `Monitor "CI" reported`, "build green")}),
				}
			},
			want: []Message{
				prompt(0, "u1", FromNotice, `Background command "Run the suite" completed (exit code 0)`),
				{ID: "q1", Seq: 1, Kind: MessagePrompt, At: at, Source: FromNotice, IsQueued: true, Text: "Monitor \"CI\" reported\nbuild green"},
			},
		},
		{
			name: "a slash command reads as typed, with its output",
			entries: func(*testing.T, string) []any {
				caveat := prompted("m1", "<local-command-caveat>Caveat: the messages below were generated by the user while running local commands.</local-command-caveat>")
				caveat["isMeta"] = true
				skill := prompted("m2", []object{textBlock("Review the current changes thoroughly.")})
				skill["isMeta"] = true
				return []any{
					caveat,
					prompted("u1", "<command-name>/model</command-name>\n            <command-message>model</command-message>\n            <command-args>opus</command-args>"),
					prompted("u2", "<local-command-stdout>Set model to \x1b[1mopus\x1b[22m</local-command-stdout>"),
					prompted("u3", "<command-message>review is running…</command-message>\n<command-name>/review</command-name>"),
					skill,
				}
			},
			want: []Message{
				{ID: "u1", Seq: 0, Kind: MessagePrompt, At: at, Source: FromSlash, Text: "/model opus", Output: "Set model to opus"},
				prompt(1, "u3", FromSlash, "/review"),
			},
		},
		{
			name: "a ! command reads as typed, with what it printed",
			entries: func(*testing.T, string) []any {
				return []any{
					prompted("u1", "<bash-input>git status</bash-input>"),
					prompted("u2", "<bash-stdout>On branch main</bash-stdout><bash-stderr>warning: stale ref</bash-stderr>"),
				}
			},
			want: []Message{
				{ID: "u1", Seq: 0, Kind: MessagePrompt, At: at, Source: FromShell, Text: "git status", Output: "On branch main", Errors: "warning: stale ref"},
			},
		},
		{
			name: "a compaction carries its summary",
			entries: func(*testing.T, string) []any {
				summary := prompted("u2", "This session is being continued from a previous conversation.")
				summary["isCompactSummary"] = true
				summary["isVisibleInTranscriptOnly"] = true
				return []any{
					system("s1", "compact_boundary", object{"content": "Conversation compacted", "compactMetadata": object{"trigger": "manual", "preTokens": 180000}}),
					attachment("x1", object{"type": "date"}),
					summary,
				}
			},
			want: []Message{
				{ID: "s1", Seq: 0, Kind: MessageCompaction, At: at, Trigger: "manual", Text: "This session is being continued from a previous conversation."},
			},
		},
		{
			name: "an interrupt reads as his stop, and the call it cut short as interrupted",
			entries: func(*testing.T, string) []any {
				return []any{
					answer("a1", "msg_1", useBlock("toolu_1", "Bash", object{"command": "go test ./...\ngo vet ./...", "description": "Run the tests"})),
					answered("u1", "toolu_1", "The user doesn't want to proceed with this tool use.", object{"interrupted": true, "stdout": "", "stderr": ""}, object{"toolDenialKind": "interrupted"}),
					prompted("u2", []object{textBlock("[Request interrupted by user for tool use]")}),
					prompted("u3", []object{textBlock("[Request interrupted by user]")}),
				}
			},
			want: []Message{
				reply(0, "msg_1", callBlock("toolu_1", "Bash", "go test ./...", CallInterrupted, "The user doesn't want to proceed with this tool use.")),
				{ID: "u2", Seq: 1, Kind: MessageInterrupt, At: at, Text: "Request interrupted by user for tool use"},
				{ID: "u3", Seq: 2, Kind: MessageInterrupt, At: at, Text: "Request interrupted by user"},
			},
		},
		{
			name: "a spilled result reads from the file Claude Code saved it to",
			entries: func(t *testing.T, home string) []any {
				saved := filepath.Join(session(home), "tool-results", "toolu_1.txt")
				writeFile(t, saved, "ok  internal/monitor\nok  internal/fleettree\n", at)
				return []any{
					answer("a1", "msg_1", useBlock("toolu_1", "Bash", object{"command": "go test ./..."})),
					answered("u1", "toolu_1", "<persisted-output>\nOutput too large (40.0KB). Full output saved to: "+saved+"\n\nPreview (first 2KB):\nok  internal/monitor\n</persisted-output>", object{"stdout": "", "persistedOutputPath": saved, "persistedOutputSize": 40960}, nil),
				}
			},
			want: []Message{
				reply(0, "msg_1", Block{Kind: BlockCall, CallID: "toolu_1", Tool: "Bash", Subject: "go test ./...", State: CallDone, Result: "ok  internal/monitor\nok  internal/fleettree", Spilled: 44}),
			},
		},
		{
			name: "a spill saved outside its conversation's folder keeps its preview",
			entries: func(t *testing.T, home string) []any {
				elsewhere := filepath.Join(home, "elsewhere", "toolu_1.txt")
				writeFile(t, elsewhere, "not this conversation's", at)
				return []any{
					answer("a1", "msg_1", useBlock("toolu_1", "mcp__docs__fetch", object{"url": "https://example.com/spec"})),
					answered("u1", "toolu_1", "<persisted-output>\nOutput too large (40.0KB). Full output saved to: "+elsewhere+"\n\nPreview (first 2KB):\n# Spec\n</persisted-output>", nil, nil),
				}
			},
			want: []Message{
				reply(0, "msg_1", Block{Kind: BlockCall, CallID: "toolu_1", Tool: "mcp__docs__fetch", Subject: "https://example.com/spec", State: CallDone, Result: "# Spec", Spilled: 40960}),
			},
		},
		{
			name: "calls end done, failed, denied or still running",
			entries: func(*testing.T, string) []any {
				return []any{
					answer("a1", "msg_1", useBlock("toolu_1", "Bash", object{"command": "go build ./..."})),
					answer("a2", "msg_1", useBlock("toolu_2", "Bash", object{"command": "rm -rf build"})),
					answer("a3", "msg_1", useBlock("toolu_3", "Grep", object{"pattern": "readOn"})),
					answer("a4", "msg_1", useBlock("toolu_4", "Bash", object{"command": "go test ./..."})),
					failedResult("u1", "toolu_1", "<tool_use_error>exit status 1</tool_use_error>"),
					answered("u2", "toolu_2", "Permission for this action was denied.", "Error: Permission denied", object{"permissionDecision": object{"decision": "reject", "source": "config"}, "toolDenialKind": "automode-blocked"}),
					answered("u3", "toolu_3", "Found 2 files", object{"numFiles": 2}, nil),
				}
			},
			want: []Message{
				reply(0, "msg_1",
					callBlock("toolu_1", "Bash", "go build ./...", CallFailed, "exit status 1"),
					callBlock("toolu_2", "Bash", "rm -rf build", CallDenied, "Permission for this action was denied."),
					callBlock("toolu_3", "Grep", "readOn", CallDone, "Found 2 files"),
					callBlock("toolu_4", "Bash", "go test ./...", CallRunning, "")),
			},
		},
		{
			name: "an edit carries its counts, and a sub-agent call its node",
			entries: func(*testing.T, string) []any {
				return []any{
					answer("a1", "msg_1", useBlock("toolu_1", "Edit", object{"file_path": workDir + `\main.go`, "old_string": "a", "new_string": "b"})),
					answer("a2", "msg_1", useBlock("toolu_2", "Write", object{"file_path": workDir + `\new.go`, "content": "package x\n\nfunc X() {}\n"})),
					answer("a3", "msg_1", useBlock("toolu_3", "Agent", object{"description": "Map the monitor", "subagent_type": "Explore", "prompt": "map it"})),
					answered("u1", "toolu_1", "The file has been updated.", object{"filePath": workDir + `\main.go`, "structuredPatch": []object{{"oldStart": 1, "oldLines": 3, "newStart": 1, "newLines": 4, "lines": []string{" package main", "-var a = 1", "+var b = 2", "+var c = 3"}}}}, nil),
					answered("u2", "toolu_2", "File created successfully.", object{"type": "create", "filePath": workDir + `\new.go`, "content": "package x\n\nfunc X() {}\n", "structuredPatch": []object{}}, nil),
					answered("u3", "toolu_3", "Async agent launched successfully.", object{"status": "async_launched", "agentId": "a1"}, nil),
				}
			},
			want: []Message{
				reply(0, "msg_1",
					Block{Kind: BlockCall, CallID: "toolu_1", Tool: "Edit", Subject: "main.go", State: CallDone, Result: "The file has been updated.", Edit: Edit{Added: 2, Removed: 1, Diff: []string{" package main", "-var a = 1", "+var b = 2", "+var c = 3"}}},
					Block{Kind: BlockCall, CallID: "toolu_2", Tool: "Write", Subject: "new.go", State: CallDone, Result: "File created successfully.", Edit: Edit{Added: 3, Diff: []string{"+package x", "+", "+func X() {}"}}},
					Block{Kind: BlockCall, CallID: "toolu_3", Tool: "Agent", Subject: "Map the monitor", State: CallDone, Result: "Async agent launched successfully.", Agent: AgentLink{Node: "subagent:toolu_3", Type: "Explore"}}),
			},
		},
		{
			name: "pictures in a prompt and in a result",
			entries: func(*testing.T, string) []any {
				return []any{
					prompted("u1", []object{imageBlock([]byte("a screenshot")), textBlock("Why is this red?")}),
					answer("a1", "msg_1", useBlock("toolu_1", "Read", object{"file_path": workDir + `\shot.png`})),
					answered("u2", "toolu_1", []object{imageBlock([]byte("the file"))}, object{"type": "image"}, nil),
				}
			},
			want: []Message{
				{ID: "u1", Seq: 0, Kind: MessagePrompt, At: at, Source: FromOverlord, Text: "Why is this red?", Pictures: []Picture{{ID: "1", MediaType: "image/png", Bytes: 12}}},
				reply(1, "msg_1", Block{Kind: BlockCall, CallID: "toolu_1", Tool: "Read", Subject: "shot.png", State: CallDone, Pictures: []Picture{{ID: "2", MediaType: "image/png", Bytes: 8}}}),
			},
		},
		{
			name: "what Claude Code records for its model alone shows nothing",
			entries: func(*testing.T, string) []any {
				sidechain := answer("a9", "msg_9", textBlock("a sub-agent's own words"))
				sidechain["isSidechain"] = true
				return []any{
					`{"type":"api-request-blob","sessionId":"session-1","hash":"h","message":{"content":[{"type":"text","text":"a copy of a prompt"}]}}`,
					attachment("x1", object{"type": "hook_success", "hookName": "PostToolUse"}),
					system("s1", "turn_duration", object{"durationMs": 1200}),
					sidechain,
					answer("a1", "msg_1", thinkingBlock("")),
					"not an entry",
				}
			},
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			path := conversation(t, home, tc.entries(t, home)...)
			reader := Reader{Home: home}

			// Act
			page, _, err := reader.MessagePage(path, -1)

			// Assert
			want := tc.want
			if want == nil {
				want = []Message{}
			}
			if err != nil || !reflect.DeepEqual(page.Messages, want) || page.More {
				t.Errorf("MessagePage = %+v, more %t, %v\nwant %+v", page.Messages, page.More, err, want)
			}
		})
	}
}

// Claude Code writes an entry whole and then its newline, so a read that
// lands between them leaves the entry for the next read, which reads on from
// where the last stopped.
func TestMessagesLeaveAHalfWrittenLastLineForTheNextRead(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home, prompted("u1", "first"))
	whole := `{"type":"user","uuid":"u2","timestamp":"` + at.Format(time.RFC3339Nano) + `","message":{"role":"user","content":"second"}}`
	appendText(t, path, whole[:40])
	reader := Reader{Home: home}
	page, cursor, err := reader.MessagePage(path, -1)
	if err != nil || !reflect.DeepEqual(page.Messages, []Message{prompt(0, "u1", FromOverlord, "first")}) {
		t.Fatalf("MessagePage with a half-written line = %+v, %v; want the first prompt alone", page.Messages, err)
	}

	// Act
	appendText(t, path, whole[40:]+"\n")
	messages, _, isAnew, err := reader.MessagesSince(path, cursor)

	// Assert
	if err != nil || isAnew || !reflect.DeepEqual(messages, []Message{prompt(1, "u2", FromOverlord, "second")}) {
		t.Errorf("MessagesSince = %+v, anew %t, %v; want the second prompt once written whole", messages, isAnew, err)
	}
}

func appendText(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// A view follows a conversation by what changed since its last read: a new
// message, and an earlier reply whose call's result came back, each once.
func TestMessagesSinceAreTheMessagesThatBeganOrChanged(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home,
		prompted("u1", "run the suite"),
		answer("a1", "msg_1", useBlock("toolu_1", "Bash", object{"command": "go test ./..."})),
		answer("a2", "msg_2", textBlock("Running.")))
	reader := Reader{Home: home}
	_, cursor, err := reader.MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}
	appendLines(t, path, answered("u2", "toolu_1", "ok", object{"stdout": "ok"}, nil), prompted("u3", "thanks"))

	// Act
	messages, next, isAnew, err := reader.MessagesSince(path, cursor)
	again, _, _, againErr := reader.MessagesSince(path, next)

	// Assert
	want := []Message{
		reply(1, "msg_1", callBlock("toolu_1", "Bash", "go test ./...", CallDone, "ok")),
		prompt(3, "u3", FromOverlord, "thanks"),
	}
	if err != nil || isAnew || !reflect.DeepEqual(messages, want) {
		t.Errorf("MessagesSince = %+v, anew %t, %v; want %+v", messages, isAnew, err, want)
	}
	if againErr != nil || len(again) != 0 {
		t.Errorf("MessagesSince with nothing new = %+v, %v; want none", again, againErr)
	}
}

// A conversation that shrank was replaced, so a view's messages of it no
// longer stand and it takes a page again.
func TestMessagesOfAReplacedConversationAreReadAnew(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home, prompted("u1", "a long first conversation"), prompted("u2", "with more"))
	reader := Reader{Home: home}
	_, cursor, err := reader.MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}
	conversation(t, home, prompted("v1", "new"))

	// Act
	messages, _, isAnew, err := reader.MessagesSince(path, cursor)
	page, _, pageErr := reader.MessagePage(path, -1)

	// Assert
	if err != nil || !isAnew || messages != nil {
		t.Errorf("MessagesSince after a replace = %+v, anew %t, %v; want it read anew", messages, isAnew, err)
	}
	if pageErr != nil || !reflect.DeepEqual(page.Messages, []Message{prompt(0, "v1", FromOverlord, "new")}) {
		t.Errorf("MessagePage after a replace = %+v, %v; want the new conversation", page.Messages, pageErr)
	}
}

// Older messages come a page at a time, each page the newest before the
// first the view holds.
func TestMessagePagesReachBackAPageAtATime(t *testing.T) {
	// Arrange
	home := t.TempDir()
	var entries []any
	for i := range 120 {
		entries = append(entries, prompted("u"+strconv.Itoa(i), "prompt "+strconv.Itoa(i)))
	}
	path := conversation(t, home, entries...)
	reader := Reader{Home: home}

	for _, tc := range []struct {
		before, first, last int
		isMore              bool
	}{
		{-1, 70, 119, true},
		{70, 20, 69, true},
		{20, 0, 19, false},
		{500, 70, 119, true},
		{0, 0, -1, false},
	} {
		t.Run("before "+strconv.Itoa(tc.before), func(t *testing.T) {
			// Act
			page, _, err := reader.MessagePage(path, tc.before)

			// Assert
			if err != nil || len(page.Messages) != tc.last-tc.first+1 || page.More != tc.isMore {
				t.Fatalf("MessagePage(%d) = %d messages, more %t, %v; want %d to %d, more %t", tc.before, len(page.Messages), page.More, err, tc.first, tc.last, tc.isMore)
			}
			if len(page.Messages) > 0 && (page.Messages[0].Seq != tc.first || page.Messages[len(page.Messages)-1].Text != "prompt "+strconv.Itoa(tc.last)) {
				t.Errorf("MessagePage(%d) runs %+v to %+v; want %d to %d", tc.before, page.Messages[0], page.Messages[len(page.Messages)-1], tc.first, tc.last)
			}
		})
	}
}

// A picture is read back from its entry by the id a read gave it, and only
// a picture a read found.
func TestMessagePictureIsReadBackByItsID(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home,
		prompted("u1", []object{imageBlock([]byte("a screenshot")), textBlock("Why is this red?")}),
		answer("a1", "msg_1", useBlock("toolu_1", "Read", object{"file_path": workDir + `\shot.png`})),
		answered("u2", "toolu_1", []object{imageBlock([]byte("the file"))}, object{"type": "image"}, nil))
	reader := Reader{Home: home}
	if _, _, err := reader.MessagePage(path, -1); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		id   string
		want string
	}{
		{"1", "a screenshot"},
		{"2", "the file"},
		{"3", ""},
		{"../1", ""},
	} {
		t.Run(tc.id, func(t *testing.T) {
			// Act
			mediaType, data, err := reader.MessagePicture(path, tc.id)

			// Assert
			if tc.want == "" {
				if !errors.Is(err, ErrNoRecord) {
					t.Errorf("MessagePicture(%q) = %q, %v; want ErrNoRecord", tc.id, data, err)
				}
				return
			}
			if err != nil || mediaType != "image/png" || string(data) != tc.want {
				t.Errorf("MessagePicture(%q) = %s %q, %v; want image/png %q", tc.id, mediaType, data, err, tc.want)
			}
		})
	}
}

// A conversation longer than one read's chunk is read across the chunks'
// edges, each entry whole and at its own offset, so a picture past them is
// read back from its own entry.
func TestMessagesReadAcrossTheChunksOfALongConversation(t *testing.T) {
	// Arrange
	home := t.TempDir()
	long := strings.Repeat("x", claudeReadChunk/3)
	var entries []any
	for i := range 4 {
		entries = append(entries, prompted("u"+strconv.Itoa(i), long))
	}
	entries = append(entries, prompted("u4", []object{imageBlock([]byte("past the chunk")), textBlock("and this")}))
	path := conversation(t, home, entries...)
	reader := Reader{Home: home}

	// Act
	page, _, err := reader.MessagePage(path, -1)
	mediaType, data, pictureErr := reader.MessagePicture(path, "1")

	// Assert
	if err != nil || len(page.Messages) != 5 || page.Messages[4].ID != "u4" || page.Messages[4].Text != "and this" || len([]rune(page.Messages[0].Text)) != messageTextLimit {
		t.Fatalf("MessagePage = %d messages, %v; want five, the last with its picture", len(page.Messages), err)
	}
	if pictureErr != nil || mediaType != "image/png" || string(data) != "past the chunk" {
		t.Errorf("MessagePicture = %s %q, %v; want the picture past the chunk", mediaType, data, pictureErr)
	}
}

// A conversation no view asked for in five minutes is let go, by the next
// ask for any conversation or by the tree's own pass, and a view that asks
// again reads it anew.
func TestAConversationNoViewAsksForIsLetGo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		letsGo  func(reader *Reader, other string)
		isAnew  bool
		elapsed time.Duration
	}{
		{"asked for within five minutes", func(reader *Reader, other string) { reader.Forget(nil) }, false, 4 * time.Minute},
		{"by the tree's own pass", func(reader *Reader, other string) { reader.Forget(nil) }, true, 6 * time.Minute},
		{"by an ask for another", func(reader *Reader, other string) { _, _, _ = reader.MessagePage(other, -1) }, true, 6 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			path := conversation(t, home, prompted("u1", []object{imageBlock([]byte("a screenshot")), textBlock("first")}))
			other := filepath.Join(home, "other.jsonl")
			writeLines(t, other, at, prompted("v1", "elsewhere"))
			now := at
			reader := Reader{Home: home, Now: func() time.Time { return now }}
			_, cursor, err := reader.MessagePage(path, -1)
			if err != nil {
				t.Fatal(err)
			}

			// Act
			now = now.Add(tc.elapsed)
			tc.letsGo(&reader, other)
			_, _, pictureErr := reader.MessagePicture(path, "1")
			_, _, isAnew, err := reader.MessagesSince(path, cursor)

			// Assert
			if errors.Is(pictureErr, ErrNoRecord) != tc.isAnew || err != nil || isAnew != tc.isAnew {
				t.Errorf("after %s: picture %v, MessagesSince anew %t, %v; want let go %t", tc.elapsed, pictureErr, isAnew, err, tc.isAnew)
			}
		})
	}
}
