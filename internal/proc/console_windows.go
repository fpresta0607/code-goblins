package proc

import (
	"errors"
	"fmt"
	"slices"
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
	hostPID, err := ConsoleHost(pid)
	if err != nil {
		return false, err
	}
	switch {
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

// consoleWindowClasses are the classes of the windows a console shows
// through: a classic console host's own window, and the pseudo console
// window of a console handed to a terminal such as Windows Terminal, which
// the terminal follows. Windows says a console's window belongs to a process
// attached to the console, or to its console host.
var consoleWindowClasses = []string{"ConsoleWindowClass", "PseudoConsoleWindow"}

// showWindowAsync is user32's ShowWindowAsync, which x/sys/windows does not
// wrap. It posts the change to the window's thread rather than wait for it,
// so a console host that does not answer cannot hold its caller up.
var showWindowAsync = windows.NewLazySystemDLL("user32.dll").NewProc("ShowWindowAsync")

// windowHide is SW_HIDE.
const windowHide = 0

// HideConsole hides each console window that shows on the desktop for
// process pid's console, and says whether it hid any: the console host's
// own, or the pseudo console window of a console handed to Windows
// Terminal, which then minimizes the window it shows the console in. It
// hides no other window, and ends nothing: the process runs on, and a
// person can close it no more.
func HideConsole(pid int) (bool, error) {
	host, err := ConsoleHost(pid)
	if err != nil {
		return false, err
	}
	listed, err := topLevelWindows()
	if err != nil {
		return false, err
	}
	isHidden := false
	var problems error
	for _, each := range listed {
		if !each.IsShown || each.Owner != pid && (host == 0 || each.Owner != host) {
			continue
		}
		class := make([]uint16, 256)
		copied, err := windows.GetClassName(each.Handle, &class[0], int32(len(class)))
		if err != nil || !slices.Contains(consoleWindowClasses, windows.UTF16ToString(class[:copied])) {
			continue
		}
		// A window that closed since it was listed leaves its handle free
		// for the next window made, which may be another program's.
		var owner uint32
		if _, err := windows.GetWindowThreadProcessId(each.Handle, &owner); err != nil || int(owner) != each.Owner {
			continue
		}
		if posted, _, err := showWindowAsync.Call(uintptr(each.Handle), windowHide); posted == 0 {
			problems = errors.Join(problems, fmt.Errorf("proc: hide console window %#x of pid %d: %w", each.Handle, pid, err))
			continue
		}
		isHidden = true
	}
	return isHidden, problems
}

// ConsoleHost is the process id of the console host that the console
// process pid is attached to runs in, and 0 for a process with no console.
// Every process attached to one console reads the same host: a shell, the
// programs of its pipelines and what it runs in the background without a
// console of its own.
func ConsoleHost(pid int) (int, error) {
	var host uintptr
	if err := queryProcess(pid, windows.ProcessConsoleHostProcess, unsafe.Pointer(&host), unsafe.Sizeof(host)); err != nil {
		return 0, err
	}
	// The value's two low bits are flags and no part of the host's id.
	return int(host &^ 3), nil
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
