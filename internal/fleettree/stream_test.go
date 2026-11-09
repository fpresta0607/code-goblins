package fleettree

import (
	"reflect"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/sessionstream"
)

// The fixtures below are model-free: each is one whole turn as a harness
// writes its own record, a prompt, an answer, a call, what came back and the
// answer that closed it. No provider is called, and none is named but in the
// record itself.

func claudeTurn(t *testing.T) MessagePage {
	t.Helper()
	home := t.TempDir()
	path := conversation(t, home,
		prompted("p1", "Read the brief"),
		answer("r1", "msg-1", textBlock("Reading b.go.")),
		answer("r2", "msg-1", useBlock("call-1", "Read", object{"file_path": "b.go"})),
		answered("t1", "call-1", "package b", object{}, object{}),
		answer("r3", "msg-2", textBlock("Done.")),
	)
	page, _, err := (&Reader{Home: home}).MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

// claudeSubAgentRunning is a Claude Code turn whose sub-agent still runs: its
// call names the baby goblin it started, and no result has come back yet.
func claudeSubAgentRunning(t *testing.T) MessagePage {
	t.Helper()
	home := t.TempDir()
	path := conversation(t, home,
		prompted("p1", "Review the branch"),
		answer("r1", "msg-1", useBlock("call-1", "Task", object{"subagent_type": "code-reviewer", "description": "Review the branch", "prompt": "Review it."})),
	)
	page, _, err := (&Reader{Home: home}).MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}
	if call := page.Messages[len(page.Messages)-1].Blocks[0]; call.State != CallRunning || call.Agent == (AgentLink{}) {
		t.Fatalf("the fixture's sub-agent call is not running with its baby goblin: %+v", call)
	}
	return page
}

func codexTurn(t *testing.T) MessagePage {
	t.Helper()
	home := t.TempDir()
	const session = "01a16c37-1768-7270-8f61-76f09c77c962"
	path := codexRollout(t, home, session,
		codexMeta(session, workDir),
		codexTurnContext("gpt-6-astra"),
		codexUserSaid("Read the brief"),
		codexAgentSaid("Reading b.go."),
		codexCall("call-1", "exec", "text(await tools.exec_command({cmd:\"type b.go\"}));"),
		codexUsage(),
		codexOutput("call-1", "package b"),
		codexUsage(),
		codexAgentSaid("Done."),
	)
	return readCodex(t, &Reader{Home: home}, path)
}

func piTurn(t *testing.T) MessagePage {
	t.Helper()
	home := t.TempDir()
	path := piSession(t, home,
		piHeader(),
		piMessageEntry("u1", "", "user", object{"content": "Read the brief"}),
		piMessageEntry("a1", "u1", "assistant", object{
			"provider": "acme", "model": "thinker-1", "stopReason": "toolUse",
			"content": []object{
				{"type": "text", "text": "Reading b.go."},
				{"type": "toolCall", "id": "call-1", "name": "read", "arguments": object{"path": "b.go"}},
			},
		}),
		piMessageEntry("t1", "a1", "toolResult", object{
			"toolCallId": "call-1", "toolName": "read", "isError": false,
			"content": []object{{"type": "text", "text": "package b"}},
		}),
		piMessageEntry("a2", "t1", "assistant", object{
			"provider": "acme", "model": "thinker-1", "stopReason": "stop",
			"content": []object{{"type": "text", "text": "Done."}},
		}),
	)
	return readPi(t, &Reader{Home: t.TempDir()}, path)
}

// TestEachHarnesssTurnIsTheSameEvents is the claim the stream exists for: a
// turn of Claude Code, Codex and pi is one shape of events, so a new harness
// is one adapter and no reader of the stream changes with it.
func TestEachHarnesssTurnIsTheSameEvents(t *testing.T) {
	// Arrange
	turns := map[string]MessagePage{"claude": claudeTurn(t), "codex": codexTurn(t), "pi": piTurn(t)}
	want := []string{
		sessionstream.KindPrompt, sessionstream.KindAssistant,
		sessionstream.KindToolCall, sessionstream.KindToolResult, sessionstream.KindAssistant,
	}

	// Act
	shapes := map[string][]string{}
	for harness, page := range turns {
		id := sessionstream.Identity{Task: "cg-session-event-stream", Generation: "1201-1", Harness: harness, Session: "session-1"}
		for _, event := range StreamEvents(id, page.Messages) {
			shapes[harness] = append(shapes[harness], event.Kind)
		}
	}

	// Assert
	for harness, got := range shapes {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s turn: %v; want %v", harness, got, want)
		}
	}
}

