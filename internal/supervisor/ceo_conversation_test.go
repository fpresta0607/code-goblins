package supervisor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// A conversation the CEO could not resume when it came back stays on the
// board, apart from any registration problem, with the command that opens it
// by hand, until the CEO next comes back on its conversation.
func TestTheBoardNamesTheConversationTheCEOCouldNotResume(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	service := &Service{Store: store, Instance: "test-instance"}
	left := CEOConversationLeft{Harness: "claude", Session: "a1b2c3d4-session", Resume: []string{"--resume", "a1b2c3d4-session"}}
	if _, err := service.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if err := RecordCEOConversationLeft(h.State, left); err != nil {
		t.Fatal(err)
	}

	// Act
	shown, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordCEOConversationLeft(h.State, CEOConversationLeft{Harness: "codex", Session: "a-different-session", Resume: []string{"resume", "a-different-session"}}); err != nil {
		t.Fatal(err)
	}
	updated, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	ClearCEOConversationLeft(h.State)
	cleared, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	for _, want := range []string{"conversation a1b2c3d4-session could not be resumed", "started on a new one", "claude --resume a1b2c3d4-session"} {
		if !strings.Contains(shown.CEOConversationLeft, want) {
			t.Errorf("CEOConversationLeft = %q, want it to say %q", shown.CEOConversationLeft, want)
		}
	}
	if strings.Contains(shown.Registration, "a1b2c3d4-session") {
		t.Errorf("Registration = %q, want the conversation left apart from any registration problem", shown.Registration)
	}
	if !strings.Contains(updated.CEOConversationLeft, "codex resume a-different-session") || strings.Contains(updated.CEOConversationLeft, "a1b2c3d4-session") {
		t.Errorf("updated CEOConversationLeft = %q, want only the latest conversation", updated.CEOConversationLeft)
	}
	if cleared.CEOConversationLeft != "" {
		t.Errorf("CEOConversationLeft = %q once the CEO came back on its conversation, want nothing", cleared.CEOConversationLeft)
	}
}

func TestSnapshotKeepsAnUnchangedCEOConversationNotice(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	service := &Service{Store: store}
	path := filepath.Join(h.Data, "backlog.md")
	writeFile(t, path, "## Queued\n- **queued** - Queued task\n  Old detail.\n")
	if err := RecordCEOConversationLeft(h.State, CEOConversationLeft{Harness: "claude", Session: "a1b2c3d4-session", Resume: []string{"--resume", "a1b2c3d4-session"}}); err != nil {
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
		t.Errorf("changed backlog snapshot with an unchanged CEO notice opened %d files, want at most five", count)
	}
	if after.CEOConversationLeft != before.CEOConversationLeft || !strings.Contains(after.CEOConversationLeft, "claude --resume a1b2c3d4-session") {
		t.Errorf("CEOConversationLeft = %q, want the unchanged notice %q", after.CEOConversationLeft, before.CEOConversationLeft)
	}
	for _, task := range after.Tasks {
		if task.ID == "queued" && task.Detail == "New detail is longer." {
			return
		}
	}
	t.Fatal("the changed queued task detail was not read")
}
