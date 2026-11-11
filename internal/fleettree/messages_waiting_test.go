package fleettree

import (
	"reflect"
	"testing"
	"time"
)

// dequeued is Claude Code taking the next prompt of its queue for a turn, which
// its record writes with no words, and absorbed it taking one into the turn
// already running, which names the words.
func dequeued() object {
	return object{"type": "queue-operation", "operation": "dequeue", "timestamp": at.Format(time.RFC3339Nano)}
}

func absorbed(text string) object {
	return object{"type": "queue-operation", "operation": "remove", "reason": "absorbed_mid_turn", "content": text, "timestamp": at.Format(time.RFC3339Nano)}
}

func waitingWords(waiting []WaitingPrompt) []string {
	words := []string{}
	for _, prompt := range waiting {
		words = append(words, prompt.Text)
	}
	return words
}

func promptWords(messages []Message) []string {
	words := []string{}
	for _, message := range messages {
		if message.Kind == MessagePrompt {
			words = append(words, message.Text)
		}
	}
	return words
}

func TestAPromptClaudeCodeHoldsWaitsInThePageUntilItIsDelivered(t *testing.T) {
	tests := []struct {
		name    string
		entries []any
		waiting []string
		prompts []string
	}{
		{
			name:    "from the moment it is queued, in order",
			entries: []any{prompted("u1", "start"), queued("check the phone too", at), queued("and the tablet", at)},
			waiting: []string{"check the phone too", "and the tablet"},
			prompts: []string{"start"},
		},
		{
			name:    "a dequeue hands the oldest over to the turn it starts",
			entries: []any{queued("first", at), queued("second", at), dequeued(), prompted("u1", "first")},
			waiting: []string{"second"},
			prompts: []string{"first"},
		},
		{
			name: "a remove hands the one it names to the turn already running",
			entries: []any{
				queued("first", at), queued("second", at), absorbed("second"),
				attachment("q1", object{"type": "queued_command", "commandMode": "prompt", "prompt": "second"}),
			},
			waiting: []string{"first"},
			prompts: []string{"second"},
		},
		{
			// Claude Code queues a ! command typed during a turn by its words
			// alone and runs it at the turn's end as a shell command (#2242).
			name:    "a shell command queued by its words is delivered by the row that runs it",
			entries: []any{queued("claude doctor", at), dequeued(), prompted("u1", "<bash-input>claude doctor</bash-input>")},
			waiting: []string{},
			prompts: []string{"claude doctor"},
		},
		{
			name:    "two of the same words each wait for their own delivery",
			entries: []any{queued("again", at), queued("again", at), dequeued(), prompted("u1", "again")},
			waiting: []string{"again"},
			prompts: []string{"again"},
		},
		{
			name:    "compaction leaves a waiting prompt queued until the harness takes it",
			entries: []any{queued("after compaction", at), system("c1", "compact_boundary", object{"compactMetadata": object{"trigger": "auto"}})},
			waiting: []string{"after compaction"},
			prompts: []string{},
		},
		{
			name:    "a prompt queued during compaction is delivered once after it",
			entries: []any{system("c1", "compact_boundary", object{"compactMetadata": object{"trigger": "auto"}}), queued("after compaction", at), dequeued(), prompted("u1", "after compaction")},
			waiting: []string{},
			prompts: []string{"after compaction"},
		},
		{
			name:    "the fleet's notices are never his waiting words",
			entries: []any{queued(notification("b1", "toolu_9", "completed", "The suite passed", ""), at), queued(WakePrefix+"look at the board", at)},
			waiting: []string{},
			prompts: []string{},
		},
		{
			name:    "a dequeue that finds only a notice waiting takes no prompt",
			entries: []any{queued(notification("b1", "toolu_9", "completed", "The suite passed", ""), at), dequeued(), queued("hold this", at)},
			waiting: []string{"hold this"},
			prompts: []string{},
		},
		{
			name:    "a hidden notice retains its FIFO slot ahead of a prompt",
			entries: []any{queued(WakePrefix+"look at the board", at), queued("hold this", at), dequeued()},
			waiting: []string{"hold this"},
			prompts: []string{},
		},
		{
			name:    "a named notice removal leaves the prompt for its own dequeue",
			entries: []any{queued(WakePrefix+"look at the board", at), queued("hold this", at), absorbed(WakePrefix + "look at the board"), dequeued(), prompted("u1", "hold this")},
			waiting: []string{},
			prompts: []string{"hold this"},
		},
		{
			name:    "an enqueue without text retains its FIFO slot",
			entries: []any{queued("", at), queued("hold this", at), dequeued()},
			waiting: []string{"hold this"},
			prompts: []string{},
		},
		{
			name:    "a delivered prompt ends only its own wait, past an entry its harness lost",
			entries: []any{queued("lost with the harness", at), queued("sent after the resume", at), dequeued(), prompted("u1", "sent after the resume")},
			waiting: []string{"lost with the harness"},
			prompts: []string{"sent after the resume"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			path := conversation(t, t.TempDir(), test.entries...)

			// Act: a board that reconnects reads the record over, so each
			// read of it gives the same.
			var reader, reconnected Reader
			page, _, err := reader.MessagePage(path, -1)
			if err != nil {
				t.Fatal(err)
			}
			again, _, err := reconnected.MessagePage(path, -1)
			if err != nil {
				t.Fatal(err)
			}

			// Assert
			if got := waitingWords(page.Waiting); !reflect.DeepEqual(got, test.waiting) {
				t.Fatalf("waiting = %q; want %q", got, test.waiting)
			}
			if got := promptWords(page.Messages); !reflect.DeepEqual(got, test.prompts) {
				t.Fatalf("prompts = %q; want %q", got, test.prompts)
			}
			if !reflect.DeepEqual(page.Waiting, again.Waiting) {
				t.Fatalf("a second read waits on %+v; the first on %+v", again.Waiting, page.Waiting)
			}
		})
	}
}

