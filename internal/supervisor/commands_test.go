package supervisor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOnlyAHarnesssOwnCommandsAreRead(t *testing.T) {
	// Arrange
	root := t.TempDir()
	write := func(path string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "commands", "compact.md"))
	write(filepath.Join(root, "commands", "review.md"))
	write(filepath.Join(root, "skills", "deploy", "SKILL.md"))
	write(filepath.Join(root, "skills", "notes.txt"))

	// Act
	names := uniqueSorted(claudeCommands(root))

	// Assert
	if want := []string{"compact", "deploy", "review"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("commands = %v, want %v", names, want)
	}
}

func TestAHarnessWithNoRecordPublishesNoCommands(t *testing.T) {
	if names := claudeCommands(t.TempDir()); len(names) != 0 {
		t.Fatalf("an empty folder published %v", names)
	}
}
