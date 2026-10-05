package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEngineChoiceSurvivesRestartAndCancellation(t *testing.T) {
	directory := t.TempDir()
	want := EngineChoice{ID: "task", Generation: "s1", Harness: "codex", Model: "gpt-6.1-sol", Effort: "high", When: "turn-end", Requested: time.Now().UTC()}

	if err := WriteEngineChoice(directory, want); err != nil {
		t.Fatal(err)
	}

	got, err := ReadEngineChoice(directory, want.ID)
	if err != nil || got != want {
		t.Fatalf("persisted choice = %+v, %v; want %+v", got, err, want)
	}
	if err := RemoveEngineChoice(directory, want.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEngineChoice(directory, want.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled choice = %v", err)
	}
	if err := RemoveEngineChoice(directory, want.ID); err != nil {
		t.Fatalf("cancel again = %v", err)
	}
}

func TestEngineChoiceRejectsDifferentTaskIdentityAndMalformedRecords(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "engine"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, record := range []string{`{"id":"other","generation":"s1","harness":"codex","when":"turn-end"}`, `{"id":"task","generation":"s1","harness":"codex","when":"surprise"}`, `{`} {
		if err := os.WriteFile(filepath.Join(directory, "engine", "task.json"), []byte(record), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadEngineChoice(directory, "task"); err == nil {
			t.Fatalf("accepted record %s", record)
		}
	}
	if err := WriteEngineChoice(directory, EngineChoice{ID: "../escape", Generation: "s1", Harness: "codex", When: "resume"}); err == nil {
		t.Fatal("accepted path outside engine directory")
	}
}
