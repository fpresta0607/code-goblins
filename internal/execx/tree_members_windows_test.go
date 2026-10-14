package execx

import (
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows counts a job's members and lists them in one call, and a member
// that starts or exits during it leaves the count one past the list, which
// a job whose members churn showed in about one read of 25,000 for up to half
// a millisecond. That is a race, not an unreadable job, so the list is read
// again until the two agree, and a job that never settles in the bound is
// unreadable.
func TestTreeReadsItsMembersAgainWhileTheyChange(t *testing.T) {
	for name, row := range map[string]struct {
		// counts is what the read numbered read counts and lists, with room
		// for that many ids.
		counts    func(read int, room uint32) (assigned, listed uint32)
		wantReads int
		wantIDs   int
	}{
		"members that changed during one read": {wantReads: 2, wantIDs: 2, counts: func(read int, room uint32) (uint32, uint32) {
			if read == 1 {
				return 3, 2
			}
			return 2, 2
		}},
		"more members than the list had room for": {wantReads: 2, wantIDs: 100, counts: func(read int, room uint32) (uint32, uint32) {
			return 100, min(100, room)
		}},
		"members that changed through every read": {wantReads: memberReadAttempts, counts: func(read int, room uint32) (uint32, uint32) {
			return 3, 2
		}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			reads := 0
			original := readJobMembers
			t.Cleanup(func() { readJobMembers = original })
			readJobMembers = func(_ windows.Handle, buffer []uintptr) error {
				reads++
				assigned, listed := row.counts(reads, uint32(len(buffer)-1))
				counts := (*[2]uint32)(unsafe.Pointer(&buffer[0]))
				counts[0], counts[1] = assigned, listed
				for index := range listed {
					buffer[1+index] = uintptr(1000 + index)
				}
				return nil
			}
			tree := &Tree{}

			// Act
			ids, err := tree.memberIDs()

			// Assert
			if reads != row.wantReads {
				t.Fatalf("the list was read %d time(s), want %d", reads, row.wantReads)
			}
			if row.wantIDs == 0 {
				if err == nil {
					t.Fatalf("members = %v, want an error for a list that never settled", ids)
				}
				return
			}
			if err != nil || len(ids) != row.wantIDs || !slices.Contains(ids, 1000) {
				t.Fatalf("members = %v, %v; want the %d members of the read that agreed", ids, err, row.wantIDs)
			}
		})
	}
}

// A job whose members start and exit all the time is measured on every
// sample, as a run's monitor samples go build's.
func TestTreeUsageReadsAJobWhoseMembersChurn(t *testing.T) {
	if os.Getenv("EXECX_TEST_CHURN") != "" {
		for deadline := time.Now().Add(4 * time.Second); time.Now().Before(deadline); {
			var children []*exec.Cmd
			for range 4 {
				child := exec.Command("cmd", "/d", "/c", "exit 0")
				if err := child.Start(); err == nil {
					children = append(children, child)
				}
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
	t.Cleanup(func() { _, _ = tree.End(); _ = command.Wait() })

	// Act
	samples := 0
	var failure error
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline) && failure == nil; samples++ {
		_, failure = tree.Usage()
	}

	// Assert
	if failure != nil {
		t.Fatalf("sample %d of a churning job = %v, want every sample read", samples, failure)
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
