package fleettree

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/routing"
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

// entryOffsets is where each entry of the conversation at path begins.
func entryOffsets(t *testing.T, path string) []int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var offsets []int
	for offset := 0; offset < len(data); {
		offsets = append(offsets, offset)
		end := strings.IndexByte(string(data[offset:]), '\n')
		if end < 0 {
			break
		}
		offset += end + 1
	}
	return offsets
}

// numbered is messages as a test names them: each seq, and the offset that
// begins each picture's id, as the number of the entry of the conversation at
// path it is the offset of.
func numbered(t *testing.T, path string, messages []Message) []Message {
	t.Helper()
	numbers := map[int]int{}
	for number, offset := range entryOffsets(t, path) {
		numbers[offset] = number
	}
	number := func(offset int) int {
		t.Helper()
		number, isEntry := numbers[offset]
		if !isEntry {
			t.Fatalf("%d begins no entry", offset)
		}
		return number
	}
	renamed := func(pictures []Picture) []Picture {
		var named []Picture
		for _, picture := range pictures {
			offset, rest, _ := strings.Cut(picture.ID, ".")
			at, err := strconv.Atoi(offset)
			if err != nil {
				t.Fatalf("picture id %q begins with no offset", picture.ID)
			}
			picture.ID = strconv.Itoa(number(at)) + "." + rest
			named = append(named, picture)
		}
		return named
	}
	named := make([]Message, 0, len(messages))
	for _, message := range messages {
		message.Seq = number(message.Seq)
		message.Pictures = renamed(message.Pictures)
		message.Blocks = slices.Clone(message.Blocks)
		for i := range message.Blocks {
			message.Blocks[i].Pictures = renamed(message.Blocks[i].Pictures)
		}
		named = append(named, message)
	}
	return named
}

// beyondTheTail is what Claude Code keeps for its model alone, past a tail's
// reach.
func beyondTheTail() []any {
	kept := `{"type":"api-request-blob","sessionId":"session-1","message":{"content":"` + strings.Repeat("x", 64<<10) + `"}}`
	var entries []any
	for range messageTailReach/len(kept) + 16 {
		entries = append(entries, kept)
	}
	return entries
}

// longConversation is a conversation longer than a tail's reach: a first
// prompt with a picture and an answer whose call is settled, and whose last
// block is written, past the reach, then sixty prompts, the last with a
// picture, and the answer to it.
func longConversation() []any {
	first := answer("a1", "msg_first", useBlock("toolu_1", "Bash", object{"command": "go test ./..."}))
	first["message"].(object)["usage"] = object{"input_tokens": 1000, "output_tokens": 10}
	entries := append([]any{prompted("u-first", []object{imageBlock([]byte("the first picture")), textBlock("the first words")}), first}, beyondTheTail()...)
	entries = append(entries, answered("u-result", "toolu_1", "ok", object{"stdout": "ok"}, nil), answer("a1-end", "msg_first", textBlock("The suite passed.")))
	for i := range 59 {
		entries = append(entries, prompted("u"+strconv.Itoa(i), "prompt "+strconv.Itoa(i)))
	}
	last := answer("a2", "msg_last", textBlock("the last words"))
	last["message"].(object)["usage"] = object{"input_tokens": 2000, "output_tokens": 20}
	return append(entries, prompted("u-last", []object{imageBlock([]byte("the last picture")), textBlock("prompt 59")}), last)
}