// TestAStreamProjectsBackToTheMessagesItCameFrom proves the stream is the
// same conversation the harness's record is, not a second guess at it.
func TestAStreamProjectsBackToTheMessagesItCameFrom(t *testing.T) {
	for name, page := range map[string]MessagePage{"claude": claudeTurn(t), "codex": codexTurn(t), "pi": piTurn(t), "claude sub-agent": claudeSubAgentRunning(t)} {
		// Arrange
		harness, _, _ := strings.Cut(name, " ")
		id := sessionstream.Identity{Task: "cg-session-event-stream", Generation: "1201-1", Harness: harness, Session: "session-1"}

		// Act
		events := StreamEvents(id, page.Messages)
		for at := range events {
			events[at].Seq = at
		}
		projected := MessagesOf(events)

		// Assert
		if !reflect.DeepEqual(projected, page.Messages) {
			t.Fatalf("%s projected back:\n got %+v\nwant %+v", name, projected, page.Messages)
		}
	}
}

// TestATurnIsNamedByThePromptThatBeganIt keeps every event of a turn under
// that turn, so a reader can say which turn ended.
func TestATurnIsNamedByThePromptThatBeganIt(t *testing.T) {
	// Arrange
	page := claudeTurn(t)
	id := sessionstream.Identity{Task: "cg-session-event-stream", Generation: "1201-1", Harness: "claude", Session: "session-1"}

	// Act
	events := StreamEvents(id, page.Messages)
	turn := TurnPrompt(page.Messages).ID

	// Assert
	if turn == "" || turn != page.Messages[0].ID {
		t.Fatalf("turn of a session with one prompt: %q; want %q", turn, page.Messages[0].ID)
	}
	for _, event := range events {
		if event.Turn != turn {
			t.Fatalf("event %s of %s names turn %q; want %q", event.Kind, event.Message, event.Turn, turn)
		}
	}
}

// TestWhatNeverReachesTheModelBeginsNoTurn keeps the fleet's own words to a
// session, and a command the harness ran itself, inside the turn they landed
// in, since no hook reports a turn for either, while a slash command that
// hands the model its prompt begins one.
func TestWhatNeverReachesTheModelBeginsNoTurn(t *testing.T) {
	for name, tc := range map[string]struct {
		landed Message
		kind   string
		turn   string
	}{
		"a notice":                        {prompt(2, "n1", FromNotice, "A background task ended"), sessionstream.KindNotice, "p1"},
		"a ! command":                     {prompt(2, "c1", FromShell, "git status"), sessionstream.KindPrompt, "p1"},
		"a slash command the harness ran": {Message{ID: "c1", Seq: 2, Kind: MessagePrompt, At: at, Source: FromSlash, Text: "/model opus", Output: "Set model to opus"}, sessionstream.KindPrompt, "p1"},
		"a slash command that prompts":    {prompt(2, "c1", FromSlash, "/review"), sessionstream.KindPrompt, "c1"},
	} {
		// Arrange
		messages := []Message{
			prompt(0, "p1", FromOverlord, "Read the brief"),
			reply(1, "r1", Block{Kind: BlockText, Text: "Reading."}),
			tc.landed,
		}
		id := sessionstream.Identity{Task: "cg-session-event-stream", Generation: "1201-1", Harness: "claude", Session: "session-1"}

		// Act
		events := StreamEvents(id, messages)
		turn := TurnPrompt(messages).ID

		// Assert
		if turn != tc.turn {
			t.Fatalf("%s: the session's turn is %q; want %q", name, turn, tc.turn)
		}
		if last := events[len(events)-1]; last.Kind != tc.kind || last.Message != tc.landed.ID || last.Turn != tc.turn {
			t.Fatalf("%s: its event %+v; want a %s of turn %s", name, last, tc.kind, tc.turn)
		}
	}
}

