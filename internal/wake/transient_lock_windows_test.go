package wake

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

// holdOpen opens path with share and closes it after hold, as another
// process reading or replacing the queue would.
func holdOpen(t *testing.T, path string, share uint32, hold time.Duration) <-chan struct{} {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(hold)
		windows.CloseHandle(handle)
		close(released)
	}()
	return released
}

// On 2026-10-01 at about 03:44Z the CFO's Stop hook failed with "open
// state\.wake-queue: The process cannot access the file because it is being
// used by another process" after one attempt. Another process holding the
// queue for a moment must cost a wait, never a failed hook or a lost notify.
func TestTheWakeQueueOutlastsAnotherProcessHoldingIt(t *testing.T) {
	tests := []struct {
		name  string
		share uint32
		act   string
	}{
		{name: "a notify while a reader holds the queue as Go and PowerShell open it", share: windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE, act: "append"},
		{name: "a notify while a handle holds the queue exclusively", share: 0, act: "append"},
		{name: "the hook's read while a handle holds the queue exclusively", share: 0, act: "pending"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			if _, err := Append(dir, "notify", "first", "working: first"); err != nil {
				t.Fatal(err)
			}
			released := holdOpen(t, filepath.Join(dir, queueFile), test.share, 1500*time.Millisecond)

			// Act
			var err error
			want := 1
			switch test.act {
			case "append":
				_, err = Append(dir, "notify", "second", "working: second")
				want = 2
			case "pending":
				_, err = Pending(dir)
			}
			<-released

			// Assert
			if err != nil {
				t.Fatalf("%s while the queue was held for 1.5 s: %v", test.act, err)
			}
			records, err := Pending(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != want {
				t.Errorf("the queue holds %d records, want %d", len(records), want)
			}
		})
	}
}

// A live process holding .wake-queue.lock through a read-modify-write that a
// loaded machine slows down must cost a notify a wait, not the notify.
func TestAppendWaitsOutALiveLockHolder(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	// ping itself, not a shell around it, so ending it ends the holder.
	holder := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	if _, err := lock.AcquireNamedOwner(dir, wakeLockName, holder.Process.Pid, "wake"); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(1500 * time.Millisecond)
		// The holder lets go as lock.ReleaseNamed does in its own process.
		_ = os.Remove(filepath.Join(dir, wakeLockName))
		close(released)
	}()

	// Act
	_, err := Append(dir, "notify", "task", "working: task")
	<-released

	// Assert
	if err != nil {
		t.Fatalf("Append while a live process held the wake lock for 1.5 s: %v", err)
	}
}

// deletePending deletes path through a handle that another process keeps
// open for hold, as a reader or a virus scanner can keep a released record
// open: until it lets go, the name stays and every open of it, a new record
// included, is refused with "Access is denied".
func deletePending(t *testing.T, path string, hold time.Duration) <-chan struct{} {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.DELETE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	isDeleted := byte(1)
	if err := windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &isDeleted, 1); err != nil {
		windows.CloseHandle(handle)
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(hold)
		windows.CloseHandle(handle)
		close(released)
	}()
	return released
}

// A holder that lets the wake lock go while another process still has its
// record open leaves the record being deleted until that process lets go.
// PR 402's CI (run 37552831952) lost a notify that way: the lock refused 10
// attempts to create a record in about half a second and the append gave up
// with most of its wait left.
func TestAppendWaitsOutAReleasedLockRecordStillBeingDeleted(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	holder := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	if _, err := lock.AcquireNamedOwner(dir, wakeLockName, holder.Process.Pid, "wake"); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(dir, wakeLockName)
	released := deletePending(t, record, 1500*time.Millisecond)
	if _, err := os.Lstat(record); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("the released record is not still being deleted, so this tests nothing: %v", err)
	}

	// Act
	_, err := Append(dir, "notify", "task", "working: task")
	<-released

	// Assert
	if err != nil {
		t.Fatalf("Append while the released wake lock's record was being deleted for 1.5 s: %v", err)
	}
	if records, err := Pending(dir); err != nil || len(records) != 1 {
		t.Errorf("the queue holds %v, %v; want the one notify", records, err)
	}
}
