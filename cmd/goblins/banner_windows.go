package main

import (
	"io"
	"os"
	"syscall"
)

var setConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")

// consoleTakesEscapes reports whether out is a console that accepts ANSI
// sequences once asked to, whatever NO_COLOR says: lines are erased and
// replaced there, and never in a pipe or a file.
func consoleTakesEscapes(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	handle := syscall.Handle(file.Fd())
	var mode uint32
	if err := syscall.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	const enableVirtualTerminalProcessing = 0x0004
	if mode&enableVirtualTerminalProcessing != 0 {
		return true
	}
	enabled, _, _ := setConsoleMode.Call(uintptr(handle), uintptr(mode|enableVirtualTerminalProcessing))
	return enabled != 0
}
