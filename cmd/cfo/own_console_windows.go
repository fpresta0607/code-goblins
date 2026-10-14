package main

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	consoleWindowProc      = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")
	consoleProcessListProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList")
	freeConsoleProc        = windows.NewLazySystemDLL("kernel32.dll").NewProc("FreeConsole")
)

// leaveOwnConsole takes this process out of a console that Windows made for
// it alone, before it does anything else, unless args are a command the
// Overlord starts to look at (isShownOnPurpose). A console program started
// by a process that gives it no console, as Win32_Process Create, a
// Start-Process at the normal window style, cmd /c start and Git Bash's
// start do, gets a new console with a window of its own, which shows on the
// desktop for as long as the program runs, blank when its output goes to a
// file, and ends the program when he closes it. A console Windows hands to
// Windows Terminal, the default terminal of Windows 11, shows as a window of
// Windows Terminal instead. Hiding the console's window hides a console
// host's but only minimizes Windows Terminal's, so the process leaves the
// console: with no process left in it, the console host ends and its window
// closes, and Windows Terminal closes the window it showed it in, measured
// on a hosted runner at about a tenth of a second.
//
// A console another process shares, such as the terminal he typed the
// command in, or one with no window, such as the hidden console every fleet
// process starts its children in, is left alone. The process goes on with no
// console: the programs it starts through execx get a console of their own
// with no window (execx.hide), and its standard handles that were the
// console's read and write the null device instead, where what it writes
// was going to a window nobody was meant to see.
func leaveOwnConsole(args []string, isGoblins bool) {
	if isShownOnPurpose(args, isGoblins) {
		return
	}
	if window, _, _ := consoleWindowProc.Call(); window == 0 {
		return
	}
	processes := make([]uint32, 2)
	if count, _, _ := consoleProcessListProc.Call(uintptr(unsafe.Pointer(&processes[0])), uintptr(len(processes))); count != 1 {
		return
	}
	leaveConsole()
}

// leaveConsole takes this process out of its console, and points its
// standard input, output and error that were the console's at the null
// device instead.
func leaveConsole() {
	standard := []struct {
		id   uint32
		file **os.File
		flag int
	}{
		{windows.STD_INPUT_HANDLE, &os.Stdin, os.O_RDONLY},
		{windows.STD_OUTPUT_HANDLE, &os.Stdout, os.O_WRONLY},
		{windows.STD_ERROR_HANDLE, &os.Stderr, os.O_WRONLY},
	}
	var isConsole [3]bool
	for at, handle := range standard {
		var mode uint32
		isConsole[at] = windows.GetConsoleMode(windows.Handle((*handle.file).Fd()), &mode) == nil
	}
	if freed, _, _ := freeConsoleProc.Call(); freed == 0 {
		return
	}
	for at, handle := range standard {
		if !isConsole[at] {
			continue
		}
		null, err := os.OpenFile(os.DevNull, handle.flag, 0)
		if err != nil {
			continue
		}
		*handle.file = null
		_ = windows.SetStdHandle(handle.id, windows.Handle(null.Fd()))
	}
}
