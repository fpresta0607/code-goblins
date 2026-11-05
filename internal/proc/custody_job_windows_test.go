package proc_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// A terminal's shim starts the harness just after the terminal records
// itself, so a custody read can begin with the job holding only the shim. The
// read took its members and then a second list, and that list named the
// harness, a live process born in the job since, as one it could not inspect:
// the read refused a live terminal (issue 1644). The process here is born
// after the read took its list and the machine's processes, as the harness
// was, because one that had only moved into the job would be found by name.
func TestCustodyReadJudgesTheListItsMembersCameFrom(t *testing.T) {
	// Arrange
	job, pids, _ := standinJob(t, "shim")
	programs := t.TempDir()
	standin.RemoveAtCleanup(t, programs)
	harness := filepath.Join(programs, "harness.exe")
	standin.Put(t, harness)
	born := 0
	proc.OpenJobProcessForTest(t, func(uint32) error {
		if born == 0 {
			born, _ = startInJob(t, job, harness)
		}
		return nil
	})

	// Act
	members, err := proc.CustodyJobProcesses(os.Getpid())

	// Assert
	if err != nil || len(members) != 1 || members[0].PID != pids["shim"] {
		t.Fatalf("a read the harness joined part way = %+v, %v; want the shim %d alone and no refusal", members, err, pids["shim"])
	}
	if born == 0 {
		t.Fatal("the harness was never born during the read, so the read met no late process")
	}
	later, err := proc.CustodyJobProcesses(os.Getpid())
	if err != nil || len(later) != 2 {
		t.Fatalf("the next read = %+v, %v; want the shim and the harness %d", later, err, born)
	}
}

// A job's list can still name a process that has exited while a handle holds
// it (issue 1430), and a close of the terminal has nothing of it left to end,
// so a custody read neither names it nor refuses for it. The list here names
// the process after it has exited, and the test holds it.
func TestCustodyReadSkipsAnExitedProcessItsJobStillLists(t *testing.T) {
	// Arrange
	job, pids, held := standinJob(t, "worker", "exited")
	proc.StaleListForTest(t)
	if ids, _, err := proc.JobProcessIDs(syscall.Handle(job)); err != nil || len(ids) != 2 {
		t.Fatalf("the job lists %v, %v; want the worker and the process to exit", ids, err)
	}
	if err := windows.TerminateProcess(held["exited"], 1); err != nil {
		t.Fatal(err)
	}
	if event, err := windows.WaitForSingleObject(held["exited"], 60000); err != nil || event != windows.WAIT_OBJECT_0 {
		t.Fatalf("the wait for the exit = %d, %v", event, err)
	}

	// Act
	members, err := proc.CustodyJobProcesses(os.Getpid())

	// Assert
	if err != nil || len(members) != 1 || members[0].PID != pids["worker"] {
		t.Fatalf("a read of a list that names an exited process = %+v, %v; want the worker %d alone and no refusal", members, err, pids["worker"])
	}
}

// What the read cannot open may still be there, and a terminal's close ends a
// process this one cannot inspect, so that alone still refuses the read.
func TestCustodyReadRefusesAListedProcessItCannotOpen(t *testing.T) {
	// Arrange
	_, pids, _ := standinJob(t, "worker", "sealed")
	proc.OpenJobProcessForTest(t, func(id uint32) error {
		if int(id) == pids["sealed"] {
			return windows.ERROR_ACCESS_DENIED
		}
		return nil
	})

	// Act
	members, err := proc.CustodyJobProcesses(os.Getpid())

	// Assert
	want := fmt.Sprintf("cannot inspect terminal job process %d", pids["sealed"])
	if err == nil || !strings.Contains(err.Error(), want) || members != nil {
		t.Fatalf("a read of a process it cannot open = %+v, %v; want a refusal that says %q", members, err, want)
	}
}
