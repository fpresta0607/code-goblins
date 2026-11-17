package supervisor

import (
	"context"
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

func hookFailureRecord(t *testing.T, event, outcome string, at time.Time) []byte {
	t.Helper()
	data, err := json.Marshal(nativehook.Failure{Harness: "pi", Event: event, Outcome: outcome, At: at})
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
	at := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	writeHookFailure(t, h.State, "a", hookFailureRecord(t, "agent_settled", "could not start (ENOENT). The event was not recorded.", at))
	writeHookFailure(t, h.State, "b", hookFailureRecord(t, "session_start", "timed out. It may have started and recorded the event.", at))
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

// A hook program missing for long fails every event; one wake names the
// earliest few, whatever their records are called, and counts the rest.
func TestManyHookFailuresNameTheEarliestAndCountTheRest(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	start := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	for index := range hookFailuresNamed + 1 {
		writeHookFailure(t, h.State, fmt.Sprintf("%02d", index), hookFailureRecord(t, "tool_execution_end", "exited with status 1. It may have recorded the event.", start.Add(time.Duration(index+1)*time.Minute)))
	}
	writeHookFailure(t, h.State, "99", hookFailureRecord(t, "session_start", "timed out. It may have started and recorded the event.", start))

	// Act
	err := store.ingestHookFailures()

	// Assert
	wakes := notifyWakes(t, h.State)
	if err != nil || len(wakes) != 1 {
		t.Fatalf("wakes = %+v (%v), want one", wakes, err)
	}
	detail := wakes[0].Detail
	if !strings.HasPrefix(detail, "native_hook_failed: pi's native hook for session_start timed out.") || strings.Count(detail, "native hook for tool_execution_end") != hookFailuresNamed-1 || !strings.Contains(detail, "and 2 more") {
		t.Fatalf("wake %q, want it to name the earliest session_start failure first, then %d others, and count 2 more", detail, hookFailuresNamed-1)
	}
}

// The reconciliation cycle is what turns the records into the wake (#2428).
func TestTheCycleTellsTheCEOOfHookFailures(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	writeHookFailure(t, h.State, "a", hookFailureRecord(t, "agent_settled", "could not start (ENOENT). The event was not recorded.", time.Now()))
	service := &Service{Store: store, Options: Options{Reconcile: func(context.Context) error { return nil }}}

	// Act
	service.cycle(context.Background(), true)

	// Assert
	wakes := notifyWakes(t, h.State)
	if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "pi's native hook for agent_settled could not start (ENOENT).") {
		t.Fatalf("wakes = %+v (cycle error %q), want one naming the agent_settled failure", wakes, service.lastError)
	}
	if left, err := os.ReadDir(nativehook.FailureDir(h.State)); err != nil || len(left) != 0 {
		t.Fatalf("left %v (%v), want no records", left, err)
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
