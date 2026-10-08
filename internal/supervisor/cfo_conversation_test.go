package supervisor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// A conversation the CFO could not resume when it came back stays on the
// board, apart from any registration problem, with the command that opens it
// by hand, until the CFO next comes back on its conversation.
func TestTheBoardNamesTheConversationTheCFOCouldNotResume(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	service := &Service{Store: store, Instance: "test-instance"}
	left := CFOConversationLeft{Harness: "claude", Session: "a1b2c3d4-session", Resume: []string{"--resume", "a1b2c3d4-session"}}
	if _, err := service.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if err := RecordCFOConversationLeft(h.State, left); err != nil {
		t.Fatal(err)
	}

	// Act
	shown, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordCFOConversationLeft(h.State, CFOConversationLeft{Harness: "codex", Session: "a-different-session", Resume: []string{"resume", "a-different-session"}}); err != nil {
		t.Fatal(err)
	}
	updated, err := service.Snapshot()
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
	if !strings.Contains(updated.CFOConversationLeft, "codex resume a-different-session") || strings.Contains(updated.CFOConversationLeft, "a1b2c3d4-session") {
		t.Errorf("updated CFOConversationLeft = %q, want only the latest conversation", updated.CFOConversationLeft)
	}
	if cleared.CFOConversationLeft != "" {
		t.Errorf("CFOConversationLeft = %q once the CFO came back on its conversation, want nothing", cleared.CFOConversationLeft)
	}
}

func TestSnapshotKeepsAnUnchangedCFOConversationNotice(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	service := &Service{Store: store}
	path := filepath.Join(h.Data, "backlog.md")
	writeFile(t, path, "## Queued\n- **queued** - Queued task\n  Old detail.\n")
	if err := RecordCFOConversationLeft(h.State, CFOConversationLeft{Harness: "claude", Session: "a1b2c3d4-session", Resume: []string{"--resume", "a1b2c3d4-session"}}); err != nil {
		t.Fatal(err)
	}
	before, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "## Queued\n- **queued** - Queued task\n  New detail is longer.\n")
	opened := fsx.Opens()

	// Act
	after, err := service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if count := fsx.Opens() - opened; count > 5 {
		t.Errorf("changed backlog snapshot with an unchanged CFO notice opened %d files, want at most five", count)
	}
	if after.CFOConversationLeft != before.CFOConversationLeft || !strings.Contains(after.CFOConversationLeft, "claude --resume a1b2c3d4-session") {
		t.Errorf("CFOConversationLeft = %q, want the unchanged notice %q", after.CFOConversationLeft, before.CFOConversationLeft)
	}
	for _, task := range after.Tasks {
		if task.ID == "queued" && task.Detail == "New detail is longer." {
			return
		}
	}
	t.Fatal("the changed queued task detail was not read")
}
