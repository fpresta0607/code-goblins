package supervisor

import (
	"encoding/json"
	"strings"
	"testing"
)

// A message too long for the board, as a long paste into the chat's box was
// before the box kept one as a file, is refused in plain words that name the
// limit it passed, never "Invalid action JSON" or "action exceeds size
// limit", and nothing is queued (#2404).
func TestAnActionTooLongForTheBoardIsRefusedInPlainWordsThatNameItsLimit(t *testing.T) {
	for _, c := range []struct {
		what, text, want string
		code             int
	}{
		{"a body over the 24 KiB the board reads", strings.Repeat("x", 30000), "It is longer than the 24 KiB the board takes in one go.", 413},
		{"text over 16,000 bytes in a body the board reads", strings.Repeat("x", 16001), "Its text is longer than the 16,000 bytes the board takes in one go.", 409},
	} {
		t.Run(c.what, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			handler := NewHTTP(&Service{Store: store, Instance: "instance-1"}, "board.local", nil)
			body, err := json.Marshal(map[string]string{"id": "long-1", "kind": "message", "text": c.text})
			if err != nil {
				t.Fatal(err)
			}

			// Act
			response := boardRequest(handler, "POST", "/api/actions", string(body))

			// Assert
			var refusal struct{ Error string }
			if err := json.Unmarshal(response.Body.Bytes(), &refusal); err != nil || response.Code != c.code || refusal.Error != c.want {
				t.Fatalf("answered %d %s, want %d %q", response.Code, response.Body, c.code, c.want)
			}
			if queued := store.Snapshot().Actions; len(queued) != 0 {
				t.Fatalf("queued %d actions, want none", len(queued))
			}
		})
	}
}

// The limits stay where they were for every kind of action: 16,000 bytes of
// text pass, and a byte more is refused in the same plain words (#2404).
func TestTheTextLimitStaysAt16000BytesForEveryKindOfAction(t *testing.T) {
	const tooLong = "Its text is longer than the 16,000 bytes the board takes in one go."
	for _, c := range []struct {
		what   string
		action Action
	}{
		{"his message to the CEO", Action{Kind: "message"}},
		{"his message to a goblin", Action{Kind: "message", TaskID: "task-1"}},
		{"his answer to a question", Action{Kind: "ceo_answer", Generation: "g1", QuestionID: "q1"}},
	} {
		for _, size := range []int{16000, 16001} {
			t.Run(c.what, func(t *testing.T) {
				// Arrange
				store, _ := testStore(t)
				action := c.action
				action.ID, action.Text = "long-1", strings.Repeat("x", size)

				// Act
				_, err := store.Queue(action)

				// Assert
				if isRefusedForItsSize := err != nil && err.Error() == tooLong; isRefusedForItsSize != (size > 16000) {
					t.Fatalf("%d bytes: refused with %v, want refused for its size only past 16,000", size, err)
				}
			})
		}
	}
}
