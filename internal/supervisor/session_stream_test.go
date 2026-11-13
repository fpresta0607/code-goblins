package supervisor

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/sessionstream"
)

func TestWhatASessionsOwnReportSaysOfItsTurn(t *testing.T) {
	for name, tc := range map[string]struct {
		node      Session
		isTracked bool
		ended     bool
		known     bool
	}{
		"a settled turn":                {Session{Phase: "settled"}, true, true, true},
		"an interrupted turn":           {Session{Phase: "interrupted"}, true, true, true},
		"a running turn":                {Session{Phase: "active"}, true, false, true},
		"a session that started again":  {Session{Phase: "started", Source: "resume"}, true, true, true},
		"a start after a compaction":    {Session{Phase: "started", Source: "compact"}, true, false, true},
		"a session untracked":           {Session{}, false, false, false},
		"a phase the board cannot read": {Session{Phase: "queued"}, true, false, false},
	} {
		got := turnState(tc.node, tc.isTracked, nil)
		if got.Ended != tc.ended || got.Known != tc.known {
			t.Fatalf("%s: %+v; want ended %t, known %t", name, got, tc.ended, tc.known)
		}
		if !got.Known && got.Why == "" {
			t.Fatalf("%s: an unknown turn says nothing of why", name)
		}
	}
}

// TestACompactionsStartLeavesItsTurnRunningUnlessHeAskedForIt is a session
// that reports its start after a compaction: one its harness made comes in the
// middle of a turn, which goes on, and one the Overlord asked for comes between
// turns, so nothing runs after it (#2293).
func TestACompactionsStartLeavesItsTurnRunningUnlessHeAskedForIt(t *testing.T) {
	asked := fleettree.Message{ID: "c0", Seq: 1, Kind: fleettree.MessageCompaction, Trigger: "manual"}
	for name, tc := range map[string]struct {
		after []fleettree.Message
		ended bool
	}{
		"a compaction the Overlord asked for":                         {nil, true},
		"a compaction the harness made":                               {[]fleettree.Message{{ID: "c1", Seq: 2, Kind: fleettree.MessageCompaction, Trigger: "auto"}}, false},
		"a compaction not written yet, a turn after one he asked for": {[]fleettree.Message{{ID: "p2", Seq: 2, Kind: fleettree.MessagePrompt}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			messages := append([]fleettree.Message{{ID: "p1", Seq: 0, Kind: fleettree.MessagePrompt}, asked}, tc.after...)

			// Act
			got := turnState(Session{Phase: "started", Source: "compact"}, true, messages)

			// Assert
			if !got.Known || got.Ended != tc.ended {
				t.Fatalf("%+v; want a known turn, ended %t", got, tc.ended)
			}
		})
	}
}

// TestAHarnesssOwnReplyEndsTheTurnItsReportIsOlderThan is the turn a usage
// limit or a failed request ended: Claude Code writes the reply itself and
// runs no hook, so the session's last report still says the turn runs. That
// report is of the turn only once it is newer than the reply, as the report of
// work that went on after it is.
func TestAHarnesssOwnReplyEndsTheTurnItsReportIsOlderThan(t *testing.T) {
	now := time.Now().UTC()
	messages := []fleettree.Message{
		{ID: "p1", Seq: 0, Kind: fleettree.MessagePrompt, At: now.Add(-time.Minute)},
		{ID: "n1", Seq: 1, Kind: fleettree.MessageReply, Model: "<synthetic>", At: now.Add(-30 * time.Second)},
	}
	for name, tc := range map[string]struct {
		reported time.Duration
		ended    bool
	}{
		"a report older than the reply":                 {-45 * time.Second, true},
		"a report of work that went on after the reply": {-10 * time.Second, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			got := turnState(Session{Phase: "active", UpdatedAt: now.Add(tc.reported)}, true, messages)

			// Assert
			if !got.Known || got.Ended != tc.ended {
				t.Fatalf("%+v; want a known turn, ended %t", got, tc.ended)
			}
		})
	}
}

