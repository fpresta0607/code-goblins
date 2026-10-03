package supervisor

import (
	"strings"
	"testing"
)

// A conversation the CFO could not resume when it came back stays on the
// board, apart from any registration problem, with the command that opens it
// by hand, until the CFO next comes back on its conversation.
func TestTheBoardNamesTheConversationTheCFOCouldNotResume(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	service := &Service{Store: store, Instance: "test-instance"}
	left := CFOConversationLeft{Harness: "claude", Session: "a1b2c3d4-session", Resume: []string{"--resume", "a1b2c3d4-session"}}
	if err := RecordCFOConversationLeft(h.State, left); err != nil {
		t.Fatal(err)
	}

	// Act
	shown, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	ClearCFOConversationLeft(h.State)
	cleared, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	for _, want := range []string{"conversation a1b2c3d4-session could not be resumed", "started on a new one", "claude --resume a1b2c3d4-session"} {
		if !strings.Contains(shown.CFOConversationLeft, want) {
			t.Errorf("CFOConversationLeft = %q, want it to say %q", shown.CFOConversationLeft, want)
		}
	}
	if strings.Contains(shown.Registration, "a1b2c3d4-session") {
		t.Errorf("Registration = %q, want the conversation left apart from any registration problem", shown.Registration)
	}
	if cleared.CFOConversationLeft != "" {
		t.Errorf("CFOConversationLeft = %q once the CFO came back on its conversation, want nothing", cleared.CFOConversationLeft)
	}
}