func TestAWaitingPromptReadsAsItsMessageWill(t *testing.T) {
	// Arrange
	path := conversation(t, t.TempDir(), queued(`CFO: also run the specs <pasted_content id="07">now</pasted_content id="07">`, at))

	// Act
	var reader Reader
	page, _, err := reader.MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(page.Waiting) != 1 {
		t.Fatalf("waiting = %+v; want one prompt", page.Waiting)
	}
	prompt := page.Waiting[0]
	if prompt.ID == "" || !prompt.At.Equal(at) || prompt.Source != FromCFO || prompt.Text != "also run the specs now" {
		t.Fatalf("waiting prompt = %+v", prompt)
	}
}

func TestMessagesSinceFollowsTheQueueBetweenPages(t *testing.T) {
	// Arrange
	path := conversation(t, t.TempDir(), prompted("u1", "start"))
	var reader Reader
	_, cursor, err := reader.MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}

	// Act: queued, which changes no message.
	appendLines(t, path, queued("hold this", at))
	changed, waiting, cursor, isAnew, err := reader.MessagesSince(path, cursor)
	if err != nil || isAnew || len(changed) != 0 {
		t.Fatalf("after the queue: %d changed, anew %t, error %v", len(changed), isAnew, err)
	}

	// Assert
	if got := waitingWords(waiting); !reflect.DeepEqual(got, []string{"hold this"}) {
		t.Fatalf("waiting = %q; want the queued prompt", got)
	}

	// Act: delivered.
	appendLines(t, path, dequeued(), stamped(prompted("u2", "hold this")))
	changed, waiting, cursor, isAnew, err = reader.MessagesSince(path, cursor)
	if err != nil || isAnew {
		t.Fatalf("after delivery: anew %t, error %v", isAnew, err)
	}

	// Assert: the row and the end of its wait come in the one read, so the
	// board never draws it twice.
	if got := promptWords(changed); !reflect.DeepEqual(got, []string{"hold this"}) {
		t.Fatalf("changed prompts = %q; want the delivered one", got)
	}
	if got := waiting; len(got) != 0 {
		t.Fatalf("waiting = %+v; want nothing once it is delivered", got)
	}
}

func TestQueueMatchingPreservesPromptProvenance(t *testing.T) {
	for _, test := range []struct {
		name    string
		entries []any
		sources []string
	}{
		{"named removal", []any{absorbed("CFO: continue"), prompted("u1", "CFO: continue")}, []string{FromOverlord, FromCFO}},
		{"delivery without removal", []any{prompted("u1", "CFO: continue")}, []string{FromOverlord, FromCFO}},
		{"handoff with different provenance", []any{dequeued(), prompted("u1", "CFO: continue")}, []string{FromOverlord, FromCFO}},
		{"Overlord removal preserves CFO duplicates", []any{absorbed("continue"), prompted("u1", "continue")}, []string{FromCFO, FromCFO}},
	} {
		t.Run(test.name, func(t *testing.T) {
			entries := []any{queued("continue", at), queued("CFO: continue", at), queued("CFO: continue", at)}
			path := conversation(t, t.TempDir(), append(entries, test.entries...)...)
			var reader Reader
			page, _, err := reader.MessagePage(path, -1)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Waiting) != len(test.sources) {
				t.Fatalf("waiting = %+v; want sources %q", page.Waiting, test.sources)
			}
			for index, source := range test.sources {
				if page.Waiting[index].Source != source || page.Waiting[index].Text != "continue" {
					t.Fatalf("waiting = %+v; want sources %q with unchanged words", page.Waiting, test.sources)
				}
			}
		})
	}
}

