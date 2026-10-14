package execx

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A job whose members start and exit all the time is measured on every
// sample, as a run's monitor samples go build's: on 2026-10-14 a run was cut
// with "incomplete job member sample" while its list of members changed as it
// was read. proc.JobProcessIDs proves each rule of that read with a stand-in;
// this samples a real job, which the old read failed within a second. The
// job is sampled from its start until its root exits, so every member it
// started was started and exited while it was sampled, however long a busy
// machine takes to start the root.
func TestTreeUsageReadsAJobWhoseMembersChurn(t *testing.T) {
	const rounds, perRound = 10, 4
	if os.Getenv("EXECX_TEST_CHURN") != "" {
		for range rounds {
			var children []*exec.Cmd
			for range perRound {
				child := exec.Command("cmd", "/d", "/c", "exit 0")
				if err := child.Start(); err != nil {
					os.Exit(7)
				}
				children = append(children, child)
			}
			for _, child := range children {
				_ = child.Wait()
			}
		}
		os.Exit(0)
	}
	// Arrange
	command := Command(os.Args[0], "-test.run=^TestTreeUsageReadsAJobWhoseMembersChurn$")
	command.Env = append(os.Environ(), "EXECX_TEST_CHURN=1")
	tree, err := StartTree(command)
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	t.Cleanup(func() { _, _ = tree.End(); <-exited })

	// Act
	samples := 0
	var failure error
	isRunning := true
	// The bound only fails a root that never exits; the churn takes a second
	// or two on an idle machine.
	for bound := time.After(2 * time.Minute); isRunning && failure == nil; samples++ {
		select {
		case err := <-exited:
			exited <- err
			isRunning = false
		case <-bound:
			t.Fatalf("the churning root did not exit within its bound, after %d samples", samples)
		default:
		}
		_, failure = tree.Usage()
	}
	usage, usageError := tree.Usage()

	// Assert
	if failure != nil || usageError != nil {
		t.Fatalf("sample %d of a churning job = %v, %v, want every sample read", samples, failure, usageError)
	}
	// The job's lifetime count shows its members did churn while it was
	// sampled, so the samples above read a changing list.
	if want := uint32(1 + rounds*perRound); usage.TotalProcesses < want {
		t.Fatalf("the job held %d processes in all, want at least %d started and exited while it was sampled", usage.TotalProcesses, want)
	}
}

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
