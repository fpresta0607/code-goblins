package verify

import (
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
	err := pruneExecutions(dir, custody, t.TempDir(), executionBudget, now)

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
	err := pruneExecutions(dir, t.TempDir(), t.TempDir(), 0, time.Now())

	// Assert
	if err == nil || !isStored(target, id) {
		t.Fatalf("pruning through a junction = %v, stored = %v, want a refusal that touches nothing", err, isStored(target, id))
	}
}

// A pass another process holds so that it cannot be read may name any
// record, so the store removes none.
func TestPruneExecutionsRemovesNothingWhileAPassIsHeldUnreadable(t *testing.T) {
	// Arrange
	dir, passes := t.TempDir(), t.TempDir()
	now := time.Now()
	ids := storedRun(t, dir, "passed", keptPasses+1, now)
	keptPassOf(t, passes, dir, ids[0], now)
	held := filepath.Join(passes, rand.Text()+".json")
	writeAged(t, held, []byte(`{}`), now)
	name, err := syscall.UTF16PtrFromString(held)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(handle)

	// Act
	err = pruneExecutions(dir, t.TempDir(), passes, executionBudget, now)

	// Assert
	if err == nil || !isStored(dir, ids[1]) {
		t.Errorf("pruneExecutions = %v, the oldest unpinned pass stored = %v; want an error and nothing removed", err, isStored(dir, ids[1]))
	}
}