func TestMessagesSinceKeepsEachStreamsQueueSnapshot(t *testing.T) {
	path := conversation(t, t.TempDir(), queued("hold this", at))
	var reader Reader
	_, cursor, err := reader.MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}
	first, firstWaiting, firstCursor, isAnew, err := reader.MessagesSince(path, cursor)
	if err != nil || isAnew || len(first) != 0 {
		t.Fatalf("first stream: messages %+v, anew %t, error %v", first, isAnew, err)
	}
	appendLines(t, path, dequeued(), stamped(prompted("u1", "hold this")))
	second, secondWaiting, _, isAnew, err := reader.MessagesSince(path, cursor)
	if err != nil || isAnew || len(second) != 1 || len(secondWaiting) != 0 {
		t.Fatalf("second stream: messages %+v, waiting %+v, anew %t, error %v", second, secondWaiting, isAnew, err)
	}
	if got := waitingWords(firstWaiting); !reflect.DeepEqual(got, []string{"hold this"}) {
		t.Fatalf("first stream lost its queued prompt before receiving delivery: %q", got)
	}
	first, firstWaiting, _, isAnew, err = reader.MessagesSince(path, firstCursor)
	if err != nil || isAnew || len(first) != 1 || first[0].Text != "hold this" || len(firstWaiting) != 0 {
		t.Fatalf("first stream delivery: messages %+v, waiting %+v, anew %t, error %v", first, firstWaiting, isAnew, err)
	}
}

func TestQueueHandoffStaysVisibleUntilItsDeliveredRow(t *testing.T) {
	for _, operation := range []struct {
		name  string
		entry object
	}{
		{"dequeue", dequeued()},
		{"remove", absorbed("continue")},
	} {
		t.Run(operation.name, func(t *testing.T) {
			path := conversation(t, t.TempDir(), queued("continue", at), queued("continue", at))
			var reader Reader
			page, cursor, err := reader.MessagePage(path, -1)
			if err != nil {
				t.Fatal(err)
			}
			firstID, secondID := page.Waiting[0].ID, page.Waiting[1].ID
			appendLines(t, path, operation.entry)
			changed, waiting, cursor, isAnew, err := reader.MessagesSince(path, cursor)
			if err != nil || isAnew || len(changed) != 0 || len(waiting) != 2 {
				t.Fatalf("handoff: changed %+v, waiting %+v, anew %t, error %v", changed, waiting, isAnew, err)
			}
			if waiting[0].ID != firstID || !waiting[0].IsSending || waiting[1].ID != secondID || waiting[1].IsSending {
				t.Fatalf("handoff lost identity or state: %+v", waiting)
			}
			var reconnected Reader
			page, _, err = reconnected.MessagePage(path, -1)
			if err != nil || !reflect.DeepEqual(page.Waiting, waiting) {
				t.Fatalf("reconnected handoff: %+v, error %v", page.Waiting, err)
			}
			appendLines(t, path, stamped(prompted("u1", "continue")))
			changed, waiting, _, isAnew, err = reader.MessagesSince(path, cursor)
			if err != nil || isAnew || len(changed) != 1 || changed[0].Text != "continue" || len(waiting) != 1 {
				t.Fatalf("delivery: changed %+v, waiting %+v, anew %t, error %v", changed, waiting, isAnew, err)
			}
			if waiting[0].ID != secondID || waiting[0].IsSending {
				t.Fatalf("delivery consumed its sibling: %+v", waiting)
			}
		})
	}
}

// continued is Claude Code carrying a session on by itself once its usage limit
// has reset, as 2.1.322 recorded it in a goblin on 2026-11-10: it queues its
// own words and takes them off the queue as it does his, then records them
// for its model alone, which shows nothing.
func continued(text string) []any {
	return []any{
		queued(text, at), dequeued(),
		stamped(object{"type": "user", "uuid": "m1", "isMeta": true, "promptSource": "system", "origin": object{"kind": "auto-continuation"}, "message": object{"role": "user", "content": text}}),
	}
}

