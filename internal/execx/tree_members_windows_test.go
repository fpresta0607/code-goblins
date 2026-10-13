package execx

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A process that has exited is no member that runs, though its object is
// still held and it was in the job.
func TestTreeHoldsNoProcessThatHasExited(t *testing.T) {
	// Arrange
	command := Command(os.Args[0], "-test.run=^$")
	tree, err := StartTree(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tree.End(); _ = command.Wait() })
	pid := uint32(command.Process.Pid)
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	if event, err := windows.WaitForSingleObject(process, uint32((10 * time.Second).Milliseconds())); err != nil || event != windows.WAIT_OBJECT_0 {
		t.Fatalf("the process did not exit: %d, %v", event, err)
	}

	// Act
	isRunning, err := tree.holds(pid)

	// Assert
	if err != nil || isRunning {
		t.Fatalf("holds = %v, %v, want an exited process left out", isRunning, err)
	}
}
