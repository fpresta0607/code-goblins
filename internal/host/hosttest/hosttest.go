// Package hosttest is test support for a test that hosts a native terminal
// in its own process with host.Run and then changes what the host's record
// says, such as which program the terminal runs.
package hosttest

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/host"
)

// Rewrite reads the record of terminal id's host under stateDir, lets change
// alter it, and puts it back as the host itself records it: written beside
// the record and renamed over it.
//
// A host puts its record in place with a rename, and the handle that renamed
// the file stays open for deletion until Windows has closed it. The record
// reads from the moment the rename lands, so a test that waits for it and
// writes it back at once does so while that handle is open. os.WriteFile
// opens a file without sharing deletion, which Windows refuses meanwhile:
// "The process cannot access the file because it is being used by another
// process", as main's CI failed on 2026-10-10 (run 38054286836). A rename
// shares deletion, so that handle does not refuse it.
func Rewrite(t testing.TB, stateDir, id string, change func(*host.Record)) {
	t.Helper()
	record, err := host.ReadRecord(stateDir, id)
	if err != nil {
		t.Fatal(err)
	}
	change(&record)
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := fsx.AtomicWriteFile(filepath.Join(stateDir, "hosts", id+".json"), data); err != nil {
		t.Fatal(err)
	}
}
