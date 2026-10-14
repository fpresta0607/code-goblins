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
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows counts a job's processes and lists them in one call, and a process
// that starts or exits during it can leave the count and the list apart: a
// job whose processes churned showed it in about one read of 25,000, for up
// to half a millisecond. The list is read again until they agree, its room
// grows when Windows says the list does not fit, and a job whose list never
// agrees, or never fits its bound, is unreadable rather than taken short.
func TestJobProcessIDsReadsTheListAgainUntilItsCountAgrees(t *testing.T) {
	header := int(8 / unsafe.Sizeof(uintptr(0)))
	tooLong := syscall.Errno(errorMoreData)
	for name, row := range map[string]struct {
		// read is what read number read of the list finds, with room for
		// that many ids: the count, the ids it lists, or an error.
		read      func(read int, room uint32) (counted, listed uint32, err error)
		wantReads int
		wantIDs   int
		wantError string
	}{
		"a list that changed during one read": {wantReads: 2, wantIDs: 2, read: func(read int, room uint32) (uint32, uint32, error) {
			if read == 1 {
				return 3, 2, nil
			}
			return 2, 2, nil
		}},
		"a list too long for its room": {wantReads: 2, wantIDs: 100, read: func(read int, room uint32) (uint32, uint32, error) {
			if room < 100 {
				return 0, 0, tooLong
			}
			return 100, 100, nil
		}},
		"a count past its room with no error": {wantReads: 2, wantIDs: 100, read: func(read int, room uint32) (uint32, uint32, error) {
			return 100, min(100, room), nil
		}},
		"a list too long for every room": {wantReads: 6, wantError: "exceeds its bound", read: func(read int, room uint32) (uint32, uint32, error) {
			return 0, 0, tooLong
		}},
		"a list that changed through every read": {wantError: "changed through every read", read: func(read int, room uint32) (uint32, uint32, error) {
			return 3, 2, nil
		}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			reads := 0
			original := readJobList
			t.Cleanup(func() { readJobList = original })
			readJobList = func(_ syscall.Handle, buffer []uintptr) error {
				reads++
				counted, listed, err := row.read(reads, uint32(len(buffer)-header))
				if err != nil {
					return err
				}
				counts := (*[2]uint32)(unsafe.Pointer(&buffer[0]))
				counts[0], counts[1] = counted, listed
				for index := range int(listed) {
					buffer[header+index] = uintptr(reads*1000 + index)
				}
				return nil
			}

			// Act
			ids, err := JobProcessIDs(0)

			// Assert
			if row.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), row.wantError) || (row.wantReads != 0 && reads != row.wantReads) || reads < 2 {
					t.Fatalf("JobProcessIDs = %v, %v after %d read(s); want an error that says %q after %d", ids, err, reads, row.wantError, row.wantReads)
				}
				return
			}
			if err != nil || reads != row.wantReads || len(ids) != row.wantIDs || ids[0] != uint32(reads*1000) {
				t.Fatalf("JobProcessIDs = %v, %v after %d read(s); want the %d ids of read %d, the one that agreed", ids, err, reads, row.wantIDs, row.wantReads)
			}
		})
	}
}

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

// TestJobMemberFixture is a process a job holds, which waits to be ended.
func TestJobMemberFixture(t *testing.T) {
	if os.Getenv("CFO_JOB_MEMBER_FIXTURE") != "1" {
		return
	}
	time.Sleep(time.Minute)
}

// A member is ended only while its pid still names the process that started
// when the member did, so a pid Windows gave a later process in the job, such
// as one a spared service started, never ends that process.
func TestEndJobMemberEndsOnlyTheProcessItListed(t *testing.T) {
	// Arrange
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.TerminateJobObject(job, 1); _ = windows.CloseHandle(job) })
	member := exec.Command(os.Args[0], "-test.run=^TestJobMemberFixture$")
	member.Env = append(os.Environ(), "CFO_JOB_MEMBER_FIXTURE=1")
	if err := member.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = member.Process.Kill(); _ = member.Wait() })
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(member.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		t.Fatal(err)
	}
	members, err := jobEntries([]syscall.Handle{syscall.Handle(job)})
	if err != nil || len(members) != 1 {
		t.Fatalf("members = %+v, %v; want the one process", members, err)
	}
	reused := members[0]
	reused.Start = reused.Start.Add(-time.Second)

	// Act
	isEnded := endJobMember(job, reused)

	// Assert
	if event, err := windows.WaitForSingleObject(process, 500); isEnded || err != nil || event != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("ended = %v, wait = %d, %v; want a process that started at another time left running", isEnded, event, err)
	}
	if !endJobMember(job, members[0]) {
		t.Fatal("the process the job listed was not ended")
	}
}
