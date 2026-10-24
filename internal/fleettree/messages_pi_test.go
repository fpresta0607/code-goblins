package fleettree

import (
	"reflect"
	"strconv"
	"testing"
	"time"
)

// The entries below are made up in the shape pi 0.85 writes a session in: a
// header, then one entry per line, each naming the entry it hangs under, so a
// /tree branch leaves entries off the path the view follows.

func piHeader() object {
	return object{"type": "session", "version": 3, "id": "pi-1", "timestamp": at.Format(time.RFC3339Nano), "cwd": workDir}
}

// piEntry is one entry of a pi session's tree.
func piEntry(kind, id, parent string, fields object) object {
	entry := object{"type": kind, "id": id, "timestamp": at.Format(time.RFC3339Nano)}
	if parent == "" {
		entry["parentId"] = nil
	} else {
		entry["parentId"] = parent
	}
	for key, value := range fields {
		entry[key] = value
	}
	return entry
}

func piMessageEntry(id, parent, role string, fields object) object {
	message := object{"role": role, "timestamp": float64(at.UnixMilli())}
	for key, value := range fields {
		message[key] = value
	}
	return piEntry("message", id, parent, object{"message": message})
}

// piSession writes a pi session for the folder under home and returns its path.
func piSession(t *testing.T, home string, entries ...any) string {
	t.Helper()
	path := home + "/.pi/agent/sessions/--C--work-repo--/20261006_213000_pi-1.jsonl"
	writeLines(t, path, at, entries...)
	return path
}

