package verify

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// storedExecution writes an execution record with status, created at, and
// both its logs, last written at written, and returns its run ID.
func storedExecution(t *testing.T, dir, status string, created, written time.Time) string {
	t.Helper()
	id := rand.Text()
	data, err := json.Marshal(ExecutionRecord{Version: 1, ID: id, Status: status, Created: created})
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{id + ".json": data, id + ".json.stdout.log": []byte("out"), id + ".json.stderr.log": []byte("err")} {
		writeAged(t, filepath.Join(dir, name), content, written)
	}
	return id
}

func writeAged(t *testing.T, path string, content []byte, written time.Time) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
}

func isStored(dir, id string) bool {
	for _, name := range []string{id + ".json", id + ".json.stdout.log", id + ".json.stderr.log"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}

// storedRun writes count records of status, the first the oldest, and
// returns their run IDs.
func storedRun(t *testing.T, dir, status string, count int, now time.Time) []string {
	t.Helper()
	var ids []string
	for index := range count {
		created := now.Add(-time.Duration(count-index) * time.Minute)
		ids = append(ids, storedExecution(t, dir, status, created, created))
	}
	return ids
}

func TestPruneExecutionsKeepsTheNewestOfEachOutcomeApart(t *testing.T) {
	// Arrange
	dir, custody := t.TempDir(), t.TempDir()
	now := time.Now()
	groups := []struct {
		ids  []string
		kept int
	}{
		{storedRun(t, dir, "passed", keptPasses+3, now), keptPasses},
		{storedRun(t, dir, "failed", keptFailures+2, now), keptFailures},
		{storedRun(t, dir, "not_started", keptRefusals+2, now), keptRefusals},
	}

	// Act
	err := pruneExecutions(dir, custody, executionBudget, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		removed := len(group.ids) - group.kept
		for index, id := range group.ids {
			if isStored(dir, id) != (index >= removed) {
				t.Fatalf("record %d of %d: stored = %v, want only the newest %d kept", index, len(group.ids), isStored(dir, id), group.kept)
			}
		}
	}
}

func TestPruneExecutionsNeverRemovesOrCountsWhatARunMayStillNeed(t *testing.T) {
	// Arrange
	dir, custody := t.TempDir(), t.TempDir()
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	failures := storedRun(t, dir, "failed", keptFailures, now.Add(-time.Hour))
	pinned := storedExecution(t, dir, "failed", now, now)
	writeAged(t, filepath.Join(custody, pinned+".json"), []byte(`{}`), now)
	running := storedExecution(t, dir, "pending", now, now.Add(-time.Hour))
	abandoned := storedExecution(t, dir, "pending", old, old)
	foreign := filepath.Join(dir, "notes.json")
	writeAged(t, foreign, []byte("{}"), old)

	// Act
	err := pruneExecutions(dir, custody, executionBudget, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !isStored(dir, pinned) || !isStored(dir, running) {
		t.Fatalf("removed a record custody pins (%v) or a run still pending (%v)", isStored(dir, pinned), isStored(dir, running))
	}
	for index, id := range failures {
		if !isStored(dir, id) {
			t.Fatalf("failure %d was evicted, as if pinned or pending records counted against the failures kept", index)
		}
	}
	if isStored(dir, abandoned) {
		t.Fatal("kept a record pending past any run's length beyond the failures it keeps")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("removed a file the store did not name: %v", err)
	}
}

func TestPruneExecutionsProtectsARecordItCannotReadOnlyWhileItIsYoung(t *testing.T) {
	// Arrange
	dir, custody := t.TempDir(), t.TempDir()
	now := time.Now()
	young, stale, renamed, newer, newerStale := rand.Text(), rand.Text(), rand.Text(), rand.Text(), rand.Text()
	writeAged(t, filepath.Join(dir, young+".json"), []byte("{"), now.Add(-time.Hour))
	writeAged(t, filepath.Join(dir, stale+".json"), []byte("{"), now.Add(-2*abandonedExecutionAge))
	other, err := json.Marshal(ExecutionRecord{Version: 1, ID: rand.Text(), Status: "passed", Created: now})
	if err != nil {
		t.Fatal(err)
	}
	writeAged(t, filepath.Join(dir, renamed+".json"), other, now.Add(-2*abandonedExecutionAge))
	// A record a newer cfo writes, in a format this one does not know.
	for id, written := range map[string]time.Time{newer: now.Add(-time.Hour), newerStale: now.Add(-2 * abandonedExecutionAge)} {
		data, err := json.Marshal(ExecutionRecord{Version: 2, ID: id, Status: "passed", Created: written})
		if err != nil {
			t.Fatal(err)
		}
		writeAged(t, filepath.Join(dir, id+".json"), data, written)
	}
	storedRun(t, dir, "failed", keptFailures, now)

	// Act
	err = pruneExecutions(dir, custody, executionBudget, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{young, newer} {
		if _, err := os.Stat(filepath.Join(dir, id+".json")); err != nil {
			t.Fatalf("removed record %s, which may still be written or a newer cfo's: %v", id, err)
		}
	}
	for _, id := range []string{stale, renamed, newerStale} {
		if _, err := os.Stat(filepath.Join(dir, id+".json")); err == nil {
			t.Fatalf("record %s, unreadable, naming another run or in an unknown format, outlived the failures kept after it", id)
		}
	}
}

func TestPruneExecutionsKeepsTheNewestFailureThatAloneOverrunsItsBudget(t *testing.T) {
	// Arrange
	dir, custody := t.TempDir(), t.TempDir()
	now := time.Now()
	pass := storedExecution(t, dir, "passed", now.Add(-time.Hour), now)
	earlier := storedExecution(t, dir, "failed", now.Add(-time.Minute), now)
	failure := storedExecution(t, dir, "failed", now, now)

	// Act
	err := pruneExecutions(dir, custody, 1, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !isStored(dir, failure) || !isStored(dir, earlier) || !isStored(dir, pass) {
		t.Fatalf("stored: failures %v and %v, pass %v, want the newest five failures and the newest pass kept over the budget", isStored(dir, failure), isStored(dir, earlier), isStored(dir, pass))
	}
}

func TestPruneExecutionsKeepsWhatItKeepsWithinItsBudgetPassesGoingFirst(t *testing.T) {
	// Arrange
	dir, custody := t.TempDir(), t.TempDir()
	now := time.Now()
	oldPass := storedExecution(t, dir, "passed", now.Add(-3*time.Hour), now)
	newPass := storedExecution(t, dir, "passed", now.Add(-time.Hour), now)
	oldFailure := storedExecution(t, dir, "failed", now.Add(-4*time.Hour), now)
	sizeOf := func(id string) int64 {
		var size int64
		for _, name := range []string{id + ".json", id + ".json.stdout.log", id + ".json.stderr.log"} {
			info, err := os.Stat(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			size += info.Size()
		}
		return size
	}
	budget := sizeOf(newPass) + sizeOf(oldFailure)

	// Act
	err := pruneExecutions(dir, custody, budget, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if isStored(dir, oldPass) || !isStored(dir, newPass) || !isStored(dir, oldFailure) {
		t.Fatalf("stored: old pass %v, new pass %v, old failure %v, want the oldest pass gone first", isStored(dir, oldPass), isStored(dir, newPass), isStored(dir, oldFailure))
	}
}

func TestPruneExecutionsRemovesALogOnlyOnceItIsAbandoned(t *testing.T) {
	// Arrange
	dir, custody := t.TempDir(), t.TempDir()
	now := time.Now()
	fresh, stale := filepath.Join(dir, rand.Text()+".json.stdout.log"), filepath.Join(dir, rand.Text()+".json.stderr.log")
	writeAged(t, fresh, []byte("log"), now.Add(-time.Hour))
	writeAged(t, stale, []byte("log"), now.Add(-2*abandonedExecutionAge))

	// Act
	err := pruneExecutions(dir, custody, executionBudget, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("removed a log a run may still write: %v", err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("kept a log abandoned past any run's length")
	}
}

func TestPruneExecutionsReadsCustodyWhereAdmissionKeepsIt(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	executions := filepath.Join(store, "executions")
	if err := os.MkdirAll(executions, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pinned := storedExecution(t, executions, "failed", now.Add(-48*time.Hour), now.Add(-48*time.Hour))
	storedRun(t, executions, "failed", keptFailures, now)
	admission, err := AdmissionDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(admission, "custody"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeAged(t, filepath.Join(admission, "custody", pinned+".json"), []byte(`{}`), now)

	// Act
	err = PruneExecutions(now)

	// Assert
	if err != nil || !isStored(executions, pinned) {
		t.Fatalf("PruneExecutions = %v, pinned stored = %v, want custody read from the admission store", err, isStored(executions, pinned))
	}
}

func TestPruneExecutionsOfAStoreThatHasNoneIsNoError(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())

	// Act
	err := PruneExecutions(time.Now())

	// Assert
	if err != nil {
		t.Fatalf("pruning a store with no executions = %v", err)
	}
}

// A run that kept no logs, as cfo gate turn's, whose output its caller
// keeps, is kept apart, so a burst of them never evicts a run's logs.
func TestPruneExecutionsKeepsRunsWithoutLogsApart(t *testing.T) {
	// Arrange
	now := time.Now()
	dir := t.TempDir()
	logged := storedExecution(t, dir, "passed", now.Add(-time.Hour), now.Add(-time.Hour))
	failures := storedRun(t, dir, "failed", keptFailures, now.Add(-time.Hour))
	// A record its run left pending a week ago, with no logs, as a wrapper
	// ended while it waited leaves: newer than every failure by when it was
	// begun, it must not take a failure's place.
	abandoned := rand.Text()
	abandonedRecord, err := json.Marshal(ExecutionRecord{Version: 1, ID: abandoned, Status: "pending", Created: now})
	if err != nil {
		t.Fatal(err)
	}
	writeAged(t, filepath.Join(dir, abandoned+".json"), abandonedRecord, now.Add(-2*abandonedExecutionAge))
	var unlogged []string
	for index := range keptUnlogged + 3 {
		id := rand.Text()
		created := now.Add(-time.Duration(keptUnlogged+3-index) * time.Second)
		data, err := json.Marshal(ExecutionRecord{Version: 1, ID: id, Status: "passed", Created: created})
		if err != nil {
			t.Fatal(err)
		}
		writeAged(t, filepath.Join(dir, id+".json"), data, created)
		unlogged = append(unlogged, id)
	}

	// Act
	err = pruneExecutions(dir, t.TempDir(), executionBudget, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !isStored(dir, logged) {
		t.Fatal("a burst of runs without logs evicted a run's logs")
	}
	for _, id := range failures {
		if !isStored(dir, id) {
			t.Fatal("a run without logs, left pending, evicted a failure's logs")
		}
	}
	for index, id := range unlogged {
		_, statError := os.Stat(filepath.Join(dir, id+".json"))
		// The abandoned record is a run without logs too, the newest of them.
		if isKept := statError == nil; isKept != (index >= 4) {
			t.Fatalf("run %d of %d without logs kept = %v; want the newest %d kept", index, len(unlogged), isKept, keptUnlogged)
		}
	}
}

// A run that kept no logs is a record alone, which would free next to
// nothing, so a store over its budget keeps it, bounded by its count only.
func TestPruneExecutionsKeepsRunsWithoutLogsOverItsBudget(t *testing.T) {
	// Arrange
	now := time.Now()
	dir := t.TempDir()
	passes := storedRun(t, dir, "passed", 3, now)
	id := rand.Text()
	data, err := json.Marshal(ExecutionRecord{Version: 1, ID: id, Status: "passed", Created: now.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	writeAged(t, filepath.Join(dir, id+".json"), data, now.Add(-time.Hour))

	// Act
	err = pruneExecutions(dir, t.TempDir(), 1, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, id+".json")); err != nil {
		t.Fatalf("a run without logs was evicted for the budget: %v", err)
	}
	if !isStored(dir, passes[len(passes)-1]) || isStored(dir, passes[0]) {
		t.Fatal("the budget did not trim the passes to the newest")
	}
}
