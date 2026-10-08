package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestLiveInReadsATaskAsLiveWhileItsRecordExists(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	if err := os.WriteFile(state.TaskMetaPath(stateDir, "task-a"), []byte("id=task-a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	isLive := LiveIn(stateDir)

	// Act and assert
	cases := map[string]bool{"task-a": true, "task-b": false, `..\task-a`: false, "": false}
	for task, want := range cases {
		if got := isLive(task); got != want {
			t.Errorf("live(%q) = %v, want %v", task, got, want)
		}
	}
}

func TestReadRecordOfAHomeThatNeverStartedAStackIsEmpty(t *testing.T) {
	record, err := ReadRecord(t.TempDir())

	if err != nil || len(record.Stacks) != 0 || record.Engine.StartedByCFO {
		t.Fatalf("record = %+v err = %v, want an empty record", record, err)
	}
}

func TestReadRecordRefusesARecordItCannotDecode(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, RecordName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadRecord(stateDir); err == nil {
		t.Fatal("ReadRecord decoded a broken record as empty, which would forget every hold")
	}
}
