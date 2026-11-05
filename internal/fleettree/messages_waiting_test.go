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
			name:    "two of the same words each wait for their own delivery",
			entries: []any{queued("again", at), queued("again", at), dequeued(), prompted("u1", "again")},
			waiting: []string{"again"},
			prompts: []string{"again"},
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
			// A harness that dies leaves what it held queued in its record
			// for good, one ahead of the entries of the session resumed after
			// it, so the words that reach the record end their own wait.
			name:    "a prompt that reaches the record no longer waits, past an entry its harness lost",
			entries: []any{queued("lost with the harness", at), queued("sent after the resume", at), dequeued(), prompted("u1", "sent after the resume")},
			waiting: []string{},
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

func TestWaitingAtFollowsTheQueueBetweenPages(t *testing.T) {
	// Arrange
	path := conversation(t, t.TempDir(), prompted("u1", "start"))
	var reader Reader
	_, cursor, err := reader.MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}

	// Act: queued, which changes no message.
	appendLines(t, path, queued("hold this", at))
	changed, cursor, isAnew, err := reader.MessagesSince(path, cursor)
	if err != nil || isAnew || len(changed) != 0 {
		t.Fatalf("after the queue: %d changed, anew %t, error %v", len(changed), isAnew, err)
	}

	// Assert
	if got := waitingWords(reader.WaitingAt(path, cursor)); !reflect.DeepEqual(got, []string{"hold this"}) {
		t.Fatalf("waiting = %q; want the queued prompt", got)
	}

	// Act: delivered.
	appendLines(t, path, dequeued(), stamped(prompted("u2", "hold this")))
	changed, cursor, isAnew, err = reader.MessagesSince(path, cursor)
	if err != nil || isAnew {
		t.Fatalf("after delivery: anew %t, error %v", isAnew, err)
	}

	// Assert: the row and the end of its wait come in the one read, so the
	// board never draws it twice.
	if got := promptWords(changed); !reflect.DeepEqual(got, []string{"hold this"}) {
		t.Fatalf("changed prompts = %q; want the delivered one", got)
	}
	if got := reader.WaitingAt(path, cursor); len(got) != 0 {
		t.Fatalf("waiting = %+v; want nothing once it is delivered", got)
	}
}

func TestWaitingAtIsEmptyForACursorOfAnotherRead(t *testing.T) {
	// Arrange
	path := conversation(t, t.TempDir(), queued("hold this", at))
	var reader Reader
	if _, _, err := reader.MessagePage(path, -1); err != nil {
		t.Fatal(err)
	}

	// Act
	got := reader.WaitingAt(path, MessageCursor{})

	// Assert
	if got != nil {
		t.Fatalf("waiting = %+v; want nothing for a read this log is not", got)
	}
}
