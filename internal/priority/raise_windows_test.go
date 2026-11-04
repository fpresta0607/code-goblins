package priority

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// threadsOf opens every thread of the process pid for its priority to be
// read and set, once it runs two at least, and closes them when the test
// ends.
func threadsOf(t *testing.T, pid int) []windows.Handle {
	t.Helper()
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(50 * time.Millisecond) {
		if threads := openThreads(t, pid); len(threads) >= 2 || time.Now().After(deadline) {
			if len(threads) < 2 {
				t.Fatalf("process %d runs %d threads after a minute, want two at least", pid, len(threads))
			}
			return threads
		}
	}
}

func openThreads(t *testing.T, pid int) []windows.Handle {
	t.Helper()
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(snapshot)
	var threads []windows.Handle
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != uint32(pid) {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_QUERY_LIMITED_INFORMATION|windows.THREAD_SET_INFORMATION, false, entry.ThreadID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { windows.CloseHandle(thread) })
		threads = append(threads, thread)
	}
	return threads
}

var getThreadPriority = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetThreadPriority")

func threadPriority(thread windows.Handle) int32 {
	priority, _, _ := getThreadPriority.Call(uintptr(thread))
	return int32(priority)
}

func ioPriority(t *testing.T, thread windows.Handle) uint32 {
	t.Helper()
	var priority uint32
	if status, _, _ := ntQueryInformationThread.Call(uintptr(thread), threadIoPriority, uintptr(unsafe.Pointer(&priority)), unsafe.Sizeof(priority), 0); status != 0 {
		t.Fatalf("read a thread's I/O priority: NTSTATUS %#x", status)
	}
	return priority
}

// Chrome runs its saving and the last thread each of its processes ends on
// at background priority, which waited tens of seconds for a processor
// beside the fleet's work (issue #896), and issues that thread's I/O at very
// low priority, which held an ended Chrome's profile for minutes beside the
// same work (issue #2011). A raise lifts a process below the normal class,
// and its threads below normal priority or normal I/O priority, to normal,
// and lowers nothing that runs above it.
func TestRaiseLiftsWhatRunsBelowNormalAndLowersNothing(t *testing.T) {
	// Arrange
	member := exec.Command(os.Args[0])
	member.Env = append(os.Environ(), waitReport+"=1")
	member.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.BELOW_NORMAL_PRIORITY_CLASS}
	if err := member.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = member.Process.Kill(); _ = member.Wait() })
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(member.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	threads := threadsOf(t, member.Process.Pid)
	const lowest, aboveNormal = -2, 1
	const ioPriorityVeryLow = uint32(0)
	for thread, priority := range map[windows.Handle]int32{threads[0]: lowest, threads[1]: aboveNormal} {
		if result, _, err := setThreadPriority.Call(uintptr(thread), uintptr(priority)); result == 0 {
			t.Fatal(err)
		}
		low := ioPriorityVeryLow
		if status, _, _ := ntSetInformationThread.Call(uintptr(thread), threadIoPriority, uintptr(unsafe.Pointer(&low)), unsafe.Sizeof(low)); status != 0 {
			t.Fatalf("lower a thread's I/O priority: NTSTATUS %#x", status)
		}
	}

	// Act
	Raise([]uint32{uint32(member.Process.Pid)})

	// Assert
	if class, err := windows.GetPriorityClass(process); err != nil || class != windows.NORMAL_PRIORITY_CLASS {
		t.Errorf("class = %#x, %v; want the normal class", class, err)
	}
	if priority := threadPriority(threads[0]); priority != threadPriorityNormal {
		t.Errorf("the lowered thread runs at %d, want normal", priority)
	}
	if priority := threadPriority(threads[1]); priority != aboveNormal {
		t.Errorf("the raised thread runs at %d, want it left above normal", priority)
	}
	for name, thread := range map[string]windows.Handle{"lowered": threads[0], "raised": threads[1]} {
		if priority := ioPriority(t, thread); priority != ioPriorityNormal {
			t.Errorf("the %s thread's I/O runs at priority %d, want normal", name, priority)
		}
	}
}
