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

// A member this process may not open is counted apart from the sampled
// working set and named as still running, whatever privilege the machine
// gives this process.
func TestTreeCountsAMemberItCannotOpen(t *testing.T) {
	if os.Getenv("EXECX_TEST_SLEEP") != "" {
		time.Sleep(time.Minute)
		os.Exit(9)
	}
	// Arrange
	command := Command(os.Args[0], "-test.run=^TestTreeCountsAMemberItCannotOpen$")
	command.Env = append(os.Environ(), "EXECX_TEST_SLEEP=1")
	tree, err := StartTree(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tree.End(); _ = command.Wait() })
	locked := uint32(command.Process.Pid)
	t.Cleanup(func() { openProcess = windows.OpenProcess })
	openProcess = func(access uint32, inherit bool, pid uint32) (windows.Handle, error) {
		if pid == locked {
			return 0, windows.ERROR_ACCESS_DENIED
		}
		return windows.OpenProcess(access, inherit, pid)
	}

	// Act
	usage, usageError := tree.Usage()
	members, membersError := tree.Members()

	// Assert
	if usageError != nil || usage.UnreadableMembers != 1 || usage.SampledPrivateWorkingSetBytes != 0 {
		t.Fatalf("usage = %+v, %v, want the member counted as unreadable and left out of the sample", usage, usageError)
	}
	if membersError != nil || len(members) != 1 || members[0].PID != int(locked) {
		t.Fatalf("members = %+v, %v, want the member named as still running", members, membersError)
	}
}
