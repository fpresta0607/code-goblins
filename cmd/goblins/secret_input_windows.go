package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"golang.org/x/sys/windows"
)

// readHiddenLine reads one line typed at the console without showing it,
// after a prompt naming what it reads. It reports false, reading nothing,
// when stdin is not a console: a pipe or a file is read as before.
func readHiddenLine(prompt io.Writer, label string) (string, bool, error) {
	handle := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) != nil {
		return "", false, nil
	}
	if err := windows.SetConsoleMode(handle, mode&^windows.ENABLE_ECHO_INPUT|windows.ENABLE_LINE_INPUT|windows.ENABLE_PROCESSED_INPUT); err != nil {
		return "", true, err
	}
	restore := func() { _ = windows.SetConsoleMode(handle, mode) }
	defer restore()
	// Ctrl-C ends the command, and the console gets its echo back first.
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	defer signal.Stop(interrupted)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-interrupted:
			restore()
			os.Exit(130)
		case <-done:
		}
	}()
	fmt.Fprintf(prompt, "%s (typing is hidden): ", label)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Fprintln(prompt)
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", true, err
	}
	return strings.TrimRight(line, "\r\n"), true, nil
}
