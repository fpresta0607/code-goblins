package verify

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	young, stale, renamed := rand.Text(), rand.Text(), rand.Text()
	writeAged(t, filepath.Join(dir, young+".json"), []byte("{"), now.Add(-time.Hour))
	writeAged(t, filepath.Join(dir, stale+".json"), []byte("{"), now.Add(-2*abandonedExecutionAge))
	other, err := json.Marshal(ExecutionRecord{Version: 1, ID: rand.Text(), Status: "passed", Created: now})
	if err != nil {
		t.Fatal(err)
	}
	writeAged(t, filepath.Join(dir, renamed+".json"), other, now.Add(-2*abandonedExecutionAge))
	storedRun(t, dir, "failed", keptFailures, now)

	// Act
	err = pruneExecutions(dir, custody, executionBudget, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, young+".json")); err != nil {
		t.Fatalf("removed a record that may still be being written: %v", err)
	}
	for _, id := range []string{stale, renamed} {
		if _, err := os.Stat(filepath.Join(dir, id+".json")); err == nil {
			t.Fatalf("record %s, unreadable or naming another run, outlived the failures kept after it", id)
		}
	}
}

func TestPruneExecutionsKeepsTheNewestFailureThatAloneOverrunsItsBudget(t *testing.T) {
	// Arrange
	dir, custody := t.TempDir(), t.TempDir()
	now := time.Now()
	pass := storedExecution(t, dir, "passed", now.Add(-time.Hour), now)
	failure := storedExecution(t, dir, "failed", now, now)

	// Act
	err := pruneExecutions(dir, custody, 1, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !isStored(dir, failure) || !isStored(dir, pass) {
		t.Fatalf("stored: failure %v, pass %v, want the newest of each kept over the budget", isStored(dir, failure), isStored(dir, pass))
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

func TestPruneExecutionsNeverTouchesWhatIsOnlyNamedLikeTheStoresOwn(t *testing.T) {
	// Arrange
	dir, custody := t.TempDir(), t.TempDir()
	now := time.Now()
	old := now.Add(-2 * abandonedExecutionAge)
	id := rand.Text()
	var lookalikes []string
	for _, name := range []string{strings.ToLower(id) + ".json", id[:25] + ".json", id + "A.json", id + ".json.bak", id + ".json.cfo-tmp-1", id + ".json.stdout.log.old"} {
		path := filepath.Join(dir, name)
		writeAged(t, path, []byte("{"), old)
		lookalikes = append(lookalikes, path)
	}
	folder := filepath.Join(dir, rand.Text()+".json")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	writeAged(t, filepath.Join(target, "kept"), []byte("kept"), old)
	junction := filepath.Join(dir, rand.Text()+".json.stdout.log")
	if output, err := exec.Command("cmd", "/d", "/c", "mklink", "/J", junction, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v %s", err, output)
	}
	storedRun(t, dir, "failed", keptFailures+1, now)

	// Act
	err := pruneExecutions(dir, custody, executionBudget, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range append(lookalikes, folder, junction, filepath.Join(target, "kept")) {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("removed %s, which the store did not name as its own: %v", path, err)
		}
	}
}

func TestPruneExecutionsRefusesAStoreFolderThatIsAJunction(t *testing.T) {
	// Arrange
	target := t.TempDir()
	// A refusal has no floor, so a budget of nothing removes it when the
	// folder is followed.
	id := storedExecution(t, target, "not_started", time.Now().Add(-time.Hour), time.Now())
	dir := filepath.Join(t.TempDir(), "executions")
	if output, err := exec.Command("cmd", "/d", "/c", "mklink", "/J", dir, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v %s", err, output)
	}

	// Act
	err := pruneExecutions(dir, t.TempDir(), 0, time.Now())

	// Assert
	if err == nil || !isStored(target, id) {
		t.Fatalf("pruning through a junction = %v, stored = %v, want a refusal that touches nothing", err, isStored(target, id))
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
