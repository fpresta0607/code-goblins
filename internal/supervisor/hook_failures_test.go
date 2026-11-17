package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// writeHookFailure writes one record as a pi extension does.
func writeHookFailure(t *testing.T, stateDir, name string, data []byte) {
	t.Helper()
	dir := nativehook.FailureDir(stateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".failure.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func hookFailureRecord(t *testing.T, event, outcome string) []byte {
	t.Helper()
	data, err := json.Marshal(nativehook.Failure{Harness: "pi", Event: event, Outcome: outcome, At: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func notifyWakes(t *testing.T, stateDir string) []wake.Record {
	t.Helper()
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	var notifies []wake.Record
	for _, record := range records {
		if record.Kind == "notify" && record.Key == "native-hook" {
			notifies = append(notifies, record)
		}
	}
	return notifies
}

// A hook a pi session could not run reaches the CEO from its record, once,
// with the event and the outcome, and its record is gone after (#2428).
func TestHookFailuresOfPiSessionsWakeTheCEOOnceAndLeaveNoRecord(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	writeHookFailure(t, h.State, "a", hookFailureRecord(t, "agent_settled", "could not start (ENOENT). The event was not recorded."))
	writeHookFailure(t, h.State, "b", hookFailureRecord(t, "session_start", "timed out. It may have started and recorded the event."))
	writeHookFailure(t, h.State, "c", []byte("{not json"))
	if err := os.WriteFile(filepath.Join(nativehook.FailureDir(h.State), ".staged.tmp"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	err := store.ingestHookFailures()
	again := store.ingestHookFailures()

	// Assert
	if err != nil || again != nil {
		t.Fatalf("ingest = %v, then %v", err, again)
	}
	wakes := notifyWakes(t, h.State)
	if len(wakes) != 1 {
		t.Fatalf("wakes = %+v, want one", wakes)
	}
	for _, want := range []string{"pi's native hook for agent_settled could not start (ENOENT).", "pi's native hook for session_start timed out.", "a failure whose record could not be read", "next:"} {
		if !strings.Contains(wakes[0].Detail, want) {
			t.Errorf("wake %q, want it to name %q", wakes[0].Detail, want)
		}
	}
	left, err := os.ReadDir(nativehook.FailureDir(h.State))
	if err != nil || len(left) != 1 || left[0].Name() != ".staged.tmp" {
		t.Fatalf("left %v (%v), want only the record still being written", left, err)
	}
}

// A hook program missing for long fails every event; one wake names the first
// few and counts the rest.
func TestManyHookFailuresAreCountedNotEachNamed(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	for index := range hookFailuresNamed + 2 {
		writeHookFailure(t, h.State, fmt.Sprintf("%02d", index), hookFailureRecord(t, "tool_execution_end", "exited with status 1. It may have recorded the event."))
	}

	// Act
	err := store.ingestHookFailures()

	// Assert
	wakes := notifyWakes(t, h.State)
	if err != nil || len(wakes) != 1 || strings.Count(wakes[0].Detail, "native hook for tool_execution_end") != hookFailuresNamed || !strings.Contains(wakes[0].Detail, "and 2 more") {
		t.Fatalf("wakes = %+v (%v), want one naming %d failures and counting 2 more", wakes, err, hookFailuresNamed)
	}
}

func TestNoHookFailureRecordsWakeNobody(t *testing.T) {
	// Arrange
	store, h := testStore(t)

	// Act
	err := store.ingestHookFailures()

	// Assert
	if err != nil || len(notifyWakes(t, h.State)) != 0 {
		t.Fatalf("ingest with no records = %v, wakes %+v; want nothing", err, notifyWakes(t, h.State))
	}
}
