//go:build linux || darwin

package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSyncedReplacesAFileWholeAndLeavesNothingStaged(t *testing.T) {
	// Arrange: a reader holds the old file open, which keeps no rename from
	// it here.
	folder := t.TempDir()
	path := filepath.Join(folder, "update.json")
	if err := os.WriteFile(path, []byte("the journal before"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	// Act
	writeErr := writeSynced(path, []byte("the journal after"))

	// Assert
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "the journal after" {
		t.Fatalf("path holds %q, %v; want the new journal", data, err)
	}
	entries, err := os.ReadDir(folder)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the folder holds %v, %v; want only the journal", entries, err)
	}
}

func TestWriteSyncedThatCannotRenameRemovesWhatItStaged(t *testing.T) {
	// Arrange: a folder where the file goes, which no rename of a file
	// replaces.
	path := filepath.Join(t.TempDir(), "update.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	// Act
	err := writeSynced(path, []byte("the journal"))

	// Assert
	if err == nil {
		t.Fatal("a write over a folder succeeded")
	}
	if _, statErr := os.Stat(path + ".update-tmp"); !os.IsNotExist(statErr) {
		t.Fatalf("a failed write left its staged file: %v", statErr)
	}
}
