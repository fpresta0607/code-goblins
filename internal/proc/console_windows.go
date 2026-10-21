package proc

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
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
// the terminal follows.
var consoleWindowClasses = []string{"ConsoleWindowClass", "PseudoConsoleWindow"}

var (
	attachConsole         = kernel32.NewProc("AttachConsole")
	getConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	setConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
	// showWindowAsync is user32's ShowWindowAsync, which x/sys/windows does
	// not wrap. It posts the change to the window's thread rather than wait
	// for it, so a console host that does not answer cannot hold its caller
	// up.
	showWindowAsync = windows.NewLazySystemDLL("user32.dll").NewProc("ShowWindowAsync")
)

// windowHide is SW_HIDE.
const windowHide = 0

// ConsoleWindowRole, as a program's first argument, makes a program built
// with this package print the window of the console the pid after it runs
// in, and exit, instead of running. Started with no console of its own, it
// can attach to another's, which is the one way Windows names a console's
// window: a console handed to Windows Terminal shows through a pseudo
// console window that neither the console's processes nor its console host
// own, but a process of another console host Windows Terminal started.
const ConsoleWindowRole = "cfo-console-window"

func init() {
	if len(os.Args) == 2 && os.Args[0] == ConsoleWindowRole {
		os.Exit(printConsoleWindow(os.Args[1]))
	}
}

// printConsoleWindow attaches this process to the console the pid it is
// given runs in and prints the console's window, 0 for a process with no
// console.
func printConsoleWindow(pidArgument string) int {
	pid, err := strconv.Atoi(pidArgument)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	// A Ctrl-C typed to the console reaches every process attached to it.
	// This one ignores it and finishes the read.
	_, _, _ = setConsoleCtrlHandler.Call(0, 1)
	if attached, _, err := attachConsole.Call(uintptr(pid)); attached == 0 {
		if errors.Is(err, windows.ERROR_INVALID_HANDLE) {
			fmt.Println(0)
			return 0
		}
		fmt.Fprintf(os.Stderr, "attach to the console of pid %d: %v\n", pid, err)
		return 1
	}
	window, _, _ := getConsoleWindow.Call()
	fmt.Println(window)
	return 0
}

// HideConsoleWindow hides window, a console's window as a program started
// as ConsoleWindowRole printed it, when it shows on the desktop, and says
// whether it hid it: a console host's own window, or the pseudo console
// window of a console handed to Windows Terminal, which then minimizes the
// window it shows the console in. It ends nothing: the console's programs
// run on, and a person can close it no more.
func HideConsoleWindow(window windows.HWND) (bool, error) {
	if window == 0 || !windows.IsWindowVisible(window) {
		return false, nil
	}
	// A window that closed since it was read leaves its handle free for the
	// next window made, which may be another program's.
	class := make([]uint16, 256)
	copied, err := windows.GetClassName(window, &class[0], int32(len(class)))
	if err != nil || !slices.Contains(consoleWindowClasses, windows.UTF16ToString(class[:copied])) {
		return false, nil
	}
	if posted, _, err := showWindowAsync.Call(uintptr(window), windowHide); posted == 0 {
		return false, fmt.Errorf("proc: hide console window %#x: %w", window, err)
	}
	return true, nil
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

// RunsAbove says process pid runs at a higher integrity level than this
// process, as a program the Overlord let run as an administrator runs above
// the fleet. Windows lets no process attach to the console of one that runs
// above it.
func RunsAbove(pid int) (bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false, fmt.Errorf("proc: open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(handle)
	var token windows.Token
	if err := windows.OpenProcessToken(handle, windows.TOKEN_QUERY, &token); err != nil {
		return false, fmt.Errorf("proc: open process %d's token: %w", pid, err)
	}
	defer token.Close()
	level, err := integrityLevel(token)
	if err != nil {
		return false, fmt.Errorf("proc: read process %d's integrity level: %w", pid, err)
	}
	own, err := integrityLevel(windows.GetCurrentProcessToken())
	if err != nil {
		return false, fmt.Errorf("proc: read this process's integrity level: %w", err)
	}
	return level > own, nil
}

// integrityLevel is the integrity level token runs at, the last part of its
// mandatory label's SID: 0x2000 for medium and 0x3000 for high.
func integrityLevel(token windows.Token) (uint32, error) {
	// The label and the one-part SID it points at, which follows it.
	buffer := make([]byte, 64)
	var size uint32
	if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &buffer[0], uint32(len(buffer)), &size); err != nil {
		return 0, err
	}
	sid := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buffer[0])).Label.Sid
	return sid.SubAuthority(uint32(sid.SubAuthorityCount()) - 1), nil
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
