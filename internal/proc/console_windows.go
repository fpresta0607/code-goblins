package proc

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ConsoleShown says the console process pid is attached to shows on the
// desktop, given the desktop's window owners (WindowOwners): its console
// host owns a visible window, as a classic console window's conhost does,
// or the program that started the host does, as Windows Terminal does for a
// console it shows in a tab of its window. A process with no console, and
// one in a pseudo console such as a native terminal's, shows none.
func ConsoleShown(pid int, windowOwners map[int]bool) (bool, error) {
	var host uintptr
	if err := queryProcess(pid, windows.ProcessConsoleHostProcess, unsafe.Pointer(&host), unsafe.Sizeof(host)); err != nil {
		return false, err
	}
	// The value's two low bits are flags and no part of the host's id.
	switch hostPID := int(host &^ 3); {
	case hostPID == 0:
		return false, nil
	case windowOwners[hostPID]:
		return true, nil
	default:
		var basic windows.PROCESS_BASIC_INFORMATION
		if err := queryProcess(hostPID, windows.ProcessBasicInformation, unsafe.Pointer(&basic), unsafe.Sizeof(basic)); err != nil {
			return false, err
		}
		return windowOwners[int(basic.InheritedFromUniqueProcessId)], nil
	}
}

// queryProcess reads one class of what Windows keeps of process pid into
// the size bytes at into.
func queryProcess(pid int, class int32, into unsafe.Pointer, size uintptr) error {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("proc: open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(handle)
	if err := windows.NtQueryInformationProcess(handle, class, into, uint32(size), nil); err != nil {
		return fmt.Errorf("proc: read process %d: %w", pid, err)
	}
	return nil
}