// piConversation is a session with one answered call, a compaction, and a turn
// abandoned by a /tree branch, whose second prompt is the leaf: the view shows
// the path to that leaf and nothing of the abandoned answer.
func piConversation(t *testing.T) (home, path string) {
	t.Helper()
	home = t.TempDir()
	path = piSession(t, home,
		piHeader(),
		piMessageEntry("u1", "", "user", object{"content": "Read the brief"}),
		piMessageEntry("a1", "u1", "assistant", object{
			"provider": "acme", "model": "thinker-1", "stopReason": "toolUse",
			"content": []object{
				{"type": "thinking", "thinking": "It names a file."},
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
		piEntry("compaction", "c1", "a2", object{"summary": "Earlier work, in short.", "firstKeptEntryId": "u1", "tokensBefore": 900}),
		piMessageEntry("a3", "c1", "assistant", object{
			"provider": "acme", "model": "thinker-1", "stopReason": "aborted", "errorMessage": "the provider refused",
			"content": []object{{"type": "text", "text": "Half an answer"}},
		}),
		piMessageEntry("u2", "c1", "user", object{"content": "Start again from here"}),
	)
	return home, path
}

func readPi(t *testing.T, reader *Reader, path string) MessagePage {
	t.Helper()
	page, _, err := reader.MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestAPiSessionReadsAlongItsActiveBranchOnly(t *testing.T) {
	// Arrange
	_, path := piConversation(t)
	reader := Reader{Home: t.TempDir()}

	// Act
	page := readPi(t, &reader, path)

	// Assert
	want := []Message{
		{ID: "u1", Seq: 0, Kind: MessagePrompt, At: at, Source: FromOverlord, Text: "Read the brief"},
		{ID: "a1", Seq: 1, Kind: MessageReply, At: at, Model: "acme/thinker-1", Blocks: []Block{
			{Kind: BlockThinking, Text: "It names a file."},
			{Kind: BlockText, Text: "Reading b.go."},
			{Kind: BlockCall, CallID: "call-1", Tool: "read", Subject: "b.go", State: "done", Result: "package b"},
		}},
		{ID: "a2", Seq: 2, Kind: MessageReply, At: at, Model: "acme/thinker-1", Blocks: []Block{{Kind: BlockText, Text: "Done."}}},
		{ID: "c1", Seq: 3, Kind: MessageCompaction, At: at, Trigger: "auto", Text: "Earlier work, in short."},
		{ID: "u2", Seq: 4, Kind: MessagePrompt, At: at, Source: FromOverlord, Text: "Start again from here"},
	}
	if len(page.Messages) != len(want) {
		t.Fatalf("read %d messages, want %d: %+v", len(page.Messages), len(want), page.Messages)
	}
	for i, message := range want {
		if got := page.Messages[i]; !equalMessages(got, message) {
			t.Errorf("message %d is %+v, want %+v", i, got, message)
		}
	}
	for _, message := range page.Messages {
		if message.ID == "a3" {
			t.Fatal("the abandoned turn of the tree is shown")
		}
	}
}

func equalMessages(got, want Message) bool {
	if got.At.UnixNano() != want.At.UnixNano() {
		return false
	}
	got.At, want.At = time.Time{}, time.Time{}
	return reflect.DeepEqual(got, want)
}

func TestAPiTurnEndedByAnAbortShowsItsNotice(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := piSession(t, home,
		piHeader(),
		piMessageEntry("u1", "", "user", object{"content": "Do it"}),
		piMessageEntry("a1", "u1", "assistant", object{
			"provider": "acme", "model": "thinker-1", "stopReason": "aborted", "errorMessage": "the provider refused",
			"content": []object{{"type": "text", "text": "Half an answer"}},
		}),
	)
	reader := Reader{Home: t.TempDir()}

	// Act
	page := readPi(t, &reader, path)

	// Assert
	if len(page.Messages) != 3 {
		t.Fatalf("read %d messages, want 3: %+v", len(page.Messages), page.Messages)
	}
	if notice := page.Messages[2]; notice.Kind != MessageInterrupt || notice.Text != "the provider refused" {
		t.Fatalf("the abort reads as %+v", notice)
	}
}

func TestAPiSessionWithNoFileYetHoldsNoMessages(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := home + "/.pi/agent/sessions/--C--work-repo--/20261006_213000_pi-1.jsonl"
	reader := Reader{Home: home}
	if !isPiSession(path) {
		t.Fatalf("%s is not read as a pi session", path)
	}

	// Act
	page, cursor, err := reader.MessagePage(path, -1)

	// Assert
	if err != nil {
		t.Fatalf("a pi session pi has not written yet failed: %v", err)
	}
	if len(page.Messages) != 0 || page.More {
		t.Fatalf("read %+v, want no messages", page)
	}
	if cursor.epoch == 0 {
		t.Fatal("the empty session holds no cursor")
	}
}

func TestAPiReadAnswersOnlyWhatChangedAndAnewAfterATreeSwitch(t *testing.T) {
	// Arrange
	_, path := piConversation(t)
	reader := Reader{Home: t.TempDir()}
	page, cursor, err := reader.MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 5 {
		t.Fatalf("read %d messages, want 5", len(page.Messages))
	}

	// Act: nothing written since the page.
	whileNothing, now, isAnew, err := reader.MessagesSince(path, cursor)
	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(whileNothing) != 0 || isAnew {
		t.Fatalf("a read that changed nothing answers %d messages, anew %v", len(whileNothing), isAnew)
	}

	// Act: a new prompt under the leaf.
	appendLines(t, path, piMessageEntry("u3", "u2", "user", object{"content": "And one more"}))
	changed, _, isAnew, err := reader.MessagesSince(path, now)
	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if isAnew {
		t.Fatal("a prompt added to the branch took the session anew")
	}
	if len(changed) != 1 || changed[0].ID != "u3" {
		t.Fatalf("a read answers %+v, want only the new prompt", changed)
	}

	// Act: the tree moves back to the abandoned answer.
	appendLines(t, path, piMessageEntry("u4", "a3", "user", object{"content": "Back to it"}))
	switched, _, isAnew, err := reader.MessagesSince(path, MessageCursor{now.epoch, now.revision})
	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !isAnew {
		t.Fatal("a tree switch left the view on the branch it had")
	}
	if len(switched) != 0 {
		t.Fatalf("a tree switch answered %d messages, want a page instead", len(switched))
	}
	if again := readPi(t, &reader, path); len(again.Messages) != 7 || again.Messages[6].ID != "u4" {
		t.Fatalf("the switched branch reads %+v", again.Messages)
	}
}

func TestAPiPicturesInAPromptAndAResultReadBack(t *testing.T) {
	// Arrange
	home := t.TempDir()
	picture := "iVBORw0KGgoAAAANSUhEUg=="
	path := piSession(t, home,
		piHeader(),
		piMessageEntry("u1", "", "user", object{"content": []object{
			{"type": "text", "text": "What is this"},
			{"type": "image", "data": picture, "mimeType": "image/png"},
		}}),
	)
	reader := Reader{Home: t.TempDir()}

	// Act
	page := readPi(t, &reader, path)

	// Assert
	if len(page.Messages) != 1 || len(page.Messages[0].Pictures) != 1 {
		t.Fatalf("the prompt reads as %+v", page.Messages)
	}
	kept := page.Messages[0].Pictures[0]
	if kept.ID != "u1:1" {
		t.Fatalf("the picture is named %q, want the entry and its block", kept.ID)
	}
	if kept.MediaType != "image/png" || kept.Bytes != 16 {
		t.Fatalf("the picture reads as %+v, want 16 bytes of PNG", kept)
	}
	mediaType, data, err := reader.MessagePicture(path, kept.ID)
	if err != nil || mediaType != "image/png" || len(data) == 0 {
		t.Fatalf("the picture reads back as %q %d bytes, %v", mediaType, len(data), err)
	}
}

func TestAPiKeepsOnlyThePictureTypesTheBoardMayServe(t *testing.T) {
	// Arrange: an SVG, an HTML image and a PNG in one prompt, and an SVG in a
	// call's result.
	home := t.TempDir()
	path := piSession(t, home,
		piHeader(),
		piMessageEntry("u1", "", "user", object{"content": []object{
			{"type": "text", "text": "Which of these"},
			{"type": "image", "data": "PHN2Zz48L3N2Zz4=", "mimeType": "image/svg+xml"},
			{"type": "image", "data": "PGh0bWw+PC9odG1sPg==", "mimeType": "text/html"},
			{"type": "image", "data": "iVBORw0KGgoAAAANSUhEUg==", "mimeType": "image/png"},
		}}),
		piMessageEntry("a1", "u1", "assistant", object{
			"provider": "acme", "model": "thinker-1", "stopReason": "toolUse",
			"content": []object{{"type": "toolCall", "id": "call-1", "name": "read", "arguments": object{"path": "a.svg"}}},
		}),
		piMessageEntry("t1", "a1", "toolResult", object{
			"toolCallId": "call-1", "toolName": "read", "isError": false,
			"content": []object{{"type": "image", "data": "PHN2Zz48L3N2Zz4=", "mimeType": "image/svg+xml"}},
		}),
	)
	reader := Reader{Home: t.TempDir()}

	// Act
	page := readPi(t, &reader, path)

	// Assert
	if len(page.Messages) != 2 {
		t.Fatalf("read %d messages, want 2", len(page.Messages))
	}
	kept := page.Messages[0].Pictures
	if len(kept) != 1 || kept[0].MediaType != "image/png" {
		t.Fatalf("the prompt keeps %+v, want only its PNG", kept)
	}
	if result := page.Messages[1].Blocks[0]; len(result.Pictures) != 0 {
		t.Fatalf("the result keeps %+v, want nothing the board may not serve", result.Pictures)
	}
	// The refused block's place is still its own, so no other picture answers it.
	if _, _, err := reader.MessagePicture(path, "u1:1"); err == nil {
		t.Fatal("a picture the board may not serve reads back")
	}
	if _, _, err := reader.MessagePicture(path, "u1:3"); err != nil {
		t.Fatalf("the allowed picture no longer reads: %v", err)
	}
}

func TestAPiUnchangedPollsParseNothingAndRebuildNothing(t *testing.T) {
	// Arrange: a long session of many turns.
	home := t.TempDir()
	entries := []any{piHeader()}
	parent := ""
	for turn := 0; turn < 300; turn++ {
		ask := "u" + strconv.Itoa(turn)
		answer := "a" + strconv.Itoa(turn)
		entries = append(entries,
			piMessageEntry(ask, parent, "user", object{"content": "Turn " + strconv.Itoa(turn)}),
			piMessageEntry(answer, ask, "assistant", object{
				"provider": "acme", "model": "thinker-1", "stopReason": "stop",
				"content": []object{{"type": "text", "text": "Answer " + strconv.Itoa(turn)}},
			}),
		)
		parent = answer
	}
	path := piSession(t, home, entries...)
	reader := Reader{Home: t.TempDir()}
	page := readPi(t, &reader, path)
	if len(page.Messages) != MessagesPerPage || page.Messages[len(page.Messages)-1].ID != "a299" {
		t.Fatalf("the newest page holds %d messages ending at %q", len(page.Messages), page.Messages[len(page.Messages)-1].ID)
	}
	log := reader.piLogs[path]
	if log == nil {
		t.Fatal("no pi log is kept")
	}
	log.mu.Lock()
	parsed, revision, epoch, held := log.parsed, log.revision, log.epoch, len(log.messages)
	log.mu.Unlock()
	if held != 600 {
		t.Fatalf("the log holds %d messages, want 600", held)
	}

	// Act: ten polls that find the file as it was.
	for poll := 0; poll < 10; poll++ {
		if _, _, err := reader.MessagePage(path, -1); err != nil {
			t.Fatal(err)
		}
		_, _, isAnew, err := reader.MessagesSince(path, MessageCursor{epoch, revision})
		if err != nil {
			t.Fatal(err)
		}
		if isAnew {
			t.Fatal("an unchanged poll took the session anew")
		}
	}

	// Assert
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.parsed != parsed {
		t.Fatalf("unchanged polls parsed %d lines, want none", log.parsed-parsed)
	}
	if log.revision != revision || log.epoch != epoch {
		t.Fatalf("unchanged polls moved the log from %d/%d to %d/%d", revision, epoch, log.revision, log.epoch)
	}
}

func TestAPiTreeSelectionShowsOnlyOnceItsNextTurnIsWritten(t *testing.T) {
	// Arrange: a /tree selection of an earlier node writes nothing at all, so
	// the file is as it was and the view holds the branch it had.
	_, path := piConversation(t)
	reader := Reader{Home: t.TempDir()}
	before := readPi(t, &reader, path)
	log := reader.piLogs[path]
	if log == nil {
		t.Fatal("no pi log is kept")
	}
	log.mu.Lock()
	size, revision := log.offset, log.revision
	log.mu.Unlock()

	// Act: the selection, then the turn that names it as its parent.
	afterSelection := readPi(t, &reader, path)
	appendLines(t, path, piMessageEntry("u5", "a1", "user", object{"content": "From the earlier node"}))
	afterTurn := readPi(t, &reader, path)

	// Assert
	if len(afterSelection.Messages) != len(before.Messages) {
		t.Fatalf("a selection that wrote nothing changed the view: %d messages, want %d", len(afterSelection.Messages), len(before.Messages))
	}
	log.mu.Lock()
	if log.offset <= size || log.revision <= revision {
		log.mu.Unlock()
		t.Fatal("the turn that named the selected node was not read")
	}
	log.mu.Unlock()
	// The branch is the selected node's own, so the turn's prompt ends it and
	// nothing of the abandoned answer after that node is shown.
	if len(afterTurn.Messages) == 0 {
		t.Fatal("the selected branch holds nothing")
	}
	last := afterTurn.Messages[len(afterTurn.Messages)-1]
	if last.ID != "u5" {
		t.Fatalf("the branch ends at %q, want the new prompt", last.ID)
	}
	for _, message := range afterTurn.Messages {
		if message.ID == "a2" || message.ID == "c1" || message.ID == "u2" {
			t.Fatalf("the abandoned path still shows %q", message.ID)
		}
	}
}

func TestAPiBatchWithABranchSwitchKeepsEveryLaterEntry(t *testing.T) {
	// Arrange: one read that carries a /tree switch and the whole new turn
	// after it, prompt, assistant and tool result together.
	_, path := piConversation(t)
	reader := Reader{Home: t.TempDir()}
	if page := readPi(t, &reader, path); len(page.Messages) != 5 {
		t.Fatalf("read %d messages, want 5", len(page.Messages))
	}

	// Act: the selected node is a1, so the whole batch after it is the new
	// branch, and the read moves past every line of it at once.
	appendLines(t, path,
		piMessageEntry("u5", "a1", "user", object{"content": "From the earlier node"}),
		piMessageEntry("a5", "u5", "assistant", object{
			"provider": "acme", "model": "thinker-1", "stopReason": "toolUse",
			"content": []object{
				{"type": "text", "text": "Reading again."},
				{"type": "toolCall", "id": "call-5", "name": "read", "arguments": object{"path": "c.go"}},
			},
		}),
		piMessageEntry("t5", "a5", "toolResult", object{
			"toolCallId": "call-5", "toolName": "read", "isError": false,
			"content": []object{{"type": "text", "text": "package c"}},
		}),
		piMessageEntry("a6", "t5", "assistant", object{
			"provider": "acme", "model": "thinker-1", "stopReason": "stop",
			"content": []object{{"type": "text", "text": "Finished."}},
		}),
	)
	page := readPi(t, &reader, path)

	// Assert: nothing of the batch was dropped, and the abandoned path is gone.
	var ids []string
	for _, message := range page.Messages {
		ids = append(ids, message.ID)
	}
	want := []string{"u1", "a1", "u5", "a5", "a6"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("the branch reads %v, want %v", ids, want)
	}
	if result := page.Messages[3].Blocks[1]; result.State != "done" || result.Result != "package c" {
		t.Fatalf("the call after the switch reads %+v", result)
	}
	log := reader.piLogs[path]
	if log == nil {
		t.Fatal("no pi log is kept")
	}
	if len(log.entries) != 11 {
		t.Fatalf("the log keeps %d entries, want every one of the 11 lines", len(log.entries))
	}
}

func TestAPiCursorAfterIdleEvictionIsNotAnsweredFromTheNewLog(t *testing.T) {
	// Arrange
	_, path := piConversation(t)
	reader := Reader{Home: t.TempDir()}
	_, cursor, err := reader.MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}

	// Act: the log goes idle and is dropped, as a long-quiet view's is, then the
	// same path is read again and a view holding the older cursor asks.
	reader.messagesMu.Lock()
	reader.forgetPiLogs(time.Now().Add(messageLogIdle + time.Minute))
	reader.messagesMu.Unlock()
	if _, _, err := reader.MessagePage(path, -1); err != nil {
		t.Fatal(err)
	}
	changed, now, isAnew, err := reader.MessagesSince(path, cursor)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if now.epoch == cursor.epoch {
		t.Fatalf("a log made again after eviction reused epoch %d", now.epoch)
	}
	if !isAnew {
		t.Fatal("a view holding the evicted log's cursor was answered as if its page still stood")
	}
	if len(changed) != 0 {
		t.Fatalf("a read taken anew answered %d messages, want a page instead", len(changed))
	}
}

func TestAPiBranchAtACallDropsTheResultOfTheAbandonedOne(t *testing.T) {
	// Arrange: the call and its result are read first, so the view holds the
	// call as done, and the tree then moves to a branch that ends at the call
	// itself and leaves its result out.
	picture := "iVBORw0KGgoAAAANSUhEUg=="
	home := t.TempDir()
	path := piSession(t, home,
		piHeader(),
		piMessageEntry("u1", "", "user", object{"content": "Read it"}),
		piMessageEntry("a1", "u1", "assistant", object{
			"provider": "acme", "model": "thinker-1", "stopReason": "toolUse",
			"content": []object{{"type": "toolCall", "id": "call-1", "name": "read", "arguments": object{"path": "a.go"}}},
		}),
		piMessageEntry("t1", "a1", "toolResult", object{
			"toolCallId": "call-1", "toolName": "read", "isError": true,
			"content": []object{
				{"type": "text", "text": "no such file"},
				{"type": "image", "data": picture, "mimeType": "image/png"},
			},
		}),
	)
	reader := Reader{Home: t.TempDir()}
	if page := readPi(t, &reader, path); len(page.Messages) != 2 || page.Messages[1].Blocks[0].State != "failed" {
		t.Fatalf("the call first reads as %+v", page.Messages)
	}

	// Act: a new prompt hangs under the call, so its result is off the branch.
	appendLines(t, path, piMessageEntry("u2", "a1", "user", object{"content": "Ask again instead"}))
	page := readPi(t, &reader, path)

	// Assert: the branch shows the call still running, with nothing of the
	// abandoned result, and the retained entry is unchanged.
	var call Block
	found := false
	for _, message := range page.Messages {
		for _, block := range message.Blocks {
			if block.CallID == "call-1" {
				call, found = block, true
			}
		}
	}
	if !found {
		t.Fatalf("the branch lost the call: %+v", page.Messages)
	}
	if call.State != "running" || call.Result != "" || len(call.Pictures) != 0 {
		t.Fatalf("the call kept the abandoned branch's result: %+v", call)
	}
	log := reader.piLogs[path]
	if log == nil {
		t.Fatal("no pi log is kept")
	}
	for _, entry := range log.entries {
		for _, block := range entry.blocks {
			if block.CallID == "call-1" && (block.State != "running" || block.Result != "") {
				t.Fatalf("the retained entry was mutated: %+v", block)
			}
		}
	}
}
