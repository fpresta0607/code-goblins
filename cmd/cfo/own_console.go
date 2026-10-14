package main

import "strings"

// isShownOnPurpose reports whether args are a command the Overlord starts
// to look at, which keeps its console even when it is the only process in
// it, as a start from the Start menu, the desktop or the Run box is: the
// quick start and its other forms (goblins setup, goblins resume and the
// goblins flags), cfo attach, and cfo with no command, which prints its
// usage. Every other command started alone in a new console was started by
// a program, not by him (leaveOwnConsole).
func isShownOnPurpose(args []string, isGoblins bool) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "attach", "setup", "resume":
		return true
	}
	return isGoblins && strings.HasPrefix(args[0], "-")
}
