//go:build !windows

package main

// takeBackCtrlC has nothing to take back: this test binary does not ignore
// SIGINT, so neither does what it starts.
func takeBackCtrlC() bool { return true }

// consoleWindow is 0: no process here is given a console window.
func consoleWindow() uintptr { return 0 }
