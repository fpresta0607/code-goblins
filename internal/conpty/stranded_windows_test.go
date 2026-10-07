package conpty

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestStrandedInputChild is a program that reads its terminal as Go's
// os.Stdin.Read, cmd and Python's input() do, with blocking ReadConsoleW
// calls, and echoes each character it reads while another writer prints
// continuously. It logs each character to the file it is given as it reads
// it, so a key that never arrived can be told from an echo that never showed.
func TestStrandedInputChild(t *testing.T) {
	arguments := flag.Args()
	if len(arguments) != 2 || arguments[0] != "stranded-input-child" {
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
	// The load has a console handle of its own, so the echo never waits for
	// it in Go's lock on os.Stdout, only in the console.
	load, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for range tick.C {
			fmt.Fprintln(load, strings.Repeat("load", 200))
		}
	}()
	fmt.Println("stranded-ready")
	sequence := 0
	characters := make([]uint16, 64)
	for {
		var read uint32
		if err := windows.ReadConsole(input, &characters[0], uint32(len(characters)), &read, nil); err != nil {
			t.Fatal(err)
		}
		for _, character := range characters[:read] {
			sequence++
			if _, err := fmt.Fprintf(progress, "read %04x as key-%04d at %s\n", character, sequence, time.Now().UTC().Format("15:04:05.000000")); err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Printf("\rkey-%04d\n", sequence); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// A key typed into a terminal reaches its program at once, however busy the
// program is printing. Windows' inbox conhost (10.0.26100) can leave a key in
// the console's input, unread, while the program waits in a blocking read and
// another of its threads prints: the key stays there until something else is
// typed (microsoft/terminal#18228 reworked that wakeup). Each key is typed
// once the program has echoed the one before, as a person types, which is
// when the program has just gone back to its read.
func TestTypedKeysReachAProgramThatPrintsWhileItReads(t *testing.T) {
	progressPath := filepath.Join(t.TempDir(), "keys-read.log")
	console, err := Start(Spec{
		Args: []string{os.Args[0], "-test.run=^TestStrandedInputChild$", "--", "stranded-input-child", progressPath},
		Cols: 120, Rows: 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	output := make(chan []byte, 256)
	stopping, ended := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		close(stopping)
		_ = console.Close()
		<-ended
	})
	// readErr is what the terminal's output ended with, set before output
	// closes.
	var readErr error
	go func() {
		defer close(ended)
		defer close(output)
		for {
			buffer := make([]byte, 32<<10)
			count, err := console.Read(buffer)
			if count > 0 {
				select {
				case output <- buffer[:count]:
				case <-stopping:
					return
				}
			}
			if err != nil {
				readErr = err
				return
			}
		}
	}()
	var shown string
	echoed := func(marker string, limit time.Duration) bool {
		timer := time.NewTimer(limit)
		defer timer.Stop()
		for !strings.Contains(shown, marker) {
			select {
			case chunk, isOpen := <-output:
				if !isOpen {
					t.Fatalf("the terminal ended before %q: %s", marker, terminalEnd(console, readErr, shown, progressPath))
				}
				shown += string(chunk)
				if len(shown) > 8192 {
					shown = shown[len(shown)-8192:]
				}
			case <-timer.C:
				return false
			}
		}
		shown = shown[strings.Index(shown, marker)+len(marker):]
		return true
	}
	if !echoed("stranded-ready", 15*time.Second) {
		t.Fatal("the program never started reading")
	}
	const keys = 3000
	var slowest time.Duration
	for sequence := 1; sequence <= keys; sequence++ {
		// Act
		typed := time.Now()
		if _, err := console.Write([]byte("a")); err != nil {
			t.Fatal(err)
		}

		// Assert
		if !echoed(fmt.Sprintf("key-%04d", sequence), 2*time.Second) {
			progress, _ := os.ReadFile(progressPath)
			lines := strings.Split(strings.TrimSpace(string(progress)), "\n")
			if strings.Contains(string(progress), fmt.Sprintf("as key-%04d", sequence)) {
				t.Fatalf("key %d of %d reached the program, but its echo did not show within 2s; the program last read: %q", sequence, keys, lines[max(0, len(lines)-3):])
			}
			t.Fatalf("key %d of %d did not reach the program within 2s while it printed: it is stranded in the console's input; the program last read: %q", sequence, keys, lines[max(0, len(lines)-3):])
		}
		slowest = max(slowest, time.Since(typed))
	}
	t.Logf("%d keys typed into a printing program, the slowest echoed after %s", keys, slowest)
}

// terminalEnd says how a terminal that ended under a test ended: the error
// its output ended with, how its program exited, what it last showed and
// what the program last read, which tell a program that failed or was ended
// from a console that went away under it.
func terminalEnd(console *Console, readErr error, shown, progressPath string) string {
	exit := "its program had not exited 5s later"
	select {
	case <-console.Done():
		exit = fmt.Sprintf("its program exited with code %#x", console.ExitCode())
	case <-time.After(5 * time.Second):
	}
	progress, _ := os.ReadFile(progressPath)
	lines := strings.Split(strings.TrimSpace(string(progress)), "\n")
	return fmt.Sprintf("its output ended with %v; %s; it last showed %q; the program last read %q", readErr, exit, shown[max(0, len(shown)-1024):], lines[max(0, len(lines)-3):])
}

// TestUnreadInputChild sends Ctrl-C and Ctrl-Break to every process attached
// to its console, then leaves its input unread until the file it is given
// exists, and then logs the kind of each input record it reads.
func TestUnreadInputChild(t *testing.T) {
	arguments := flag.Args()
	if len(arguments) != 3 || arguments[0] != "unread-input-child" {
		return
	}
	signal.Notify(make(chan os.Signal, 2), os.Interrupt)
	input := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(input, &mode); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetConsoleMode(input, mode&^uint32(windows.ENABLE_LINE_INPUT|windows.ENABLE_ECHO_INPUT|windows.ENABLE_PROCESSED_INPUT)); err != nil {
		t.Fatal(err)
	}
	for _, event := range []uint32{windows.CTRL_C_EVENT, windows.CTRL_BREAK_EVENT} {
		if err := windows.GenerateConsoleCtrlEvent(event, 0); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	fmt.Println("unread-ready")
	for {
		if _, err := os.Stat(arguments[2]); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	progress, err := os.Create(arguments[1])
	if err != nil {
		t.Fatal(err)
	}
	defer progress.Close()
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
		if received > 0 {
			if _, err := fmt.Fprintf(progress, "kind=%d down=%d character=%04x\n", event.Kind, event.Down, event.Character); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Input a program leaves unread is followed, within moments, by a wake that
// every reader skips, a menu event: a program left waiting by conhost reads
// its key then. The console's input waker outlives the Ctrl-C and Ctrl-Break
// that reach every process attached to the console.
func TestInputLeftUnreadIsFollowedByAWakeEveryReaderSkips(t *testing.T) {
	// Arrange
	directory := t.TempDir()
	progressPath, release := filepath.Join(directory, "records.log"), filepath.Join(directory, "read-now")
	console, s := startChild(t, Spec{
		Args: []string{os.Args[0], "-test.run=^TestUnreadInputChild$", "--", "unread-input-child", progressPath, release},
		Env:  os.Environ(),
		Cols: 80, Rows: 25,
	})
	s.waitFor(t, "unread-ready")

	// Act
	if _, err := console.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// Assert
	var records []string
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		progress, _ := os.ReadFile(progressPath)
		records = strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(progress)), " ", "/"))
		if slices.Contains(records, "kind=8/down=0/character=0000") || time.Now().After(deadline) {
			break
		}
	}
	key := slices.Index(records, "kind=1/down=1/character=0061")
	wake := slices.Index(records, "kind=8/down=0/character=0000")
	if key < 0 || wake < key {
		t.Fatalf("the program read %q: want its key, then a menu event waking it", records)
	}
	if strings.Count(strings.Join(records, " "), "kind=1/down=1/character=0061") != 1 {
		t.Fatalf("the program read %q: want its key once", records)
	}
}