// A long conversation's newest page comes from a read of its tail, and the
// read of the whole conversation beside it changes nothing a view holds: each
// message keeps its seq, id and pictures, a view following the tail's page is
// sent nothing again, an answer begun before the tail shows only whole, and
// the older pages and what the session spent come from the whole.
func TestALongConversationReadsTheSameOnceReadWhole(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home, longConversation()...)
	reader := Reader{Home: home}
	newest, cursor, err := reader.MessagePage(path, -1)
	if err != nil || len(newest.Messages) != MessagesPerPage || !newest.More {
		t.Fatalf("MessagePage = %d messages, more %t, %v; want a full page and more", len(newest.Messages), newest.More, err)
	}

	// Act
	older, _, olderErr := reader.MessagePage(path, newest.Messages[0].Seq)
	changed, _, isAnew, sinceErr := reader.MessagesSince(path, cursor)
	again, _, againErr := reader.MessagePage(path, -1)

	// Assert
	if newest.Usage != nil {
		t.Errorf("the tail's page carries usage %+v; want none until the whole conversation is read", newest.Usage)
	}
	if olderErr != nil || len(older.Messages) != 13 || older.More || older.Messages[0].Text != "the first words" || older.Messages[12].Text != "prompt 10" || newest.Messages[0].Text != "prompt 11" {
		t.Fatalf("the page before the newest = %d messages, more %t, %v; want the first words to prompt 10 and no more", len(older.Messages), older.More, olderErr)
	}
	if first := older.Messages[1]; len(first.Blocks) != 2 || first.Blocks[0].State != CallDone || first.Blocks[0].Result != "ok" || first.Blocks[1].Text != "The suite passed." {
		t.Errorf("the first answer = %+v; want its call done with the result read past the tail's reach, and its last block", first.Blocks)
	}
	held := append(slices.Clone(older.Messages), newest.Messages...)
	for i := 1; i < len(held); i++ {
		if held[i].Seq <= held[i-1].Seq {
			t.Errorf("%s at seq %d follows %s at seq %d; want the seqs of the pages rising", held[i].ID, held[i].Seq, held[i-1].ID, held[i-1].Seq)
			break
		}
	}
	if sinceErr != nil || isAnew || len(changed) != 0 {
		t.Errorf("MessagesSince the tail's page = %d changed, anew %t, %v; want nothing sent again", len(changed), isAnew, sinceErr)
	}
	if againErr != nil || !reflect.DeepEqual(again.Messages, newest.Messages) {
		t.Errorf("the newest page once read whole = %+v, %v\nwant the tail's page %+v", again.Messages, againErr, newest.Messages)
	}
	if again.Usage == nil || again.Usage.InputTokens != 3000 || again.Usage.OutputTokens != 30 {
		t.Errorf("usage once read whole = %+v; want both answers counted", again.Usage)
	}
	for _, tc := range []struct {
		message Message
		want    string
	}{
		{older.Messages[0], "the first picture"},
		{newest.Messages[48], "the last picture"},
	} {
		if len(tc.message.Pictures) != 1 {
			t.Fatalf("message %s holds pictures %+v; want one", tc.message.ID, tc.message.Pictures)
		}
		if _, data, err := reader.MessagePicture(path, tc.message.Pictures[0].ID); err != nil || string(data) != tc.want {
			t.Errorf("MessagePicture(%s) = %q, %v; want %q", tc.message.Pictures[0].ID, data, err, tc.want)
		}
	}
}

// A message written while a long conversation is read whole comes to a view
// of its tail's page as a change, the same as one written later, whichever
// read finds it.
func TestAMessageWrittenWhileTheWholeIsReadComesAsAChange(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home, longConversation()...)
	reader := Reader{Home: home}
	newest, cursor, err := reader.MessagePage(path, -1)
	if err != nil || len(newest.Messages) == 0 {
		t.Fatalf("MessagePage = %d messages, %v; want the newest page", len(newest.Messages), err)
	}
	appendLines(t, path, prompted("u-new", "written meanwhile"))

	// Act
	_, _, olderErr := reader.MessagePage(path, newest.Messages[0].Seq)
	changed, _, isAnew, sinceErr := reader.MessagesSince(path, cursor)

	// Assert
	if olderErr != nil || sinceErr != nil || isAnew || len(changed) != 1 || changed[0].ID != "u-new" {
		t.Errorf("MessagesSince = %+v, anew %t, %v, %v; want the prompt written meanwhile alone", changed, isAnew, sinceErr, olderErr)
	}
}

