package state

import (
	"strings"
	"testing"
	"time"
)

func TestPreparedWaitPersistsExactIdentityAndRejectsUnsupportedConditions(t *testing.T) {
	at := time.Now().UTC()
	condition, err := NewPauseCondition("dependency", "task:prerequisite", at)
	if err != nil {
		t.Fatal(err)
	}
	original := PreparedWait{ID: "prepared", Generation: "current", Project: "project", Worktree: "worktree", Session: "session", Branch: "feat/prepared", Head: strings.Repeat("a", 40), Handoff: "handoff.md", Digest: strings.Repeat("b", 64), Condition: condition, PreparedAt: at, Phase: "prepared"}
	original.Operation = original.OperationID()
	for _, test := range []struct {
		name    string
		change  func(*PreparedWait)
		isValid bool
	}{
		{"task delivery", func(*PreparedWait) {}, true},
		{"PR merge", func(record *PreparedWait) { record.Condition.Until = "pr:https://github.com/owner/repo/pull/1" }, true},
		{"installed feature", func(record *PreparedWait) { record.Condition.Until = "installed:feature" }, false},
		{"date", func(record *PreparedWait) { record.Condition.Until = "date:" + at.Format(time.RFC3339) }, false},
		{"self", func(record *PreparedWait) { record.Condition.Until = "task:prepared" }, false},
		{"missing handoff", func(record *PreparedWait) { record.Digest = "" }, false},
		{"unbound generation", func(record *PreparedWait) { record.Generation = "" }, false},
		{"missing completion time", func(record *PreparedWait) { record.PreparedAt = time.Time{} }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := original
			test.change(&record)
			record.Operation = record.OperationID()
			directory := t.TempDir()
			err := WritePreparedWait(directory, record)
			if (err == nil) != test.isValid {
				t.Fatalf("write error=%v, valid=%t", err, test.isValid)
			}
			if test.isValid {
				saved, err := ReadPreparedWait(directory, record.ID)
				if err != nil || saved.Operation != record.Operation || saved != record {
					t.Fatalf("saved=%+v, err=%v", saved, err)
				}
			}
		})
	}
}
