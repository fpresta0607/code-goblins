package lock

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Windows fixes a process's creation time as it makes the process, so a
// holder is known by its start alone, and the record it writes has the
// fields it had before a record could name an instance.
func TestARecordCarriesNoInstanceOnWindows(t *testing.T) {
	// Arrange
	dir := t.TempDir()

	// Act
	_, err := Acquire(dir)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Release(dir) })
	raw, err := os.ReadFile(filepath.Join(dir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if instance, isRecorded := fields["instance"]; isRecorded {
		t.Errorf("the record names the instance %s, want none on Windows", instance)
	}
}
