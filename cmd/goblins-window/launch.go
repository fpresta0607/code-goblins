package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"golang.org/x/sys/windows"
)

// goblinsName is the program an install puts beside this one: it finds the
// supervisor or starts it, and starts this window on its board.
const goblinsName = "goblins.exe"

// saidLines is how many of the last lines goblins said the user is shown.
const saidLines = 12

// launch opens the app from the window program alone, as the Start menu and
// Start at login start it: it runs the goblins beside window with --window,
// which finds the supervisor or starts it as goblins does in a terminal and
// then starts this program on the board, in the tray alone with background.
// There stays one way to start a supervisor. goblins is a console program and
// this one has no console, so it is started with one that is never shown and
// opening the app shows no terminal. It returns what to tell the user when
// the board did not open, and nothing when it did.
func launch(window string, background bool) string {
	goblins := filepath.Join(filepath.Dir(window), goblinsName)
	if _, err := os.Stat(goblins); errors.Is(err, fs.ErrNotExist) {
		return "Code Goblins is not installed beside this window: " + goblins + " is missing.\n\nRun the Code Goblins install again."
	}
	args := []string{"--window"}
	if background {
		args = append(args, "--background")
	}
	command := execx.Command(goblins, args...)
	command.Dir = filepath.Dir(window)
	var said strings.Builder
	command.Stderr = &said
	err := command.Run()
	if err == nil {
		return ""
	}
	why := lastLines(said.String(), saidLines)
	if why == "" {
		why = err.Error()
	}
	return "Code Goblins could not open the board.\n\n" + why + "\n\nTo see more, open a terminal and run: goblins"
}

// lastLines returns the last count lines of text that say anything.
func lastLines(text string, count int) string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}

// tell shows message in a box of its own. This program has no console, so
// what it writes to stderr goes nowhere when the Start menu starts it.
func tell(message string) {
	text, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return
	}
	caption, _ := windows.UTF16PtrFromString("Code Goblins")
	_, _ = windows.MessageBox(0, text, caption, windows.MB_OK|windows.MB_ICONERROR)
}