func TestAPromptStopsSendingOnceTheSessionAnswersAfterItsHandoff(t *testing.T) {
	const reset = "Your claude.ai usage limit has reset. Continue the task you were working on when the limit was reached; do not repeat work that is already complete."
	later := at.Add(2 * time.Second)
	tests := []struct {
		name    string
		entries []any
		sending []string
		queued  []string
		prompts []string
	}{
		{
			name:    "words Claude Code queued itself and recorded for its model alone",
			entries: append(continued(reset), said("Carrying on with the reload.", later)),
		},
		{
			name:    "words its harness lost, handed over in the place of the ones after them",
			entries: []any{queued("lost with the harness", at), queued("sent after the resume", at), dequeued(), prompted("u1", "sent after the resume"), said("On it.", later)},
			prompts: []string{"sent after the resume"},
		},
		{
			name:    "words absorbed into a running turn whose record names others",
			entries: []any{queued("check the phone too", at), absorbed("check the phone too"), said("Checking both.", later)},
		},
		{
			// A notice Claude Code writes itself is stamped before the handoff
			// and recorded after it, as its usage limit's was.
			name:    "an answer written before the handoff and recorded after it ends nothing",
			entries: []any{queued("hold this", at), dequeued(), said("You've hit your weekly limit", at.Add(-time.Second))},
			sending: []string{"hold this"},
		},
		{
			name:    "a prompt still queued waits through an answer",
			entries: []any{queued("hold this", at), said("Still working.", later)},
			queued:  []string{"hold this"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			path := conversation(t, t.TempDir(), test.entries...)

			// Act
			var reader Reader
			page, _, err := reader.MessagePage(path, -1)
			if err != nil {
				t.Fatal(err)
			}

			// Assert
			sending, queued := []string{}, []string{}
			for _, prompt := range page.Waiting {
				if prompt.IsSending {
					sending = append(sending, prompt.Text)
				} else {
					queued = append(queued, prompt.Text)
				}
			}
			if !reflect.DeepEqual(sending, append([]string{}, test.sending...)) || !reflect.DeepEqual(queued, append([]string{}, test.queued...)) {
				t.Fatalf("sending = %q and queued = %q; want %q and %q", sending, queued, test.sending, test.queued)
			}
			if got := promptWords(page.Messages); !reflect.DeepEqual(got, append([]string{}, test.prompts...)) {
				t.Fatalf("prompts = %q; want %q", got, test.prompts)
			}
		})
	}
}

// A stream that read the handoff hears it end with the answer, in the one
// read, so a bubble that reads Sending never stays past it.
func TestMessagesSinceEndsAHandoffWithTheAnswerAfterIt(t *testing.T) {
	// Arrange
	const reset = "Your claude.ai usage limit has reset. Continue the task you were working on when the limit was reached; do not repeat work that is already complete."
	path := conversation(t, t.TempDir(), append([]any{prompted("u1", "start")}, continued(reset)...)...)
	var reader Reader
	page, cursor, err := reader.MessagePage(path, -1)
	if err != nil || len(page.Waiting) != 1 || !page.Waiting[0].IsSending {
		t.Fatalf("handed over: waiting %+v, error %v; want the one prompt sending", page.Waiting, err)
	}

	// Act
	appendLines(t, path, said("Carrying on with the reload.", at.Add(2*time.Second)))
	changed, waiting, _, isAnew, err := reader.MessagesSince(path, cursor)

	// Assert
	if err != nil || isAnew || len(changed) != 1 || changed[0].Kind != MessageReply || len(waiting) != 0 {
		t.Fatalf("answered: changed %+v, waiting %+v, anew %t, error %v; want the answer and nothing waiting", changed, waiting, isAnew, err)
	}
}

func TestSuccessiveQueueHandoffsKeepTheirOwnDelivery(t *testing.T) {
	path := conversation(t, t.TempDir(), queued("continue", at), queued(WakePrefix+"look at the board", at), queued("CFO: continue", at), dequeued(), dequeued(), dequeued())
	var reader Reader
	page, cursor, err := reader.MessagePage(path, -1)
	if err != nil || len(page.Waiting) != 2 || !page.Waiting[0].IsSending || !page.Waiting[1].IsSending {
		t.Fatalf("handoffs: waiting %+v, error %v", page.Waiting, err)
	}
	appendLines(t, path, prompted("u1", "CFO: continue"))
	changed, waiting, cursor, isAnew, err := reader.MessagesSince(path, cursor)
	if err != nil || isAnew || len(changed) != 1 || len(waiting) != 1 || waiting[0].Source != FromOverlord || !waiting[0].IsSending {
		t.Fatalf("CFO delivery: changed %+v, waiting %+v, anew %t, error %v", changed, waiting, isAnew, err)
	}
	appendLines(t, path, prompted("u2", "continue"))
	changed, waiting, _, isAnew, err = reader.MessagesSince(path, cursor)
	if err != nil || isAnew || len(changed) != 1 || changed[0].Source != FromOverlord || len(waiting) != 0 {
		t.Fatalf("Overlord delivery: changed %+v, waiting %+v, anew %t, error %v", changed, waiting, isAnew, err)
	}
}
