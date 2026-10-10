package wake

import (
	"fmt"
	"os"
	"slices"
	"testing"
)

// stateEntries is the name of every file and folder directly inside dir.
func stateEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// On 2026-10-09 an acknowledgement of 55 records wrote a notice file for
// each, about 1.5 s apiece on a loaded machine: 84 s. However many records an
// acknowledgement retires, their notices are one write to one file.
func TestAnAcknowledgementKeepsEveryNoticeInOneFile(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	first := map[string]int{}
	last := 0
	for at := range 55 {
		identity := fmt.Sprintf("done/g%d", at)
		record, err := AppendOnce(dir, identity, "notify", "g1", "done: "+identity)
		if err != nil {
			t.Fatal(err)
		}
		first[identity], last = record.Seq, record.Seq
	}
	before := stateEntries(t, dir)

	// Act
	err := AckThrough(dir, last)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var added []string
	for _, name := range stateEntries(t, dir) {
		if !slices.Contains(before, name) {
			added = append(added, name)
		}
	}
	if want := []string{".wake-ack", ".wake-noticed"}; !slices.Equal(added, want) {
		t.Errorf("the acknowledgement of 55 records added %v to the state folder, want %v: the ack floor and one file of notices", added, want)
	}
	if notices, err := os.ReadDir(dir + `\wake-notices`); err == nil {
		t.Errorf("the acknowledgement wrote %d notice files, one for each record", len(notices))
	}
	for identity, seq := range first {
		again, isNew, err := AppendFirst(dir, identity, "notify", "g1", "done: "+identity)
		if err != nil || isNew || again.Seq != seq {
			t.Fatalf("the acknowledged notice %s was appended again: %+v, new = %t, %v", identity, again, isNew, err)
		}
	}
	if pending, err := Pending(dir); err != nil || len(pending) != 0 {
		t.Errorf("the queue holds %v, %v, want nothing", pending, err)
	}
}
