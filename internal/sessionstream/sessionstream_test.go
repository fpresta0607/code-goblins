package sessionstream

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

var at = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func goblin() Identity {
	return Identity{Task: "cg-stream", Generation: "1201-1", Harness: "claude", Session: "session-1"}
}

func event(id Identity, kind, message string, block int) Event {
	return Event{ID: EventID(id, kind, message, "", block, "words"), At: at, Kind: kind, Message: message, Block: block, Text: "words"}
}

func TestTheStreamLivesBesideTheIdentityRecord(t *testing.T) {
	stateDir := t.TempDir()
	for name, want := range map[string]struct {
		id   Identity
		path string
	}{
		"goblin": {goblin(), filepath.Join("conversations", "cg-stream", "1201-1", "session-1.jsonl")},
		"ceo":    {Identity{Harness: "pi", Session: "pi-7"}, filepath.Join("conversations", "cfo", "pi-7.jsonl")},
	} {
		got, err := want.id.Path(stateDir)
		if err != nil || got != filepath.Join(stateDir, want.path) {
			t.Fatalf("%s stream: %q, %v; want %q", name, got, err, filepath.Join(stateDir, want.path))
		}
	}
}

func TestAnIdentityThatCouldReachOutsideTheFolderIsRefused(t *testing.T) {
	for name, id := range map[string]Identity{
		"traversal in the session":    {Task: "cg-stream", Generation: "g1", Harness: "claude", Session: "../../secrets"},
		"traversal in the generation": {Task: "cg-stream", Generation: "..", Harness: "codex", Session: "s1"},
		"a harness nobody reads":      {Task: "cg-stream", Generation: "g1", Harness: "grok", Session: "s1"},
		"a task that names no task":   {Task: "bad id", Generation: "g1", Harness: "pi", Session: "s1"},
	} {
		if _, err := id.Path(t.TempDir()); err == nil {
			t.Fatalf("%s: identity %+v was accepted", name, id)
		}
	}
}

func TestAppendKeepsOneStreamInOrderAndWritesNothingTwice(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	appender, err := Open(stateDir, goblin())
	if err != nil {
		t.Fatal(err)
	}
	first := []Event{event(goblin(), KindPrompt, "m1", 0), event(goblin(), KindAssistant, "r1", 0)}

	// Act
	written, _, err := appender.Write(first)
	if err != nil || len(written) != 2 {
		t.Fatalf("first write: %d, %v; want 2", len(written), err)
	}
	again, _, err := appender.Write(first)
	if err != nil || len(again) != 0 {
		t.Fatalf("replayed write: %d, %v; want 0", len(again), err)
	}
	reopened, err := Open(stateDir, goblin())
	if err != nil {
		t.Fatal(err)
	}
	later, _, err := reopened.Write([]Event{event(goblin(), KindTurnEnd, "m1", 0)})

	// Assert
	if err != nil || len(later) != 1 {
		t.Fatalf("write after a restart: %d, %v; want 1", len(later), err)
	}
	events, err := Read(stateDir, goblin())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Seq != 0 || events[1].Seq != 1 || events[2].Seq != 2 {
		t.Fatalf("stream: %+v", events)
	}
	if events[2].Kind != KindTurnEnd {
		t.Fatalf("a stream reopened by another writer lost its place: %+v", events)
	}
}

func TestASessionWithNoStreamIsNotAnEmptyConversation(t *testing.T) {
	if _, err := Read(t.TempDir(), goblin()); !errors.Is(err, ErrNoStream) {
		t.Fatalf("no stream: %v; want ErrNoStream", err)
	}
}

func TestAStreamThatCannotBeReadIsReportedAndNeverShownAsEmpty(t *testing.T) {
	for name, data := range map[string]string{
		"a line that is not an event": "{\"id\":\"a\",\"kind\":\"prompt\"}\nnot json\n",
		"an entry naming no event":    "{\"id\":\"a\",\"kind\":\"prompt\"}\n{\"seq\":4}\n",
	} {
		// Arrange
		stateDir := t.TempDir()
		path, err := goblin().Path(stateDir)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}

		// Act
		_, err = Read(stateDir, goblin())

		// Assert
		if !errors.Is(err, ErrUnreadable) {
			t.Fatalf("%s: %v; want ErrUnreadable", name, err)
		}
	}
}