func TestWhatTheStreamSaysOfATurn(t *testing.T) {
	prompts := []fleettree.Message{{ID: "p1", Seq: 0, Kind: fleettree.MessagePrompt, Text: "go"}, {ID: "r1", Seq: 1, Kind: fleettree.MessageReply}}
	// The replies of a turn that began more than a page back.
	replies := []fleettree.Message{{ID: "r9", Seq: 9, Kind: fleettree.MessageReply}, {ID: "r10", Seq: 10, Kind: fleettree.MessageReply}}
	for name, tc := range map[string]struct {
		messages []fleettree.Message
		events   []sessionstream.Event
		turn     string
		state    string
	}{
		"a turn still running":                 {prompts, nil, "p1", "running"},
		"a turn that ended":                    {prompts, []sessionstream.Event{{Kind: "turn_end", Turn: "p1"}}, "p1", "ended"},
		"a turn never reported":                {prompts, []sessionstream.Event{{Kind: "missing", Turn: "p1", Missing: "no hooks"}}, "p1", "unknown"},
		"a report that says it runs":           {prompts, []sessionstream.Event{{Kind: "missing", Turn: "p1", Missing: "no hooks"}, {Kind: "turn_state", Turn: "p1"}}, "p1", "running"},
		"a session with no turn":               {nil, nil, "", "none"},
		"a session that holds no message":      {nil, []sessionstream.Event{{Kind: "turn_state"}}, "", "none"},
		"an earlier turn's end":                {prompts, []sessionstream.Event{{Kind: "turn_end", Turn: "p0"}}, "p1", "running"},
		"a turn begun off the page that runs":  {replies, []sessionstream.Event{{Kind: "turn_state"}}, "", "running"},
		"a turn begun off the page that ended": {replies, []sessionstream.Event{{Kind: "turn_state"}, {Kind: "turn_end"}}, "", "ended"},
		"a turn begun off the page unreported": {replies, nil, "", "none"},
	} {
		turn, got, why := streamTurn(tc.messages, tc.events)
		if got != tc.state || turn != tc.turn {
			t.Fatalf("%s: turn %q is %q; want turn %q %q", name, turn, got, tc.turn, tc.state)
		}
		if got == "unknown" && why == "" {
			t.Fatalf("%s says nothing of why the turn is unknown", name)
		}
	}
}

func TestThePageTheBoardDrawsIsTheStreams(t *testing.T) {
	// Arrange
	transcript := fleettree.MessagePage{Conversation: "conv-1", More: false, Messages: []fleettree.Message{{ID: "t1", Seq: 0, Kind: fleettree.MessagePrompt}}}
	projection := []fleettree.Message{{ID: "s1", Seq: 7, Kind: fleettree.MessagePrompt}, {ID: "s2", Seq: 9, Kind: fleettree.MessageReply}}

	// Act
	page := pageOfStream(transcript, projection, -1)
	older := pageOfStream(transcript, projection, 9)

	// Assert
	if len(page.Messages) != 2 || page.Messages[0].ID != "s1" || page.Conversation != "conv-1" {
		t.Fatalf("the page of the stream: %+v", page)
	}
	if len(older.Messages) != 1 || older.Messages[0].ID != "s1" {
		t.Fatalf("the page before a message: %+v", older)
	}
}

func TestAPageTheStreamDoesNotHoldIsTheRecords(t *testing.T) {
	// Arrange
	transcript := fleettree.MessagePage{Conversation: "conv-1", Messages: []fleettree.Message{{ID: "t1", Seq: 0, Kind: fleettree.MessagePrompt}}}
	projection := []fleettree.Message{{ID: "s2", Seq: 9, Kind: fleettree.MessageReply}}

	// Act: an older page the stream began after, and a session with no stream.
	older := pageOfStream(transcript, projection, 9)
	none := pageOfStream(transcript, nil, -1)

	// Assert
	if len(older.Messages) != 1 || older.Messages[0].ID != "t1" {
		t.Fatalf("an older page the stream does not hold: %+v", older)
	}
	if len(none.Messages) != 1 || none.Messages[0].ID != "t1" {
		t.Fatalf("a session with no stream: %+v", none)
	}
}