// A long conversation whose tail begins no message, an answer begun before
// it and nothing after, shows nothing from the tail and has a view take the
// newest page anew once the whole is read.
func TestALongConversationWhoseTailHoldsNoMessageIsPagedAnew(t *testing.T) {
	// Arrange
	home := t.TempDir()
	entries := append([]any{prompted("u1", "map the repo"), answer("a1", "msg_1", textBlock("Reading it."))}, beyondTheTail()...)
	path := conversation(t, home, append(entries, answer("a2", "msg_1", textBlock("It holds two packages.")))...)
	reader := Reader{Home: home}
	tail, cursor, err := reader.MessagePage(path, -1)

	// Act
	_, _, beforeErr := reader.MessagePage(path, 0)
	_, _, isAnew, sinceErr := reader.MessagesSince(path, cursor)
	whole, _, wholeErr := reader.MessagePage(path, -1)

	// Assert
	if err != nil || len(tail.Messages) != 0 || !tail.More {
		t.Errorf("the tail's page = %+v, more %t, %v; want no message and more", tail.Messages, tail.More, err)
	}
	if beforeErr != nil || sinceErr != nil || !isAnew {
		t.Errorf("MessagesSince once read whole = anew %t, %v, %v; want the view to take the page anew", isAnew, sinceErr, beforeErr)
	}
	if wholeErr != nil || len(whole.Messages) != 2 || whole.More || len(whole.Messages[1].Blocks) != 2 {
		t.Errorf("the newest page once read whole = %+v, more %t, %v; want the prompt and its whole answer", whole.Messages, whole.More, wholeErr)
	}
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

// A message he typed to a goblin on the board reaches its terminal behind the
// CFO's stamp and the board's, and reads back as his own words, so the board
// can show it as his and as read. The CFO's own steer stays the CFO's, and a
// command he typed stays as he typed it.
func TestABoardMessageToAGoblinReadsAsHisOwnWords(t *testing.T) {
	for _, tc := range []struct{ name, text, source, words string }{
		{"his message from the board", routing.SteerPrefix + routing.BoardMessagePrefix + "check the deploy before you merge", FromOverlord, "check the deploy before you merge"},
		{"the CFO's steer", routing.SteerPrefix + "rebase on main", FromCFO, "rebase on main"},
		{"his command", "/compact", FromOverlord, "/compact"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			source, words := promptSource(tc.text)

			// Assert
			if source != tc.source || words != tc.words {
				t.Errorf("promptSource(%q) = %q, %q; want %q, %q", tc.text, source, words, tc.source, tc.words)
			}
		})
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
				reply(2, "msg_1", Block{Kind: BlockThinking, Text: "The readme says what the repo is."}, callBlock("toolu_1", "Read", "README.md", CallDone, "1\t# Repo"), Block{Kind: BlockText, Text: "It names the repo."}),
				reply(6, "msg_2", Block{Kind: BlockText, Text: "Done."}),
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
				{ID: "u1", Seq: 1, Kind: MessagePrompt, At: at, Source: FromSlash, Text: "/model opus", Output: "Set model to opus"},
				prompt(3, "u3", FromSlash, "/review"),
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
				{ID: "u2", Seq: 2, Kind: MessageInterrupt, At: at, Text: "Request interrupted by user for tool use"},
				{ID: "u3", Seq: 3, Kind: MessageInterrupt, At: at, Text: "Request interrupted by user"},
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
				{ID: "u1", Seq: 0, Kind: MessagePrompt, At: at, Source: FromOverlord, Text: "Why is this red?", Pictures: []Picture{{ID: "0.0", MediaType: "image/png", Bytes: 12}}},
				reply(1, "msg_1", Block{Kind: BlockCall, CallID: "toolu_1", Tool: "Read", Subject: "shot.png", State: CallDone, Pictures: []Picture{{ID: "2.0.0", MediaType: "image/png", Bytes: 8}}}),
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
			if got := numbered(t, path, page.Messages); err != nil || !reflect.DeepEqual(got, want) || page.More {
				t.Errorf("MessagePage = %+v, more %t, %v\nwant %+v", got, page.More, err, want)
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
	if err != nil || !reflect.DeepEqual(numbered(t, path, page.Messages), []Message{prompt(0, "u1", FromOverlord, "first")}) {
		t.Fatalf("MessagePage with a half-written line = %+v, %v; want the first prompt alone", page.Messages, err)
	}

	// Act
	appendText(t, path, whole[40:]+"\n")
	messages, _, isAnew, err := reader.MessagesSince(path, cursor)

	// Assert
	if err != nil || isAnew || !reflect.DeepEqual(numbered(t, path, messages), []Message{prompt(1, "u2", FromOverlord, "second")}) {
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
		prompt(4, "u3", FromOverlord, "thanks"),
	}
	if err != nil || isAnew || !reflect.DeepEqual(numbered(t, path, messages), want) {
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
	if pageErr != nil || !reflect.DeepEqual(numbered(t, path, page.Messages), []Message{prompt(0, "v1", FromOverlord, "new")}) {
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
	offsets := entryOffsets(t, path)
	reader := Reader{Home: home}

	// before is the entry a page is before, or a seq past every entry.
	for _, tc := range []struct {
		before, first, last int
		isMore              bool
	}{
		{-1, 70, 119, true},
		{70, 20, 69, true},
		{20, 0, 19, false},
		{1 << 30, 70, 119, true},
		{0, 0, -1, false},
	} {
		t.Run("before "+strconv.Itoa(tc.before), func(t *testing.T) {
			before := tc.before
			if before >= 0 && before < len(offsets) {
				before = offsets[before]
			}

			// Act
			page, _, err := reader.MessagePage(path, before)

			// Assert
			if err != nil || len(page.Messages) != tc.last-tc.first+1 || page.More != tc.isMore {
				t.Fatalf("MessagePage(%d) = %d messages, more %t, %v; want %d to %d, more %t", tc.before, len(page.Messages), page.More, err, tc.first, tc.last, tc.isMore)
			}
			if len(page.Messages) > 0 && (page.Messages[0].Seq != offsets[tc.first] || page.Messages[len(page.Messages)-1].Text != "prompt "+strconv.Itoa(tc.last)) {
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
	result := strconv.Itoa(entryOffsets(t, path)[2])

	for _, tc := range []struct {
		id   string
		want string
	}{
		{"0.0", "a screenshot"},
		{result + ".0.0", "the file"},
		{"0.1", ""},
		{"1", ""},
		{"../0.0", ""},
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
	mediaType, data, pictureErr := reader.MessagePicture(path, strconv.Itoa(entryOffsets(t, path)[4])+".0")

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
			_, _, pictureErr := reader.MessagePicture(path, "0.0")
			_, _, isAnew, err := reader.MessagesSince(path, cursor)

			// Assert
			if errors.Is(pictureErr, ErrNoRecord) != tc.isAnew || err != nil || isAnew != tc.isAnew {
				t.Errorf("after %s: picture %v, MessagesSince anew %t, %v; want let go %t", tc.elapsed, pictureErr, isAnew, err, tc.isAnew)
			}
		})
	}
}

// The wake line the supervisor types into a Codex or pi CFO's terminal is the
// fleet speaking, never the Overlord: it reads as the session's own notice,
// with its words kept, and everything else he types stays his own message,
// a line that begins with "cfo" included (the Overlord, 2026-10-26).
func TestTheFleetsTypedWakeIsANoticeNotHisMessage(t *testing.T) {
	// Arrange
	wake := WakePrefix + "2 queued wakes (ci cg-a, notify cg-b); run cfo drain, handle what it shows and ack it with the WAKE_ACK_REQUIRED command it prints"
	home := t.TempDir()
	path := conversation(t, home,
		prompted("u1", wake),
		prompted("u2", "cfo, take a look at the failing test"),
		prompted("u3", "cfo watcher wake-ups are noisy, quiet them down"),
	)
	reader := Reader{Home: home}

	// Act
	page, _, err := reader.MessagePage(path, -1)

	// Assert
	want := []Message{
		prompt(0, "u1", FromNotice, wake),
		prompt(1, "u2", FromOverlord, "cfo, take a look at the failing test"),
		prompt(2, "u3", FromOverlord, "cfo watcher wake-ups are noisy, quiet them down"),
	}
	if got := numbered(t, path, page.Messages); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("MessagePage = %+v, %v\nwant %+v", got, err, want)
	}
}

// A pi session reads the same fleet wake line as the session's notice, since
// the supervisor types it into a pi CFO's terminal too.
func TestAPiSessionReadsTheFleetsWakeAsANotice(t *testing.T) {
	// Arrange
	wake := WakePrefix + "1 queued wake (notify cg-a); run cfo drain, handle what it shows and ack it with the WAKE_ACK_REQUIRED command it prints"
	home := t.TempDir()
	path := piSession(t, home,
		piHeader(),
		piMessageEntry("u1", "", "user", object{"content": wake}),
		piMessageEntry("u2", "u1", "user", object{"content": "cfo, what changed?"}),
	)
	reader := Reader{Home: home}

	// Act
	page := readPi(t, &reader, path)

	// Assert
	want := []Message{
		prompt(0, "u1", FromNotice, wake),
		prompt(1, "u2", FromOverlord, "cfo, what changed?"),
	}
	if !reflect.DeepEqual(page.Messages, want) {
		t.Fatalf("messages = %+v, want %+v", page.Messages, want)
	}
}

func TestFleetProducerMessagesAreNotOverlordPrompts(t *testing.T) {
	for _, test := range []struct {
		text, source string
	}{
		{WakePrefix + "1 queued wake (notify task-1); run cfo drain", FromNotice},
		{"== SESSION LOCK ==\nSESSION LOCK: held by pid 123 on test\n== WAKE QUEUE ==\nWAKE QUEUE: empty", FromNotice},
		{"WAKE QUEUE: 1 pending\n  1 notify task-1 done\nWAKE_ACK_REQUIRED: cfo drain --ack-through 1", FromNotice},
		{"AFK MODE IS ON: the Supreme Overlord turned it on 2026-10-02 02:10 UTC from the board, and he is away until he turns it off.\nIt is complete autopilot: no Command Center prompt opens for him while it is on, and nothing is asked of him or held for him.\nWAKE QUEUE: empty", FromNotice},
		{routing.SteerPrefix + "Your run npm test ended with exit 0 after 1m35s; read its output and carry on from it.", FromNotice},
		{routing.SteerPrefix + "Your helper task-1 is done: its work is committed on its branch, ready. Merge it into your branch with: cfo helper merge parent", FromNotice},
		{routing.SteerPrefix + "Your helper task-1 asks: Which path? Answer it with: cfo send task-1 \"<answer>\"", FromNotice},
		{routing.SteerPrefix + "Your helper task-1 failed: test failure. Tell it what to do with: cfo send task-1 \"<what>\", or stop it with: cfo kill task-1 --reason \"<why>\"", FromNotice},
		{"cfo drain please", FromOverlord},
		{"cfo watcher wake-ups need fixing", FromOverlord},
		{"Your run was slow, please check it", FromOverlord},
		{"WAKE QUEUE: please show it", FromOverlord},
		{routing.SteerPrefix + "Your run needs a regression test", FromCFO},
	} {
		for _, harness := range []string{"claude", "pi", "codex"} {
			t.Run(harness+"/"+test.text, func(t *testing.T) {
				home := t.TempDir()
				var path string
				if harness == "pi" {
					path = piSession(t, home, piHeader(), piMessageEntry("u1", "", "user", object{"content": test.text}))
				} else if harness == "codex" {
					path = codexRollout(t, home, "notices", codexMeta("notices", workDir), codexUserSaid(test.text))
				} else {
					path = conversation(t, home, prompted("u1", test.text))
				}
				reader := Reader{Home: home}
				page, _, err := reader.MessagePage(path, -1)
				if err != nil || len(page.Messages) != 1 || page.Messages[0].Source != test.source {
					t.Fatalf("messages = %+v, %v; want source %s", page.Messages, err, test.source)
				}
			})
		}
	}
}

// A todo list a Claude Code call wrote reads as the call's plan: each
// step's words on one line and its state, and a state the board does not
// draw read as pending.
func TestATodoListACallWroteIsItsPlan(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home,
		prompted("u1", "Fix the swipe"),
		answer("a1", "msg_1", useBlock("toolu_1", "TodoWrite", object{"todos": []object{
			{"content": "Read the touch handlers", "status": "completed", "activeForm": "Reading the touch handlers"},
			{"content": "Hand the swipe to the history\nand keep Paste", "status": "in_progress", "activeForm": "Handing the swipe over"},
			{"content": "Run the phone spec", "status": "blocked", "activeForm": "Running the phone spec"},
		}})),
	)
	reader := Reader{Home: home}

	// Act
	page, _, err := reader.MessagePage(path, -1)

	// Assert
	want := []PlanStep{{"Read the touch handlers", PlanCompleted}, {"Hand the swipe to the history", PlanInProgress}, {"Run the phone spec", PlanPending}}
	if err != nil || len(page.Messages) != 2 || !reflect.DeepEqual(page.Messages[1].Blocks[0].Plan, want) {
		t.Fatalf("MessagePage = %+v, %v; want the call's plan %+v", page.Messages, err, want)
	}
}

// A todo list keeps its plan only once Claude Code wrote it: a TodoWrite
// that failed, was denied or was interrupted is its row with its outcome,
// as Claude Code stored no list.
func TestATodoListKeepsItsPlanOnlyOnceWritten(t *testing.T) {
	for _, test := range []struct {
		name    string
		result  object
		state   string
		hasPlan bool
	}{
		{"written", answered("u1", "toolu_1", "Todos have been modified successfully.", object{"oldTodos": []object{}, "newTodos": []object{{"content": "Fix the swipe", "status": "in_progress", "activeForm": "Fixing the swipe"}}}, nil), CallDone, true},
		{"failed", failedResult("u1", "toolu_1", "<tool_use_error>InputValidationError: TodoWrite failed due to the following issue:\nThe required parameter `todos[0].activeForm` is missing</tool_use_error>"), CallFailed, false},
		{"denied", answered("u1", "toolu_1", "Permission for this action was denied.", "Error: Permission denied", object{"permissionDecision": object{"decision": "reject", "source": "config"}, "toolDenialKind": "automode-blocked"}), CallDenied, false},
		{"interrupted", answered("u1", "toolu_1", "The user doesn't want to proceed with this tool use.", object{"interrupted": true}, object{"toolDenialKind": "interrupted"}), CallInterrupted, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			path := conversation(t, home,
				answer("a1", "msg_1", useBlock("toolu_1", "TodoWrite", object{"todos": []object{{"content": "Fix the swipe", "status": "in_progress"}}})),
				test.result,
			)
			reader := Reader{Home: home}

			// Act
			page, _, err := reader.MessagePage(path, -1)

			// Assert
			if err != nil || len(page.Messages) != 1 || len(page.Messages[0].Blocks) != 1 {
				t.Fatalf("MessagePage = %+v, %v; want one call", page.Messages, err)
			}
			call := page.Messages[0].Blocks[0]
			if call.State != test.state || (len(call.Plan) != 0) != test.hasPlan {
				t.Fatalf("call = %+v; want state %q and a plan %v", call, test.state, test.hasPlan)
			}
		})
	}
}

// Each harness's plan is read from its own tool's shape alone: Claude Code's
// TodoWrite from its todos object, and Codex's update_plan from its plan in
// the arguments Codex records as a JSON string.
func TestAPlanIsReadOnlyFromItsOwnToolsShape(t *testing.T) {
	for _, test := range []struct{ name, tool, input string }{
		{"a TodoWrite holding a plan", "TodoWrite", `{"plan":[{"step":"Fix the swipe","status":"pending"}]}`},
		{"a TodoWrite with its todos as a string", "TodoWrite", `"{\"todos\":[{\"content\":\"Fix the swipe\",\"status\":\"pending\"}]}"`},
		{"an update_plan holding todos", "update_plan", `"{\"todos\":[{\"content\":\"Fix the swipe\",\"status\":\"pending\"}]}"`},
		{"an update_plan with its arguments as an object", "update_plan", `{"plan":[{"step":"Fix the swipe","status":"pending"}]}`},
		{"another tool holding todos", "Write", `{"todos":[{"content":"Fix the swipe","status":"pending"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			plan := callPlan(test.tool, []byte(test.input))

			// Assert
			if plan != nil {
				t.Fatalf("callPlan(%q, %s) = %+v; want no plan", test.tool, test.input, plan)
			}
		})
	}
}
