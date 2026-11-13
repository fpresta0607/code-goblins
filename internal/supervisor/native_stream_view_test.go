package supervisor

import (
	"testing"

	"github.com/fpresta0607/code-goblins/internal/claudestream"
)

func TestAStreamSessionsScreenSaysWhatItsTurnDoesAndWhetherAToolCallWaits(t *testing.T) {
	cases := []struct {
		name    string
		session claudestream.Session
		want    string
	}{
		{"idle", claudestream.Session{}, `{"type":"screen","asking":false,"open":false,"working":false,"submission":""}`},
		{"working before any words", claudestream.Session{IsWorking: true}, `{"type":"screen","asking":false,"open":false,"working":true,"submission":""}`},
		{"thinking", claudestream.Session{IsWorking: true, Live: []claudestream.LiveBlock{{Kind: "thinking", Text: "hm"}}}, `{"type":"screen","asking":false,"open":false,"working":true,"submission":"","doing":"Thinking"}`},
		{"writing after thinking", claudestream.Session{IsWorking: true, Live: []claudestream.LiveBlock{{Kind: "thinking"}, {Kind: "text", Text: "On it"}}}, `{"type":"screen","asking":false,"open":false,"working":true,"submission":"","doing":"Writing"}`},
		{"a tool call waits", claudestream.Session{IsWorking: true, Asks: []claudestream.Ask{{RequestID: "r1", Tool: "Bash"}}}, `{"type":"screen","asking":true,"open":false,"working":true,"submission":""}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := string(streamScreen(test.session))

			// Assert
			if got != test.want {
				t.Errorf("the screen report is %s, want %s", got, test.want)
			}
		})
	}
}

func TestAStreamSessionIsDrawnUnderItsHeaderWithoutTheControlCharactersItsWordsHold(t *testing.T) {
	// Arrange
	rows := []string{"working", "\x1b[31mred\x07 words\tand a tab"}

	// Act
	got := string(streamDrawing("Zeke runs Claude Code on stream-json pipes.", rows))

	// Assert
	want := "\x1b[H\x1b[2J\x1b[3JZeke runs Claude Code on stream-json pipes.\r\n\r\nworking\r\n[31mred words\tand a tab"
	if got != want {
		t.Errorf("the drawing is %q, want %q", got, want)
	}
}