// TestALaterEventOfACallReplacesTheOneBeforeIt is what lets a call that is
// running become the call that finished without appending the call twice.
func TestALaterEventOfACallReplacesTheOneBeforeIt(t *testing.T) {
	// Arrange
	running := []sessionstream.Event{{
		ID: "a", Seq: 0, Kind: sessionstream.KindToolCall, Message: "r1", Block: 0,
		CallID: "call-1", Tool: "Read", Subject: "b.go", State: CallRunning,
	}}
	done := []sessionstream.Event{
		running[0],
		{ID: "b", Seq: 1, Kind: sessionstream.KindToolResult, Message: "r1", Block: 0, CallID: "call-1", State: "done", Result: "package b"},
	}

	// Act
	folded := MessagesOf(done)

	// Assert: one call, finished, with what came back.
	if len(folded) != 1 || len(folded[0].Blocks) != 1 {
		t.Fatalf("folded call: %+v", folded)
	}
	if block := folded[0].Blocks[0]; block.CallID != "call-1" || block.State != "done" || block.Result != "package b" {
		t.Fatalf("folded call block: %+v", block)
	}
}

// TestAHarnessThatRewroteItsWordsReplacesThem keeps a record that changed
// behind the reader from growing a second copy of the same message.
func TestAHarnessThatRewroteItsWordsReplacesThem(t *testing.T) {
	for name, events := range map[string][]sessionstream.Event{
		"a reply's words": {
			{ID: "a", Seq: 0, Kind: sessionstream.KindAssistant, Message: "r1", Block: 0, Text: "first words"},
			{ID: "b", Seq: 1, Kind: sessionstream.KindAssistant, Message: "r1", Block: 0, Text: "rewritten words"},
		},
		"a prompt's words": {
			{ID: "a", Seq: 0, Kind: sessionstream.KindPrompt, Message: "p1", Text: "first words"},
			{ID: "b", Seq: 1, Kind: sessionstream.KindPrompt, Message: "p1", Text: "rewritten words"},
		},
	} {
		// Act
		folded := MessagesOf(events)

		// Assert
		if len(folded) != 1 {
			t.Fatalf("%s: folded %d messages; want 1", name, len(folded))
		}
		if folded[0].Kind == MessageReply {
			if len(folded[0].Blocks) != 1 || folded[0].Blocks[0].Text != "rewritten words" {
				t.Fatalf("%s: folded reply %+v", name, folded[0])
			}
			continue
		}
		if folded[0].Text != "rewritten words" {
			t.Fatalf("%s: folded prompt %+v", name, folded[0])
		}
	}
}

// TestAnEmptySessionKeepsNoTurn keeps a session that has only heard the
// fleet's own words from claiming a turn it never had.
func TestAnEmptySessionKeepsNoTurn(t *testing.T) {
	if turn := TurnPrompt([]Message{prompt(0, "n1", FromNotice, "hello")}).ID; turn != "" {
		t.Fatalf("turn of a session with no prompt: %q", turn)
	}
}

// TestAHarnessThatRewroteItsRecordRestatesIt keeps a stream that only appends
// from showing a message the harness dropped, as pi's /tree branch reset
// drops an abandoned answer.
func TestAHarnessThatRewroteItsRecordRestatesIt(t *testing.T) {
	// Arrange: a session whose stream holds an answer the harness has since
	// dropped, as a pi branch reset leaves it.
	id := sessionstream.Identity{Task: "cg-session-event-stream", Generation: "1201-1", Harness: "pi", Session: "session-1"}
	messages := []Message{
		{ID: "u1", Seq: 0, Kind: MessagePrompt, Source: FromOverlord, Text: "First try"},
		{ID: "a1", Seq: 1, Kind: MessageReply, Blocks: []Block{{Kind: BlockText, Text: "Abandoned"}}},
	}
	previous := StreamEvents(id, messages)
	for at := range previous {
		previous[at].Seq = at
	}
	kept := []Message{{ID: "u1", Seq: 0, Kind: MessagePrompt, Source: FromOverlord, Text: "First try"}, {ID: "u2", Seq: 1, Kind: MessagePrompt, Source: FromOverlord, Text: "Try again"}}

	// Act
	restated := RestatedEvent(id, kept)
	restated.Seq = len(previous)
	folded := MessagesOf(append(previous, restated))

	// Assert
	if !reflect.DeepEqual(folded, kept) {
		t.Fatalf("a restated conversation:\n got %+v\nwant %+v", folded, kept)
	}
}
