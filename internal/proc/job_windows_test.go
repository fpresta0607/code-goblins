package proc

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestJobHolderFixture is a process holding a job open, which waits to be
// ended.
func TestJobHolderFixture(t *testing.T) {
	if os.Getenv("CFO_JOB_HOLDER_FIXTURE") != "1" {
		return
	}
	if job, _, err := procCreateJobObjectW.Call(0, 0); job == 0 {
		t.Fatal(err)
	}
	fmt.Println("holding")
	time.Sleep(time.Minute)
}

// A process that has ended holds no job any more. Reading a process's jobs
// copies the system's handle table and then each job handle the table lists
// for it, and a holder that ends in between is still in the copied table but
// refuses its handles. On 2026-10-09 a goblin's stop read its terminal
// host's jobs just after ending the host, met exactly that, and was refused
// with "Access is denied", so the sweep for the goblin's other processes
// never ran (CI run 37985536621 failed on it). Here the holder ends between
// the table and the handle: its jobs are none, never an error.
func TestAHolderThatEndsWhileItsJobsAreReadHoldsNone(t *testing.T) {
	// Arrange
	holder := exec.Command(os.Args[0], "-test.run=^TestJobHolderFixture$")
	holder.Env = append(os.Environ(), "CFO_JOB_HOLDER_FIXTURE=1")
	output, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || strings.TrimSpace(line) != "holding" {
		t.Fatalf("the holder fixture said %q, %v; want holding", line, err)
	}
	// A handle of the test's own keeps the pid the holder's once it has ended.
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(holder.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	copies := 0
	endThenCopy := func(source, handle, target syscall.Handle, copied *syscall.Handle, access uint32, inherit bool, options uint32) error {
		copies++
		if err := holder.Process.Kill(); err != nil {
			t.Errorf("end the holder: %v", err)
		}
		if event, err := windows.WaitForSingleObject(process, 30000); err != nil || event != windows.WAIT_OBJECT_0 {
			t.Errorf("the holder had not ended after 30 s: event %d, %v", event, err)
		}
		return syscall.DuplicateHandle(source, handle, target, copied, access, inherit, options)
	}

	// Act
	jobs, err := heldJobsBy(holder.Process.Pid, jobObjectQuery, endThenCopy)

	// Assert
	closeAll(jobs)
	if copies != 1 {
		t.Fatalf("the read copied %d of the holder's handles, want its one job, so the holder never ended under the read", copies)
	}
	if err != nil || len(jobs) != 0 {
		t.Fatalf("a holder that ended under the read holds %d jobs, %v; want none and no error", len(jobs), err)
	}
}

func TestJobProcessStartRejectsAPIDThatNoLongerBelongsToTheJob(t *testing.T) {
	job, _, err := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(syscall.Handle(job))
	// A PID from an earlier job snapshot can now name an unrelated process.
	if started, belongs := jobProcessStart(syscall.Handle(job), uint32(os.Getpid())); belongs || !started.IsZero() {
		t.Fatalf("unrelated replacement authorized by old membership: %v %v", started, belongs)
	}
}
