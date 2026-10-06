package conpty

import (
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

func TestAnEndingConhostWouldHoldIsHeldAndWrittenAsTheKeysItStandsFor(t *testing.T) {
	altPressed := func(character string) string {
		return fmt.Sprintf("\x1b[0;0;%d;1;2;1_\x1b[0;0;%d;0;2;1_", character[0], character[0])
	}
	for name, test := range map[string]struct {
		typed, keys string
		held        int
	}{
		"Escape alone":                            {typed: "\x1b", held: 1, keys: escapeKey},
		"text ending in Escape":                   {typed: "abc\x1b", held: 1, keys: escapeKey},
		"Alt and a character that starts a CSI":   {typed: "\x1b[", held: 2, keys: altPressed("[")},
		"Alt and a character that starts an SS3":  {typed: "\x1bO", held: 2, keys: altPressed("O")},
		"Alt and a character that starts a DCS":   {typed: "\x1bP", held: 2, keys: altPressed("P")},
		"Alt and a character that starts an OSC":  {typed: "\x1b]", held: 2, keys: altPressed("]")},
		"Alt and a character that starts an APC":  {typed: "x\x1b_", held: 2, keys: altPressed("_")},
		"Alt and a character that ends the pair":  {typed: "\x1bx"},
		"a whole sequence":                        {typed: "\x1b[A"},
		"a key event as Windows sends it":         {typed: "\x1b[65;30;97;1;0;1_"},
		"text":                                    {typed: "hello\r"},
		"nothing":                                 {typed: ""},
		"Escape inside the input, not at its end": {typed: "\x1bq"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			typed := []byte(test.typed)

			// Act
			held := heldEnding(typed)

			// Assert
			if held != test.held {
				t.Fatalf("heldEnding(%q) = %d, want %d", test.typed, held, test.held)
			}
			if held == 0 {
				return
			}
			if keys := asKeyEvents(typed[len(typed)-held:]); string(keys) != test.keys {
				t.Errorf("asKeyEvents(%q) = %q, want %q", typed[len(typed)-held:], keys, test.keys)
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

// A key event written as Windows sends it and split across two writes, as cfo
// attach splits what its console reads 4 KiB at a time, reaches the program
// as that key: the Escape that ends the first write is held for the second,
// never sent alone.
func TestAKeyEventSplitAcrossTwoWritesReachesTheProgramWhole(t *testing.T) {
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
	if _, err := console.Write([]byte("\x1b[65;30;97;1;0;1_\x1b[65;30;97;0;0;1_")); err != nil {
		t.Fatal(err)
	}
	if !read("key 65 character 0061 alt false") {
		t.Fatal("the program never read the first key event")
	}

	for _, split := range []struct{ name, first, rest, read string }{
		{"after its Escape", "\x1b", "[66;48;98;1;0;1_\x1b[66;48;98;0;0;1_", "key 66 character 0062 alt false"},
		{"after its bracket", "\x1b[", "67;46;99;1;0;1_\x1b[67;46;99;0;0;1_", "key 67 character 0063 alt false"},
	} {
		// Act
		for _, part := range []string{split.first, split.rest} {
			if _, err := console.Write([]byte(part)); err != nil {
				t.Fatal(err)
			}
		}

		// Assert
		if !read(split.read) {
			progress, _ := os.ReadFile(progressPath)
			t.Fatalf("a key event split %s did not reach the program as %q; it read:\n%s", split.name, split.read, progress)
		}
	}
	if progress, _ := os.ReadFile(progressPath); strings.Contains(string(progress), "key 27 ") || strings.Contains(string(progress), "alt true") {
		t.Fatalf("a split key event reached the program as Escape or Alt; it read:\n%s", progress)
	}
}
