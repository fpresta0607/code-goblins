package afk

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The supervisor asks for the stretch's lines for every snapshot it sends, so
// the reader reads the log's file again only when the file has changed, and
// always when it has.
func TestAReaderReadsTheLogAgainOnlyWhenItHasChanged(t *testing.T) {
	// Arrange
	dir, state := turnedOn(t)
	var reader Reader
	deploy := Entry{Kind: KindDeploy, What: "acme production", Evidence: "/health reads 200"}
	if _, err := Log(dir, deploy, night.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, auditFile)

	// Act
	first, unreadable, err := reader.Entries(dir, state.Session)
	if err != nil || unreadable != 0 {
		t.Fatal(unreadable, err)
	}
	// The same size and time of writing with other words in it: a reader
	// that read the file again would return these.
	written, err := os.Stat(log)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, make([]byte, len(kept)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(log, written.ModTime(), written.ModTime()); err != nil {
		t.Fatal(err)
	}
	unchanged, _, err := reader.Entries(dir, state.Session)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, kept, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Log(dir, Entry{Kind: KindInstall, What: "cfo 1.4.2", Evidence: "cfo version reads 1.4.2"}, night.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	grown, _, err := reader.Entries(dir, state.Session)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := reader.Entries(dir, "afk-another-stretch")
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(first) != 2 || first[1].What != deploy.What {
		t.Fatalf("the first read = %+v, want the switch and the deploy", first)
	}
	if len(unchanged) != 2 || unchanged[1].What != deploy.What {
		t.Errorf("a read of a file that had not changed = %+v, want what was read before", unchanged)
	}
	if len(grown) != 3 || grown[2].Kind != KindInstall {
		t.Errorf("a read after the log grew = %+v, want the new line too", grown)
	}
	if len(other) != 0 {
		t.Errorf("another stretch's lines = %+v, want none: the reader keeps one stretch at a time and never hands it to another", other)
	}
}

// A home where AFK mode was never turned on has no log, which reads as no
// lines.
func TestAReaderReadsNoLogAsNoLines(t *testing.T) {
	var reader Reader

	entries, unreadable, err := reader.Entries(t.TempDir(), "afk-1")

	if err != nil || unreadable != 0 || len(entries) != 0 {
		t.Fatalf("Entries = %+v, %d, %v, want nothing", entries, unreadable, err)
	}
}
