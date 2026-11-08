package sessionstream

import (
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
