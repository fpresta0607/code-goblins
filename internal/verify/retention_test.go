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
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, written, written); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func isStored(dir, id string) bool {
	for _, name := range []string{id + ".json", id + ".json.stdout.log", id + ".json.stderr.log"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}

func TestPruneExecutionsKeepsTheNewestOfEachOutcome(t *testing.T) {
	// Arrange
	dir, custody := t.TempDir(), t.TempDir()
	now := time.Now()
	var passes, failures []string
	for index := range keptPasses + 3 {
		created := now.Add(-time.Duration(keptPasses+3-index) * time.Minute)
		passes = append(passes, storedExecution(t, dir, "passed", created, created))
	}
	for index := range keptFailures + 2 {
		created := now.Add(-time.Duration(keptFailures+2-index) * time.Hour)
		status := "failed"
		if index%2 == 0 {
			status = "not_started"
		}
		failures = append(failures, storedExecution(t, dir, status, created, created))
	}

	// Act
	err := pruneExecutions(dir, custody, executionBudget, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for index, id := range passes {
		if isStored(dir, id) != (index >= 3) {
			t.Fatalf("pass %d of %d: stored = %v, want only the newest %d kept", index, len(passes), isStored(dir, id), keptPasses)
		}
	}
	for index, id := range failures {
		if isStored(dir, id) != (index >= 2) {
			t.Fatalf("failure %d of %d: stored = %v, want only the newest %d kept", index, len(failures), isStored(dir, id), keptFailures)
		}
	}
}

func TestPruneExecutionsNeverRemovesWhatARunMayStillNeed(t *testing.T) {
	// Arrange
	dir, custody := t.TempDir(), t.TempDir()
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	for range keptFailures {
		storedExecution(t, dir, "failed", now, now)
	}
	pinned := storedExecution(t, dir, "failed", old, old)
	if err := os.WriteFile(filepath.Join(custody, pinned+".json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	running := storedExecution(t, dir, "pending", old, now.Add(-time.Hour))
	abandoned := storedExecution(t, dir, "pending", old, old)
	foreign := filepath.Join(dir, "notes.json")
	if err := os.WriteFile(foreign, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	err := pruneExecutions(dir, custody, executionBudget, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !isStored(dir, pinned) || !isStored(dir, running) {
		t.Fatalf("removed a record custody pins (%v) or a run still pending (%v)", isStored(dir, pinned), isStored(dir, running))
	}
	if isStored(dir, abandoned) {
		t.Fatal("kept a record pending past any run's length beyond the failures it keeps")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("removed a file the store did not name: %v", err)
	}
}

func TestPruneExecutionsCountsAnUnreadableRecordAsNoPass(t *testing.T) {
	// Arrange
	dir, custody := t.TempDir(), t.TempDir()
	now := time.Now()
	unreadable := rand.Text()
	path := filepath.Join(dir, unreadable+".json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	for range keptFailures {
		storedExecution(t, dir, "failed", now, now)
	}

	// Act
	err := pruneExecutions(dir, custody, executionBudget, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("an unreadable record outlived the failures kept after it")
	}
}

func TestPruneExecutionsRemovesALogOnlyOnceItIsAbandoned(t *testing.T) {
	// Arrange
	dir, custody := t.TempDir(), t.TempDir()
	now := time.Now()
	fresh, stale := filepath.Join(dir, rand.Text()+".json.stdout.log"), filepath.Join(dir, rand.Text()+".json.stderr.log")
	for path, written := range map[string]time.Time{fresh: now.Add(-time.Hour), stale: now.Add(-2 * abandonedExecutionAge)} {
		if err := os.WriteFile(path, []byte("log"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, written, written); err != nil {
			t.Fatal(err)
		}
	}

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
