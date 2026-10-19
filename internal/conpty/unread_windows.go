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
// until it is read, and says when a key press has sat unread for unreadAfter
// and when they were read, so a viewer can say so.

// keysUnreadLine and keysReadLine are the lines of the waker's report that
// say key presses sit unread in the console's input and that they were read.
const (
	keysUnreadLine = "keys unread"
	keysReadLine   = "keys read"
)

// unreadAfter is how long a key press sits unread before the waker says so.
// A program that reads as usual takes a key within milliseconds, Claude Code
// while it streams within a few hundred, and only a key that has changed
// nothing on the screen for a second is one a person may press again. It is
// read on the wall clock, which a pause of the whole machine runs on: a key
// typed just before a pause is said unread as the machine runs again, until
// the program, which the pause stopped too, reads it moments later. That
// note on a viewer is accepted (#1102).
const unreadAfter = time.Second

// unreadShownFor is how long the waker keeps saying key presses sit unread,
// however soon they are read, so a viewer's note is read rather than seen to
// blink.
const unreadShownFor = time.Second

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
// when a key press among it has sat unread for unreadAfter and, after that,
// when all of them were read. It reads nothing out of the input and writes
// nothing into it.
func watchUnread(input windows.Handle, written <-chan struct{}, report io.Writer) {
	var keys unreadKeys
	for range written {
		since := time.Now()
		for {
			if time.Since(since) < unreadSlowAfter {
				time.Sleep(unreadLook)
			} else {
				time.Sleep(unreadSlowLook)
			}
			var records [64]inputRecord
			var count uint32
			if ok, _, _ := peekConsoleInput.Call(uintptr(input), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&count))); ok == 0 {
				if keys.isSaid() {
					keys = unreadKeys{}
					fmt.Fprintln(report, keysReadLine)
				}
				break
			}
			if line := keys.look(time.Now(), records[:count], int(count) == len(records)); line != "" {
				fmt.Fprintln(report, line)
			}
			if count == 0 && !keys.isSaid() {
				break
			}
		}
	}
}

// unreadKeys follows the records left unread in the console's input from one
// look to the next, each with when it was first seen, so the wait it reads is
// the oldest unread key press's own. Claude Code reads typed keys in bursts
// while it streams, and while keys keep coming some key just typed is unread
// at most looks, though none waits long: timed from the first key of the run,
// as it once was, that read as one long wait (the Overlord, 2026-10-14,
// "program busy typed keys waiting glitches adn flashes freuqnetly").
type unreadKeys struct {
	unread []seenRecord
	// saidAt is when the waker said key presses sit unread, zero while it
	// has not.
	saidAt time.Time
}

// seenRecord is a record unread in the console's input and when the waker
// first saw it there.
type seenRecord struct {
	record inputRecord
	since  time.Time
}

// look takes the records unread in the console's input at now, oldest first,
// isPart when they are only the first of them, and returns the line the
// waker says, if any: that key presses sit unread once one has sat for
// unreadAfter, and that they were read once none is left and the first line
// was said unreadShownFor before. A key typed after the second line was said
// has waited no time yet, so the waker says the first again no sooner than
// unreadAfter after it.
func (k *unreadKeys) look(now time.Time, records []inputRecord, isPart bool) string {
	kept := k.unread[readSince(k.unread, records, isPart):]
	k.unread = make([]seenRecord, len(records))
	oldest := time.Time{}
	for index, record := range records {
		since := now
		if index < len(kept) {
			since = kept[index].since
		}
		k.unread[index] = seenRecord{record, since}
		// A key event starts with whether the key went down. A window's
		// focus, a resize and the waker's own wakes are records too, and none
		// of them is typed.
		if oldest.IsZero() && record.Kind == keyEvent && binary.LittleEndian.Uint32(record.Event[:4]) != 0 {
			oldest = since
		}
	}
	switch {
	case !k.isSaid() && !oldest.IsZero() && now.Sub(oldest) >= unreadAfter:
		k.saidAt = now
		return keysUnreadLine
	case k.isSaid() && oldest.IsZero() && now.Sub(k.saidAt) >= unreadShownFor:
		k.saidAt = time.Time{}
		return keysReadLine
	}
	return ""
}

// isSaid reports whether the waker has said key presses sit unread and not
// yet that they were read.
func (k *unreadKeys) isSaid() bool {
	return !k.saidAt.IsZero()
}

// readSince returns how many of the records unread at the last look the
// program has read since: the fewest after which the rest are where the
// records unread now start, as input is read from its start and written at
// its end. Of keys that are all the same, a key read and another typed
// between two looks reads as none read, so such a wait is never read short.
func readSince(before []seenRecord, now []inputRecord, isPart bool) int {
	for read := range before {
		rest := before[read:]
		if len(rest) > len(now) && !isPart {
			continue
		}
		isSame := true
		for index := range min(len(rest), len(now)) {
			if rest[index].record != now[index] {
				isSame = false
				break
			}
		}
		if isSame {
			return read
		}
	}
	return len(before)
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
