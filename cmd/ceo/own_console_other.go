//go:build !windows

package main

// leaveOwnConsole does nothing: only Windows gives a console program a
// window of its own.
func leaveOwnConsole([]string, bool) {}
