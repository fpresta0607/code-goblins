package conpty

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A program that is busy leaves what was typed unread in its console's input:
// Claude Code reads nothing for a second or more while it starts a tool, and
// a shell reads nothing while a command runs. A key pressed then changes
// nothing on the screen until the program reads it, and pressed again it is
// typed twice, which in a screen where the key switches something on and off
// undoes it. No wake helps a program that is not waiting to read. The
// console's input waker, attached to the console, watches what was typed
// until it is read, and says when key presses have sat unread for
// unreadAfter and when they were read, so a viewer can say so.

// keysUnreadLine and keysReadLine are the lines of the waker's report that
// say key presses sit unread in the console's input and that they were read.
const (
	keysUnreadLine = "keys unread"
	keysReadLine   = "keys read"
)

// unreadAfter is how long key presses sit unread before the waker says so. A
// program that reads as usual takes a key within milliseconds, and on a busy
// machine within a few hundred.
const unreadAfter = 350 * time.Millisecond

// The waker looks at input still unread every unreadLook, and every
// unreadSlowLook once it has sat for unreadSlowAfter, as it does under a
// program that reads nothing for as long as it runs.
const (
	unreadLook      = 50 * time.Millisecond
	unreadSlowLook  = 250 * time.Millisecond
	unreadSlowAfter = 3 * time.Second
)

// keyEvent is KEY_EVENT, the input record kind of a key going down or up.
const keyEvent = 1

var peekConsoleInput = kernel32.NewProc("PeekConsoleInputW")

// watchUnread is the waker's end: each time written signals input written, it
// looks at the console's input until none is left unread, and says on report
// when key presses among it have sat unread for unreadAfter and, after that,
// when all of it was read. It reads nothing out of the input and writes
// nothing into it.
func watchUnread(input windows.Handle, written <-chan struct{}, report io.Writer) {
	for range written {
		since := time.Now()
		isSaid := false
		for {
			if time.Since(since) < unreadSlowAfter {
				time.Sleep(unreadLook)
			} else {
				time.Sleep(unreadSlowLook)
			}
			var unread uint32
			if ok, _, _ := getNumberOfConsoleInputEvents.Call(uintptr(input), uintptr(unsafe.Pointer(&unread))); ok == 0 || unread == 0 {
				break
			}
			if !isSaid && time.Since(since) >= unreadAfter && hasUnreadKey(input) {
				fmt.Fprintln(report, keysUnreadLine)
				isSaid = true
			}
		}
		if isSaid {
			fmt.Fprintln(report, keysReadLine)
		}
	}
}

// hasUnreadKey reports whether a key press is among the first records left
// unread in the console's input. A window's focus, a resize and the waker's
// own wakes are records too, and none of them is typed.
func hasUnreadKey(input windows.Handle) bool {
	var records [64]inputRecord
	var count uint32
	if ok, _, _ := peekConsoleInput.Call(uintptr(input), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&count))); ok == 0 {
		return false
	}
	for _, record := range records[:count] {
		// A key event starts with whether the key went down.
		if record.Kind == keyEvent && binary.LittleEndian.Uint32(record.Event[:4]) != 0 {
			return true
		}
	}
	return false
}

// takeUnread tells the console's owner what line says of unread key presses,
// when it says anything. It reports whether line did.
func (w *waker) takeUnread(line string) bool {
	if line != keysUnreadLine && line != keysReadLine {
		return false
	}
	if w.unread != nil {
		w.unread(line == keysUnreadLine)
	}
	return true
}