func TestTheChangesAViewIsToldAreTheStreams(t *testing.T) {
	// Arrange
	changed := []fleettree.Message{{ID: "r1", Seq: 1, Blocks: []fleettree.Block{{Kind: fleettree.BlockCall, State: fleettree.CallRunning}}}}
	projection := []fleettree.Message{{ID: "r1", Seq: 1, Blocks: []fleettree.Block{{Kind: fleettree.BlockCall, State: fleettree.CallDone, Result: "done"}}}}

	// Act
	kept := changedOfStream(changed, projection)

	// Assert
	if len(kept) != 1 || kept[0].Blocks[0].State != fleettree.CallDone {
		t.Fatalf("a changed message the stream holds: %+v", kept)
	}
}

// TestAReadThatOnlyCarriesOnTheStreamIsNoRestatement keeps an ordinary read of
// newer messages from being taken for a harness that rewrote its record.
func TestAReadThatOnlyCarriesOnTheStreamIsNoRestatement(t *testing.T) {
	held := []fleettree.Message{{ID: "u1", Seq: 2}, {ID: "a1", Seq: 3}, {ID: "u2", Seq: 4}, {ID: "a2", Seq: 5}}
	for name, tc := range map[string]struct {
		read []fleettree.Message
		want bool
	}{
		"a read past the stream":   {[]fleettree.Message{{ID: "u1", Seq: 2}, {ID: "a1", Seq: 3}, {ID: "u2", Seq: 4}, {ID: "a2", Seq: 5}, {ID: "u3", Seq: 6}}, false},
		"a read of what it holds":  {slices.Clone(held), false},
		"a read that begins later": {[]fleettree.Message{{ID: "u2", Seq: 4}, {ID: "a2", Seq: 5}}, false},
		// The newest page once the session wrote more than a page: it begins
		// past the stream's oldest message and ends past its newest.
		"a page that slid on past the stream": {[]fleettree.Message{{ID: "u2", Seq: 4}, {ID: "a2", Seq: 5}, {ID: "u3", Seq: 6}, {ID: "a3", Seq: 7}}, false},
		"a stream that began after the read":  {[]fleettree.Message{{ID: "u0", Seq: 0}, {ID: "a0", Seq: 1}, {ID: "u1", Seq: 2}, {ID: "a1", Seq: 3}, {ID: "u2", Seq: 4}, {ID: "a2", Seq: 5}}, false},
		"a read of other messages":            {[]fleettree.Message{{ID: "u1", Seq: 2}, {ID: "u9", Seq: 3}}, true},
		"a read that dropped the newest":      {[]fleettree.Message{{ID: "u1", Seq: 2}, {ID: "a1", Seq: 3}, {ID: "u2", Seq: 4}}, true},
		"a read that shares nothing with it":  {[]fleettree.Message{{ID: "u7", Seq: 14}, {ID: "a7", Seq: 15}}, true},
		"a read of an empty record":           {nil, false},
		"a stream that holds nothing":         {[]fleettree.Message{{ID: "u1", Seq: 0}}, false},
	} {
		held := held
		if name == "a stream that holds nothing" {
			held = nil
		}
		if got := streamDiverges(held, tc.read); got != tc.want {
			t.Fatalf("%s: %t; want %t", name, got, tc.want)
		}
	}
}

