package conpty

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/priority"
	"github.com/fpresta0607/code-goblins/internal/priority/prioritytest"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// A terminal's host runs one priority class above normal, and what it
// starts for its console's work must not: the console's program is a
// goblin's harness, and neither start here goes through execx. Windows gives
// the child of a raised process the normal class as long as its start names
// no class, so the program and the console server run at normal. The input
// waker beside the program in the console's job is the host's own control,
// whose start the host's record waits on (issue #891), and is started one
// class above the work.
func TestWhatARaisedHostStartsForItsConsoleRunsAtNormalButItsWaker(t *testing.T) {
	// Arrange
	prioritytest.FromNormal(t)
	defer priority.AboveTheWork()()
	if raised, err := windows.GetPriorityClass(windows.CurrentProcess()); err != nil || raised != windows.ABOVE_NORMAL_PRIORITY_CLASS {
		t.Fatalf("this process runs at priority class %#x (%v), so nothing here is started by a raised process", raised, err)
	}

	// Act
	console, _, server := startChildWithServer(t, Spec{Cols: 80, Rows: 25})
	// The console's job holds its program and its input waker.
	members := make([]uintptr, 64+2)
	if err := proc.QueryJobInformation(console.job, windows.JobObjectBasicProcessIdList, unsafe.Pointer(&members[0]), uintptr(len(members))*unsafe.Sizeof(uintptr(0))); err != nil {
		t.Fatal(err)
	}
	count := *(*uint32)(unsafe.Add(unsafe.Pointer(&members[0]), 4))
	pids := unsafe.Slice((*uintptr)(unsafe.Add(unsafe.Pointer(&members[0]), 8)), int(count))
	classes := map[string]uint32{}
	for _, pid := range pids {
		name := "the console's input waker"
		if int(pid) == console.PID() {
			name = "the console's program"
		}
		class, err := windows.GetPriorityClass(openProcess(t, int(pid)))
		if err != nil {
			t.Fatal(err)
		}
		classes[name] = class
	}
	class, err := windows.GetPriorityClass(server)
	if err != nil {
		t.Fatal(err)
	}
	classes["the console server"] = class

	// Assert
	for name, want := range map[string]uint32{"the console's program": windows.NORMAL_PRIORITY_CLASS, "the console's input waker": windows.ABOVE_NORMAL_PRIORITY_CLASS, "the console server": windows.NORMAL_PRIORITY_CLASS} {
		class, isRead := classes[name]
		if !isRead {
			t.Errorf("%s was not found among what the raised process started", name)
		} else if class != want {
			t.Errorf("%s runs at priority class %#x, want %#x", name, class, want)
		}
	}
}