func TestAnEntryStillBeingWrittenIsPassedOver(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	path, err := goblin().Path(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	complete := Encode([]Event{event(goblin(), KindPrompt, "m1", 0)})
	if err := os.WriteFile(path, append(complete, []byte(`{"id":"half","kind":"text"`)...), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	events, err := Read(stateDir, goblin())

	// Assert
	if err != nil || len(events) != 1 || events[0].Kind != KindPrompt {
		t.Fatalf("stream with an unfinished entry: %+v, %v", events, err)
	}
}

func TestAnAppendAfterAnEntryCutShortKeepsTheStreamReadable(t *testing.T) {
	// Arrange: a write was cut short after a whole entry.
	stateDir := t.TempDir()
	path, err := goblin().Path(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	complete := Encode([]Event{event(goblin(), KindPrompt, "m1", 0)})
	if err := os.WriteFile(path, append(complete, []byte(`{"id":"half","kind":"text"`)...), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act: the next append must not bury the cut-short entry mid-stream, where
	// every reader would refuse the stream for good.
	appender, err := Open(stateDir, goblin())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := appender.Write([]Event{event(goblin(), KindAssistant, "r1", 0)}); err != nil {
		t.Fatalf("append after an entry cut short: %v", err)
	}

	// Assert
	events, err := Read(stateDir, goblin())
	if err != nil {
		t.Fatalf("a stream appended after an entry cut short: %v", err)
	}
	if len(events) != 2 || events[0].Message != "m1" || events[1].Message != "r1" || events[1].Seq != 1 {
		t.Fatalf("stream: %+v", events)
	}
}

// TestAnAppendAfterAWriteCutShortKeepsTheStreamReadable keeps a torn entry that
// appeared after the appender read its stream, as a write that failed leaves,
// from being buried by the next append: the append begins at the last whole
// entry whatever the file ends with.
func TestAnAppendAfterAWriteCutShortKeepsTheStreamReadable(t *testing.T) {
	// Arrange: an appender that already knows its stream, whose newest entry was
	// cut short before its newline.
	stateDir := t.TempDir()
	appender, err := Open(stateDir, goblin())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := appender.Write([]Event{event(goblin(), KindPrompt, "m1", 0)}); err != nil {
		t.Fatal(err)
	}
	path, err := goblin().Path(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"id":"half","kind":"text"`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	// Act
	written, _, err := appender.Write([]Event{event(goblin(), KindAssistant, "r1", 0)})

	// Assert
	if err != nil || len(written) != 1 {
		t.Fatalf("append after a write cut short: %d, %v; want 1", len(written), err)
	}
	events, err := Read(stateDir, goblin())
	if err != nil {
		t.Fatalf("a stream appended after a write cut short: %v", err)
	}
	if len(events) != 2 || events[0].Message != "m1" || events[1].Message != "r1" || events[1].Seq != 1 {
		t.Fatalf("stream: %+v", events)
	}
}

func TestAStreamAtItsBoundKeepsWhatTheSessionIsNow(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	appender, err := Open(stateDir, goblin())
	if err != nil {
		t.Fatal(err)
	}
	events := make([]Event, 0, MaxEvents)
	for at := 0; at < MaxEvents; at++ {
		events = append(events, event(goblin(), KindAssistant, "r"+strconv.Itoa(at), 0))
	}
	if _, _, err := appender.Write(events); err != nil {
		t.Fatal(err)
	}

	// Act
	_, isCompacted, err := appender.Write([]Event{event(goblin(), KindAssistant, "newest", 0)})

	// Assert
	if err != nil || !isCompacted {
		t.Fatalf("write at the bound: compacted %v, %v", isCompacted, err)
	}
	kept, err := Read(stateDir, goblin())
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != MaxEvents/2 {
		t.Fatalf("a stream at its bound kept %d events; want %d", len(kept), MaxEvents/2)
	}
	if newest := kept[len(kept)-1]; newest.Message != "newest" || newest.Seq != len(kept)-1 {
		t.Fatalf("a compacted stream lost what the session is now: %+v", newest)
	}
	for at, event := range kept {
		if at > 0 && event.Message == kept[at-1].Message {
			t.Fatalf("a compacted stream begins part way through a message: %+v", kept[:2])
		}
	}
}

func TestTheSameEventDerivedAgainHasTheSameID(t *testing.T) {
	id := goblin()
	one := EventID(id, KindAssistant, "r1", "", 0, "words")
	if other := EventID(id, KindAssistant, "r1", "", 0, "words"); one != other {
		t.Fatalf("the same event: %q and %q", one, other)
	}
	for name, other := range map[string]string{
		"changed words": EventID(id, KindAssistant, "r1", "", 0, "other words"),
		"another block": EventID(id, KindAssistant, "r1", "", 1, "words"),
		"another kind":  EventID(id, KindThinking, "r1", "", 0, "words"),
		"another task":  EventID(Identity{Task: "other", Generation: "1201-1", Harness: "claude", Session: "session-1"}, KindAssistant, "r1", "", 0, "words"),
	} {
		if other == one {
			t.Fatalf("%s: %q names the same event", name, other)
		}
	}
}

// TestACompactionKeepsACallAndItsResultApart keeps what a call was called on
// and the plan it wrote when a stream reaches its bound: the result that
// answers it never folds the call away.
func TestACompactionKeepsACallAndItsResultApart(t *testing.T) {
	// Arrange: a call and its result already in the stream, then one reply
	// written again and again until the stream reaches its bound, so the
	// compaction the next write makes folds what it can of the stream.
	stateDir := t.TempDir()
	appender, err := Open(stateDir, goblin())
	if err != nil {
		t.Fatal(err)
	}
	call := Event{ID: "call", Kind: KindToolCall, Message: "r-call", CallID: "c1", Tool: "Read", Subject: "b.go", State: "running", Plan: []PlanStep{{Text: "read it", Status: "in_progress"}}}
	result := Event{ID: "result", Kind: KindToolResult, Message: "r-call", CallID: "c1", State: "done", Result: "package b"}
	if _, _, err := appender.Write([]Event{call, result}); err != nil {
		t.Fatal(err)
	}
	rewrites := make([]Event, 0, MaxEvents-2)
	for at := 0; at < MaxEvents-2; at++ {
		rewrites = append(rewrites, Event{ID: "reply-" + strconv.Itoa(at), Kind: KindAssistant, Message: "r-reply", Text: strconv.Itoa(at)})
	}
	if _, _, err := appender.Write(rewrites); err != nil {
		t.Fatal(err)
	}

	// Act
	_, isCompacted, err := appender.Write([]Event{event(goblin(), KindAssistant, "newest", 0)})

	// Assert: the call stands beside its result, with what it was called on
	// and the plan it wrote.
	if err != nil || !isCompacted {
		t.Fatalf("write at the bound: compacted %v, %v", isCompacted, err)
	}
	kept, err := Read(stateDir, goblin())
	if err != nil {
		t.Fatal(err)
	}
	var keptCall, keptResult *Event
	for at := range kept {
		switch kept[at].ID {
		case "call":
			keptCall = &kept[at]
		case "result":
			keptResult = &kept[at]
		}
	}
	if keptCall == nil || keptResult == nil {
		t.Fatalf("a compacted stream kept the call %v and its result %v; want both", keptCall != nil, keptResult != nil)
	}
	if keptCall.Subject != "b.go" || len(keptCall.Plan) != 1 {
		t.Fatalf("the call a compaction kept: %+v; want its subject and its plan", *keptCall)
	}
}

// TestAFailedAppendLeavesTheStreamAsItWas keeps a write that fails after part
// of it reached the file from leaving any of it behind: not a torn entry, and
// not a whole one of the events it reported unwritten. The stream is cut back
// to the entry it ended at, and the next write appends after it.
func TestAFailedAppendLeavesTheStreamAsItWas(t *testing.T) {
	// Arrange: a stream of one prompt, and a disk that fills up after the
	// first entry of the next append and part of its second.
	stateDir := t.TempDir()
	appender, err := Open(stateDir, goblin())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := appender.Write([]Event{event(goblin(), KindPrompt, "m1", 0)}); err != nil {
		t.Fatal(err)
	}
	appender.mu.Lock()
	appender.write = func(file *os.File, data []byte) error {
		first := bytes.IndexByte(data, '\n') + 1
		if _, err := file.Write(data[:first+(len(data)-first)/2]); err != nil {
			return err
		}
		return errors.New("the disk filled up")
	}
	appender.mu.Unlock()
	reply := []Event{event(goblin(), KindAssistant, "r1", 0), event(goblin(), KindAssistant, "r2", 0)}

	// Act
	_, _, err = appender.Write(reply)

	// Assert: the append failed, and the stream holds exactly what it held.
	if err == nil {
		t.Fatal("a failed append reported success")
	}
	kept, err := Read(stateDir, goblin())
	if err != nil || len(kept) != 1 || kept[0].Message != "m1" {
		t.Fatalf("a failed append left the stream %+v, %v", kept, err)
	}
	appender.mu.Lock()
	appender.write = writeFile
	appender.mu.Unlock()
	if _, _, err := appender.Write(reply); err != nil {
		t.Fatalf("the write after a failed one: %v", err)
	}
	kept, err = Read(stateDir, goblin())
	if err != nil || len(kept) != 3 || kept[1].Message != "r1" || kept[2].Message != "r2" {
		t.Fatalf("the stream after a failed append and one more: %+v, %v", kept, err)
	}
}
