package conpty

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// awaitUnread returns what the console next says of unread key presses,
// failing the test when it says nothing within ten seconds.
func awaitUnread(t *testing.T, said <-chan bool, want bool) {
	t.Helper()
	select {
	case isUnread := <-said:
		if isUnread != want {
			t.Fatalf("the console said unread=%t, want %t", isUnread, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the console did not say unread=%t within 10s", want)
	}
}

// A key typed while the program is busy sits unread in the console's input,
// where nothing on the screen shows it was typed: the Overlord pressed Enter
// on a row of Claude Code's permissions screen on 2026-10-09 while Claude
// Code was starting a tool, and nothing was approved. The console says such
// key presses sit unread, and says they were read once the program reads
// them.
func TestKeysABusyProgramLeavesUnreadAreSaidUnreadAndThenRead(t *testing.T) {
	readNow := filepath.Join(t.TempDir(), "read-now")
	said := make(chan bool, 8)
	console, output := startChild(t, Spec{
		Cols: 80, Rows: 25,
		Env:    append(os.Environ(), childMode+"=late", lateChildFile+"="+readNow),
		Unread: func(isUnread bool) { said <- isUnread },
	})

	typeLine(t, console, "typed while busy")

	awaitUnread(t, said, true)
	if err := os.WriteFile(readNow, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	awaitUnread(t, said, false)
	output.waitFor(t, "got typed while busy")
}

// A key the program reads as usual is never said to sit unread, so a viewer
// shows nothing for ordinary typing.
func TestKeysAProgramReadsAtOnceAreNeverSaidUnread(t *testing.T) {
	said := make(chan bool, 8)
	console, output := startChild(t, Spec{Cols: 80, Rows: 25, Unread: func(isUnread bool) { said <- isUnread }})

	for _, line := range []string{"one", "two", "three"} {
		typeLine(t, console, line)
		output.waitFor(t, "got "+line)
	}

	select {
	case isUnread := <-said:
		t.Fatalf("the console said unread=%t of keys its program read at once", isUnread)
	case <-time.After(2 * unreadAfter):
	}
}

// A window's focus and a resize are input a program may leave unread for as
// long as it likes, and neither was typed.
func TestInputThatIsNoKeyPressIsNeverSaidUnread(t *testing.T) {
	said := make(chan bool, 8)
	console, _ := startChild(t, Spec{
		Cols: 80, Rows: 25,
		Env:    append(os.Environ(), childMode+"=late", lateChildFile+"="+filepath.Join(t.TempDir(), "never")),
		Unread: func(isUnread bool) { said <- isUnread },
	})

	if _, err := console.Write([]byte("\x1b[I")); err != nil {
		t.Fatal(err)
	}
	if err := console.Resize(90, 30); err != nil {
		t.Fatal(err)
	}

	select {
	case isUnread := <-said:
		t.Fatalf("the console said unread=%t of a focus report and a resize", isUnread)
	case <-time.After(3 * unreadAfter):
	}
}
