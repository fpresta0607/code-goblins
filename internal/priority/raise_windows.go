package priority

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var setThreadPriority = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadPriority")

// normalPriority is the priority a thread of a process in the normal class
// runs at by default, and threadPriorityNormal is SetThreadPriority's value
// for it.
const (
	normalPriority       = 8
	threadPriorityNormal = 0
)

// Raise runs each of the processes pids, and every thread of them, at
// normal priority at least: a process below the normal class is put in it,
// and a thread whose base priority is below normal is set to normal in its
// process's class. A program's background threads run at priority 4, and on
// a machine whose cores other work fills at normal priority they wait tens
// of seconds for a processor, so a process whose exit waits on them, as
// each process's end waits on its last thread, takes as long to end. A
// thread's base priority is read from the machine's list of threads, which
// still gives it for the last thread of a process that has exited, whose
// own priority can no longer be read, and which an ended job no longer
// lists among its processes. A process or thread that ended before it was
// raised needs nothing, and one that cannot be opened is left as it is, as
// is every one when the machine's threads cannot be listed: a raise only
// hurries an end, which waits for them either way.
func Raise(pids []uint32) {
	members := map[uint32]bool{}
	for _, pid := range pids {
		members[pid] = true
		raiseClass(pid)
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if members[entry.OwnerProcessID] && entry.BasePri < normalPriority {
			raiseThread(entry.ThreadID)
		}
	}
}

func raiseClass(pid uint32) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_SET_INFORMATION, false, pid)
	if err != nil {
		return
	}
	defer windows.CloseHandle(process)
	if class, err := windows.GetPriorityClass(process); err == nil && (class == windows.IDLE_PRIORITY_CLASS || class == windows.BELOW_NORMAL_PRIORITY_CLASS) {
		_ = windows.SetPriorityClass(process, windows.NORMAL_PRIORITY_CLASS)
	}
}

func raiseThread(id uint32) {
	thread, err := windows.OpenThread(windows.THREAD_SET_LIMITED_INFORMATION, false, id)
	if err != nil {
		return
	}
	defer windows.CloseHandle(thread)
	setThreadPriority.Call(uintptr(thread), threadPriorityNormal)
}
