package conpty

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestAnEndingConhostWouldHoldIsWrittenAsTheKeysItStandsFor(t *testing.T) {
	altPressed := func(character string) string {
		return fmt.Sprintf("\x1b[0;0;%d;1;2;1_\x1b[0;0;%d;0;2;1_", character[0], character[0])
	}
	for name, test := range map[string]struct{ typed, written string }{
		"Escape alone":                            {typed: "\x1b", written: escapeKey},
		"text ending in Escape":                   {typed: "abc\x1b", written: "abc" + escapeKey},
		"Alt and a character that starts a CSI":   {typed: "\x1b[", written: altPressed("[")},
		"Alt and a character that starts an SS3":  {typed: "\x1bO", written: altPressed("O")},
		"Alt and a character that starts a DCS":   {typed: "\x1bP", written: altPressed("P")},
		"Alt and a character that starts an OSC":  {typed: "\x1b]", written: altPressed("]")},
		"Alt and a character that starts an APC":  {typed: "x\x1b_", written: "x" + altPressed("_")},
		"Alt and a character that ends the pair":  {typed: "\x1bx", written: "\x1bx"},
		"a whole sequence":                        {typed: "\x1b[A", written: "\x1b[A"},
		"a key event as Windows sends it":         {typed: "\x1b[65;30;97;1;0;1_", written: "\x1b[65;30;97;1;0;1_"},
		"text":                                    {typed: "hello\r", written: "hello\r"},
		"nothing":                                 {typed: "", written: ""},
		"Escape inside the input, not at its end": {typed: "\x1bq", written: "\x1bq"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			typed := []byte(test.typed)
			kept := bytes.Clone(typed)

			// Act
			written := endingAsKeyEvents(typed)

			// Assert
			if string(written) != test.written {
				t.Errorf("endingAsKeyEvents(%q) = %q, want %q", test.typed, written, test.written)
			}
			if !bytes.Equal(typed, kept) {
				t.Errorf("endingAsKeyEvents changed what it was given: %q, was %q", typed, kept)
			}
		})
	}
}

// TestKeyRecordChild reads its console's input one record at a time, as
// crossterm does, without virtual terminal input, and logs each key record.
func TestKeyRecordChild(t *testing.T) {
	arguments := flag.Args()
	if len(arguments) != 2 || arguments[0] != "key-record-child" {
		return
	}
	progress, err := os.Create(arguments[1])
	if err != nil {
		t.Fatal(err)
	}
	defer progress.Close()
	input := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(input, &mode); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetConsoleMode(input, mode&^uint32(windows.ENABLE_LINE_INPUT|windows.ENABLE_ECHO_INPUT|windows.ENABLE_PROCESSED_INPUT|windows.ENABLE_VIRTUAL_TERMINAL_INPUT)); err != nil {
		t.Fatal(err)
	}
	fmt.Println("key-records-ready")
	readEvents := kernel32.NewProc("ReadConsoleInputW")
	for {
		var event struct {
			Kind, Padding                uint16
			Down                         int32
			Repeat, Key, Scan, Character uint16
			Control                      uint32
		}
		var received uint32
		if ok, _, err := readEvents.Call(uintptr(input), uintptr(unsafe.Pointer(&event)), 1, uintptr(unsafe.Pointer(&received))); ok == 0 {
			t.Fatal(err)
		}
		if received > 0 && event.Kind == 1 && event.Down != 0 {
			if _, err := fmt.Fprintf(progress, "key %d character %04x alt %t\n", event.Key, event.Character, event.Control&(leftAltPressed|1) != 0); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// An Escape typed into a terminal that has read a key event as Windows sends
// it, as one does once cfo attach has typed into it, reaches the program at
// once, and the key after it arrives as itself, not as Alt and that key.
func TestAnEscapeTypedAfterAWindowsKeyEventReachesTheProgramAtOnce(t *testing.T) {
	progressPath := filepath.Join(t.TempDir(), "keys.log")
	console, s := startChild(t, Spec{
		Args: []string{os.Args[0], "-test.run=^TestKeyRecordChild$", "--", "key-record-child", progressPath},
		Env:  os.Environ(),
		Cols: 80, Rows: 25,
	})
	s.waitFor(t, "key-records-ready")
	read := func(want string) bool {
		for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if progress, _ := os.ReadFile(progressPath); strings.Contains(string(progress), want) {
				return true
			}
		}
		return false
	}
	// A key event as cfo attach writes one: a pressed and released.
	if _, err := console.Write([]byte("\x1b[65;30;97;1;0;1_\x1b[65;30;97;0;0;1_")); err != nil {
		t.Fatal(err)
	}
	if !read("key 65 character 0061 alt false") {
		t.Fatal("the program never read the key event")
	}

	for _, key := range []struct{ name, typed, read string }{
		{"Escape", "\x1b", "key 27 character 001b alt false"},
		{"q after it", "q", "character 0071 alt false"},
		{"Alt and [", "\x1b[", "character 005b alt true"},
	} {
		// Act
		if _, err := console.Write([]byte(key.typed)); err != nil {
			t.Fatal(err)
		}

		// Assert
		if !read(key.read) {
			progress, _ := os.ReadFile(progressPath)
			t.Fatalf("%s typed as %q did not reach the program as %q within a second; it read:\n%s", key.name, key.typed, key.read, progress)
		}
	}
}
