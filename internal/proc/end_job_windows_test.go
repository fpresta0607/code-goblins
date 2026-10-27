package proc_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// Windows lists no process that a job nested in the one read is ending,
// from the moment that end begins until the process is done, and often
// counts it no more either: a headless Chrome puts most of its processes
// each in a job of its own, which ends them once the browser has ended, and
// each such process left the list with up to 21 threads not yet ended
// (issue 1066). An end that spares a machine service ends the rest one by
// one and its caller then lets the job's handle close without ending what
// it holds, so it reads the machine's processes for the job's members the
// list leaves out, and is done only once those have ended too. The worker
// here is one the list leaves out while it runs.
func TestEndJobBesideAMachineServiceEndsAProcessTheJobNoLongerLists(t *testing.T) {
	// Arrange
	job, pids, held := standinJob(t, "com.docker.standin", "worker")
	proc.LeaveOutForTest(t, pids["worker"])
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	ended, spared, err := proc.EndJob(ctx, job)

	// Assert
	if err != nil || len(ended) != 1 || ended[0].PID != pids["worker"] || len(spared) != 1 || spared[0].PID != pids["com.docker.standin"] {
		t.Fatalf("EndJob ended %+v and spared %+v, %v; want the worker %d ended and the machine service %d spared", ended, spared, err, pids["worker"], pids["com.docker.standin"])
	}
	if event, err := windows.WaitForSingleObject(held["worker"], 0); err != nil || event != windows.WAIT_OBJECT_0 {
		t.Fatal("the worker the job's list left out still runs after the job's end")
	}
	if event, err := windows.WaitForSingleObject(held["com.docker.standin"], 0); err != nil || event != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("the machine service was ended by an end that spares it")
	}
}

// On a machine whose cores other work fills, a process an end beside a
// machine service ends takes a while to finish exiting, and each of the
// end's own reads of the machine's processes can take seconds: a read begun
// as soon as it asked found the processes still ending, and the next began
// past the end's bound, so it named them as outliving it (issue 1275). The
// end reads again only once what it ended has exited, so a read that takes
// longer than its whole bound finds them gone.
func TestEndJobBesideAMachineServiceWaitsOutTheExitsItAskedForBeforeItReads(t *testing.T) {
	// Arrange
	job, pids, held := standinJob(t, "com.docker.standin", "worker")
	proc.LoadForTest(t, 500*time.Millisecond, proc.EndJobFor+500*time.Millisecond)

	// Act
	ended, spared, err := proc.EndJob(context.Background(), job)

	// Assert
	if err != nil || len(ended) != 1 || ended[0].PID != pids["worker"] || len(spared) != 1 || spared[0].PID != pids["com.docker.standin"] {
		t.Fatalf("EndJob ended %+v and spared %+v, %v; want the worker %d ended and the machine service %d spared", ended, spared, err, pids["worker"], pids["com.docker.standin"])
	}
	if event, err := windows.WaitForSingleObject(held["worker"], 0); err != nil || event != windows.WAIT_OBJECT_0 {
		t.Fatal("the worker still runs after the job's end")
	}
	if event, err := windows.WaitForSingleObject(held["com.docker.standin"], 0); err != nil || event != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("the machine service was ended by an end that spares it")
	}
}

// A job's list can still name a process that has exited while a handle holds
// it, and Windows still counts it in the job: an end beside a machine service
// that read the job again once what it ended had exited named the worker as a
// member to end once more, and reported it twice or as outliving the end
// (issue 1430). The list here names the worker after it has exited, and the
// test holds it.
func TestEndJobBesideAMachineServiceSkipsAnExitedProcessItsJobStillLists(t *testing.T) {
	// Arrange
	job, pids, _ := standinJob(t, "com.docker.standin", "worker")
	proc.StaleListForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	ended, spared, err := proc.EndJob(ctx, job)

	// Assert
	if err != nil || len(ended) != 1 || ended[0].PID != pids["worker"] || len(spared) != 1 || spared[0].PID != pids["com.docker.standin"] {
		t.Fatalf("EndJob ended %+v and spared %+v, %v; want the worker %d ended once and the machine service %d spared", ended, spared, err, pids["worker"], pids["com.docker.standin"])
	}
}

// A goblin's stop reads the jobs its terminal's host held (proc.HoldJobs)
// and ends their processes one by one beside a machine service, so the read
// names a process Windows still holds in a job but lists no more, as it does
// while a job nested in it ends a Chrome process (issue 1066).
func TestHeldJobsNameAProcessTheirJobNoLongerLists(t *testing.T) {
	// Arrange
	_, pids, _ := standinJob(t, "worker")
	proc.LeaveOutForTest(t, pids["worker"])
	held, err := proc.HoldJobs(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	// Act
	members, err := held.Processes()

	// Assert
	if err != nil || !slices.ContainsFunc(members, func(member proc.Entry) bool {
		return member.PID == pids["worker"] && strings.EqualFold(member.ExeBase, "worker.exe")
	}) {
		t.Fatalf("the held jobs' processes = %+v, %v; want the worker %d its job lists no more", members, err, pids["worker"])
	}
}

// standinJob starts a stand-in under each of names in a job of its own, each
// running until ended, and returns the job and each stand-in by name, by pid
// and held open.
func standinJob(t *testing.T, names ...string) (windows.Handle, map[string]int, map[string]windows.Handle) {
	t.Helper()
	programs := t.TempDir()
	standin.RemoveAtCleanup(t, programs)
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.TerminateJobObject(job, 1); _ = windows.CloseHandle(job) })
	pids := map[string]int{}
	held := map[string]windows.Handle{}
	for _, name := range names {
		program := filepath.Join(programs, name+".exe")
		standin.Put(t, program)
		started := exec.Command(program)
		started.Env = append(os.Environ(), standin.Env("", standin.Rule{Any: true, Hold: true})...)
		if err := started.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = started.Wait() })
		pids[name] = started.Process.Pid
		held[name] = standin.Hold(t, started.Process.Pid)
		process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(started.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		err = windows.AssignProcessToJobObject(job, process)
		windows.CloseHandle(process)
		if err != nil {
			t.Fatal(err)
		}
	}
	return job, pids, held
}