// TestAReplyReadAgainAfterARestatementIsDrawnWhole is a reply the stream held
// while its call ran, a restatement that does not hold it, as a view that
// reopens past a gap writes, and the older page read again once the call
// finished: the reply is written whole after the restatement, by the
// supervisor that wrote it and by one started after, so the board draws the
// reply rather than its result beside empty blocks.
func TestAReplyReadAgainAfterARestatementIsDrawnWhole(t *testing.T) {
	for name, isRestarted := range map[string]bool{"the supervisor that restated it": false, "a supervisor started after": true} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			id := sessionstream.Identity{Task: "talker", Generation: "s1", Harness: "claude", Session: "talker-conversation"}
			held, err := (&Service{Store: store}).sessionStreamOf(id)
			if err != nil {
				t.Fatal(err)
			}
			write := func(held *heldStream, events []sessionstream.Event) []fleettree.Message {
				t.Helper()
				projection, err := held.record(func([]fleettree.Message, []sessionstream.Event) []sessionstream.Event { return events })
				if err != nil {
					t.Fatal(err)
				}
				return projection
			}
			at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
			prompt := fleettree.Message{ID: "u1", Seq: 0, Kind: fleettree.MessagePrompt, At: at, Source: fleettree.FromOverlord, Text: "Read b.go"}
			reply := func(state, result string) fleettree.Message {
				return fleettree.Message{ID: "msg_1", Seq: 1, Kind: fleettree.MessageReply, At: at, Model: "claude-opus-5-5", Blocks: []fleettree.Block{
					{Kind: fleettree.BlockText, Text: "Reading b.go."},
					{Kind: fleettree.BlockCall, CallID: "toolu_1", Tool: "Read", Subject: "b.go", State: state, Result: result},
				}}
			}
			write(held, fleettree.StreamEvents(id, []fleettree.Message{prompt, reply(fleettree.CallRunning, "")}))
			write(held, []sessionstream.Event{fleettree.RestatedEvent(id, []fleettree.Message{{ID: "u9", Seq: 90, Kind: fleettree.MessagePrompt, At: at, Source: fleettree.FromOverlord, Text: "Later"}})})
			if isRestarted {
				if held, err = (&Service{Store: store}).sessionStreamOf(id); err != nil {
					t.Fatal(err)
				}
			}
			finished := reply(fleettree.CallDone, "package b")

			// Act
			projection := write(held, fleettree.StreamEvents(id, []fleettree.Message{prompt, finished}))

			// Assert
			drawn := slices.IndexFunc(projection, func(message fleettree.Message) bool { return message.ID == "msg_1" })
			if drawn < 0 || !reflect.DeepEqual(projection[drawn], finished) {
				t.Fatalf("the board draws %+v; want the reply whole: %+v", projection, finished)
			}
		})
	}
}

// TestAStreamWhoseWriteFailedIsReadAgainFromItsFile is a write the stream's
// file refused, after which the file holds an entry this supervisor never saw
// land, as an append whose cut back failed too leaves one: the next write reads
// the stream again from its file, so the entry is neither written twice nor
// missing from what the board draws.
func TestAStreamWhoseWriteFailedIsReadAgainFromItsFile(t *testing.T) {
	// Arrange: a stream that holds a session's first prompt.
	store, _ := testStore(t)
	service := &Service{Store: store}
	id := sessionstream.Identity{Task: "talker", Generation: "s1", Harness: "claude", Session: "talker-conversation"}
	held, err := service.sessionStreamOf(id)
	if err != nil {
		t.Fatal(err)
	}
	write := func(events []sessionstream.Event) ([]fleettree.Message, error) {
		return held.record(func([]fleettree.Message, []sessionstream.Event) []sessionstream.Event { return events })
	}
	if _, err := write(fleettree.StreamEvents(id, []fleettree.Message{{ID: "u1", Seq: 0, Kind: fleettree.MessagePrompt, Text: "Why is the card red?"}})); err != nil {
		t.Fatal(err)
	}
	path, err := id.Path(store.Home.State)
	if err != nil {
		t.Fatal(err)
	}
	second := fleettree.StreamEvents(id, []fleettree.Message{{ID: "u2", Seq: 1, Kind: fleettree.MessagePrompt, Text: "And now?"}})
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) })
	if _, err := write(second); err == nil {
		t.Fatal("a write to a stream whose file refuses it succeeded")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	landed := slices.Clone(second)
	landed[0].Seq = 1
	appendEntries(t, path, strings.TrimSuffix(string(sessionstream.Encode(landed)), "\n"))

	// Act
	projection, err := write(second)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	events, err := sessionstream.Read(store.Home.State, id)
	if err != nil {
		t.Fatal(err)
	}
	written := 0
	for _, event := range events {
		if event.ID == second[0].ID {
			written++
		}
	}
	if written != 1 {
		t.Fatalf("the stream's file holds the entry %d times; want once", written)
	}
	drawn := 0
	for _, message := range projection {
		if message.ID == "u2" {
			drawn++
		}
	}
	if drawn != 1 {
		t.Fatalf("the board draws the entry %d times: %+v; want once", drawn, projection)
	}
}

// appendEntries appends Claude Code entries to the conversation at path, as
// Claude Code appends them.
func appendEntries(t *testing.T, path string, entries ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(entries, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}
