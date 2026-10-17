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
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if threads := openThreads(t, pid); len(threads) >= 2 || time.Now().After(deadline) {
			if len(threads) < 2 {
				t.Fatalf("process %d runs %d threads after 10 s, want two at least", pid, len(threads))
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
		thread, err := windows.OpenThread(windows.THREAD_QUERY_LIMITED_INFORMATION|windows.THREAD_SET_LIMITED_INFORMATION, false, entry.ThreadID)
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

// Chrome runs its saving and the last thread each of its processes ends on
// at background priority, which waited tens of seconds for a processor
// beside the fleet's work (issue #896). A raise lifts a process below the
// normal class, and its threads below normal priority, to normal, and
// lowers nothing that runs above it.
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
	for thread, priority := range map[windows.Handle]int32{threads[0]: lowest, threads[1]: aboveNormal} {
		if result, _, err := setThreadPriority.Call(uintptr(thread), uintptr(priority)); result == 0 {
			t.Fatal(err)
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
}
