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
// supervisor or starts it, and says where its board is.
const goblinsName = "goblins.exe"

// saidLines is how many of the last lines goblins said the user is shown: room
// for all it says when a supervisor does not start, which is at most four
// lines of its own, saying what to do and where serve.log is, and then the
// last twelve lines of that log.
const saidLines = 16

// locate finds the board for the window program started alone, as the Start
// menu, the desktop and Start at login start it: it runs the goblins beside
// window with --window --locate, which finds the supervisor or starts it as
// goblins does in a terminal and prints the board's address and the fleet's
// state folder, one to a line. This program then shows that board itself, so
// the window stays the process the shell started, with the desktop's
// environment. The supervisor knows the board as the Overlord's own by the
// program this window runs, whatever started it.
// There stays one way to start a supervisor. goblins is a console program and
// this one has no console, so it is started with one that is never shown and
// opening the app shows no terminal. It returns what to tell the user when
// the board was not found, and nothing when it was.
func locate(window string) (board, stateDir, message string) {
	goblins := filepath.Join(filepath.Dir(window), goblinsName)
	if _, err := os.Stat(goblins); errors.Is(err, fs.ErrNotExist) {
		return "", "", "Code Goblins is not installed beside this window: " + goblins + " is missing.\n\nRun the Code Goblins install again."
	}
	command := execx.Command(goblins, "--window", "--locate")
	command.Dir = filepath.Dir(window)
	var found, said strings.Builder
	command.Stdout = &found
	command.Stderr = &said
	err := command.Run()
	if err == nil {
		if lines := strings.Split(lastLines(found.String(), 2), "\n"); len(lines) == 2 {
			return lines[0], lines[1], ""
		}
		err = errors.New(goblins + " did not say where the board is")
	}
	why := lastLines(said.String(), saidLines)
	if why == "" {
		why = err.Error()
	}
	return "", "", "Code Goblins could not open the board.\n\n" + why + "\n\nTo see more, open a terminal and run: goblins"
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
