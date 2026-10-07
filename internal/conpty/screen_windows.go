package conpty

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Reading a console's screen takes a process attached to the console, and the
// console's owner never attaches, since a Ctrl-C typed to the terminal and its
// closing can reach every process attached. The console's input waker is
// attached from the start, so it reads the screen when asked: the owner sends
// screenAsked among the bytes that tell of input written, and the waker
// answers with a line of its report. A read is then a message to a process
// already running, never a process of its own, which on a machine slow to
// start processes took seconds.

const (
	// inputWritten, sent to the waker, tells it of input written to the
	// console, and screenAsked asks it for the console's screen.
	inputWritten byte = 1
	screenAsked  byte = 2
)

// screenAnswerPrefix starts each line of the waker's report that answers for
// the screen, followed by how many asks it answers, a space and the answer.
const screenAnswerPrefix = "screen "

// screenAnswer is the waker's answer for the screen: the rows of the
// console's window, or why they could not be read. asks is how many asks the
// waker had when it read them.
type screenAnswer struct {
	Rows  []string `json:"rows,omitempty"`
	Error string   `json:"error,omitempty"`
	asks  uint64
}

// Screen reads the console's screen as the console holds it: one string per
// row of its window, each as wide as the window. A read the console's input
// waker cannot answer, or does not answer within timeout, is an error, never
// an empty screen.
func (c *Console) Screen(timeout time.Duration) ([]string, error) {
	return c.waker.screen(timeout)
}

// screen asks the waker for the console's screen and waits up to timeout for
// an answer it read after the ask arrived. Asks are numbered in the order
// they are sent, which is the order the waker counts them in, so an answer
// that came too late for one read never answers a later one.
func (w *waker) screen(timeout time.Duration) ([]string, error) {
	w.asking.Lock()
	_, err := w.typed.Write([]byte{screenAsked})
	if err == nil {
		w.asks++
	}
	ask := w.asks
	w.asking.Unlock()
	if err != nil {
		return nil, fmt.Errorf("conpty: ask the console's input waker for its screen: %w", err)
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		w.answering.Lock()
		answer, isEnded, answered := w.answer, w.isEnded, w.answered
		w.answering.Unlock()
		switch {
		case answer.asks >= ask && answer.Error != "":
			return nil, fmt.Errorf("conpty: the console's input waker could not read its screen: %s", answer.Error)
		case answer.asks >= ask:
			return answer.Rows, nil
		case isEnded:
			return nil, errors.New("conpty: the console's input waker has ended, so nothing reads its screen")
		}
		select {
		case <-answered:
		case <-deadline.C:
			return nil, fmt.Errorf("conpty: the console's input waker did not read its screen within %s", timeout)
		}
	}
}

// take keeps line as the waker's latest answer for the screen, when it is
// one, and tells the reads waiting. It reports whether line was an answer.
func (w *waker) take(line string) bool {
	rest, isAnswer := strings.CutPrefix(line, screenAnswerPrefix)
	if !isAnswer {
		return false
	}
	count, payload, _ := strings.Cut(rest, " ")
	asks, err := strconv.ParseUint(count, 10, 64)
	if err != nil {
		return false
	}
	var answer screenAnswer
	if json.Unmarshal([]byte(payload), &answer) != nil {
		return false
	}
	answer.asks = asks
	w.answering.Lock()
	defer w.answering.Unlock()
	w.answer = answer
	close(w.answered)
	w.answered = make(chan struct{})
	return true
}

// end tells the reads waiting, and every read after, that the waker has
// ended and answers no more.
func (w *waker) end() {
	w.answering.Lock()
	defer w.answering.Unlock()
	w.isEnded = true
	close(w.answered)
}

// answerScreens is the waker's end: it reads the screen each time asked
// signals, once for every ask that came while it read the last time, and
// says how many asks had come when it read.
func answerScreens(asked <-chan struct{}, asks *atomic.Uint64, report io.Writer) {
	for range asked {
		answer := screenAnswer{asks: asks.Load()}
		var err error
		if answer.Rows, err = consoleScreen(); err != nil {
			answer.Error = err.Error()
		}
		// Rows and an error are strings, which always encode.
		payload, _ := json.Marshal(answer)
		fmt.Fprintf(report, "%s%d %s\n", screenAnswerPrefix, answer.asks, payload)
	}
}

// olderHostScreenRole, as a program's first argument, makes a program built
// with this package read the screen of the console the pid after it runs in
// and exit, instead of running. A host started by a cfo from before input
// wakers read screens starts its own program that way for every read, and an
// update puts the new build at that program's path while such a host runs
// on, so every build answers it.
const olderHostScreenRole = "cfo-host-screen"

var (
	attachConsole              = kernel32.NewProc("AttachConsole")
	setConsoleCtrlHandler      = kernel32.NewProc("SetConsoleCtrlHandler")
	readConsoleOutputCharacter = kernel32.NewProc("ReadConsoleOutputCharacterW")
)

func init() {
	if len(os.Args) > 1 && os.Args[0] == olderHostScreenRole {
		os.Exit(printScreen(os.Args[1]))
	}
}

// printScreen is the screen reader an older host starts: it attaches this
// process to the console the pid it is given runs in and prints the
// console's screen as JSON.
func printScreen(pidArgument string) int {
	pid, err := strconv.Atoi(pidArgument)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	// A Ctrl-C typed to the terminal reaches every process attached to its
	// console. This one ignores it and finishes the read. A handler would not
	// do: attaching drops the handlers registered before, Go's own included.
	_, _, _ = setConsoleCtrlHandler.Call(0, 1)
	if attached, _, err := attachConsole.Call(uintptr(pid)); attached == 0 {
		fmt.Fprintf(os.Stderr, "attach to the console of pid %d: %v\n", pid, err)
		return 1
	}
	rows, err := consoleScreen()
	if err != nil {
		fmt.Fprintf(os.Stderr, "read the console of pid %d: %v\n", pid, err)
		return 1
	}
	if err := json.NewEncoder(os.Stdout).Encode(rows); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// consoleScreen reads the rows of the window of this process's console, from
// its active screen buffer.
func consoleScreen() ([]string, error) {
	name, err := windows.UTF16PtrFromString("CONOUT$")
	if err != nil {
		return nil, err
	}
	out, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(out)
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(out, &info); err != nil {
		return nil, err
	}
	width := int(info.Window.Right-info.Window.Left) + 1
	cells := make([]uint16, width)
	var rows []string
	for y := info.Window.Top; y <= info.Window.Bottom; y++ {
		// The row's first cell, as the COORD the call takes by value.
		at := uint32(uint16(info.Window.Left)) | uint32(uint16(y))<<16
		var read uint32
		if ok, _, err := readConsoleOutputCharacter.Call(uintptr(out), uintptr(unsafe.Pointer(&cells[0])), uintptr(width), uintptr(at), uintptr(unsafe.Pointer(&read))); ok == 0 {
			return nil, err
		}
		rows = append(rows, string(utf16.Decode(cells[:read])))
	}
	return rows, nil
}
