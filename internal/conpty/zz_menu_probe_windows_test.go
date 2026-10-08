package conpty

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Probe, not committed: does a menu event written while a program waits in
// ReadConsoleW with nothing else unread make that read fail?
func TestZZMenuProbeChild(t *testing.T) {
	if os.Getenv("ZZ_MENU_PROBE") == "" {
		return
	}
	logPath := os.Getenv("ZZ_MENU_PROBE")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	input := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(input, &mode); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetConsoleMode(input, mode&^uint32(windows.ENABLE_LINE_INPUT|windows.ENABLE_ECHO_INPUT|windows.ENABLE_PROCESSED_INPUT|windows.ENABLE_VIRTUAL_TERMINAL_INPUT)); err != nil {
		t.Fatal(err)
	}
	name, _ := windows.UTF16PtrFromString("CONIN$")
	writer, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	written := 0
	go func() {
		for {
			time.Sleep(time.Millisecond)
			record := inputRecord{Kind: menuEvent}
			var count uint32
			if ok, _, err := writeConsoleInput.Call(uintptr(writer), uintptr(unsafe.Pointer(&record)), 1, uintptr(unsafe.Pointer(&count))); ok == 0 {
				fmt.Fprintf(log, "write menu event failed: %v\n", err)
				return
			}
			written++
		}
	}()
	fmt.Println("probe-ready")
	characters := make([]uint16, 64)
	deadline := time.Now().Add(20 * time.Second)
	reads, empty := 0, 0
	for time.Now().Before(deadline) {
		var read uint32
		if err := windows.ReadConsole(input, &characters[0], uint32(len(characters)), &read, nil); err != nil {
			var code windows.Errno
			errors.As(err, &code)
			fmt.Fprintf(log, "ReadConsole failed after %d reads (%d empty) and %d menu events: %v (errno %d)\n", reads, empty, written, err, uintptr(code))
			os.Exit(1)
		}
		reads++
		if read == 0 {
			empty++
		}
	}
	fmt.Fprintf(log, "ReadConsole never failed: %d reads (%d empty), %d menu events\n", reads, empty, written)
}

func TestZZMenuProbe(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "probe.log")
	env := append(os.Environ(), "ZZ_MENU_PROBE="+logPath)
	console, err := Start(Spec{Args: []string{os.Args[0], "-test.run=^TestZZMenuProbeChild$"}, Env: env, Cols: 120, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	defer console.Close()
	go func() {
		buffer := make([]byte, 32<<10)
		for {
			if _, err := console.Read(buffer); err != nil {
				return
			}
		}
	}()
	stop := time.After(30 * time.Second)
typing:
	for {
		select {
		case <-console.Done():
			break typing
		case <-stop:
			t.Fatal("probe child never ended")
		case <-time.After(500 * time.Microsecond):
			console.Write([]byte("a"))
		}
	}
	data, _ := os.ReadFile(logPath)
	t.Logf("exit %#x: %s", console.ExitCode(), strings.TrimSpace(string(data)))
}

// Probe, not committed: Go starts with SEM_NOGPFAULTERRORBOX, which the
// console host inherits and which keeps Windows Error Reporting from dumping
// it when it crashes, so the probe gives it the default error mode.
func init() {
	if os.Getenv("CONPTY_PROBE_DUMPS") != "" {
		windows.SetErrorMode(0)
	}
}

// Probe, not committed: CONPTY_PROBE_DLL names Microsoft's conpty.dll from
// the Microsoft.Windows.Console.ConPTY package, which starts the
// OpenConsole.exe beside it in place of the system conhost.
func init() {
	path := os.Getenv("CONPTY_PROBE_DLL")
	if path == "" {
		return
	}
	dll := windows.NewLazyDLL(path)
	create, resize, closeConsole := dll.NewProc("CreatePseudoConsole"), dll.NewProc("ResizePseudoConsole"), dll.NewProc("ClosePseudoConsole")
	if err := create.Find(); err != nil {
		panic(err)
	}
	coord := func(size windows.Coord) uintptr { return uintptr(*(*uint32)(unsafe.Pointer(&size))) }
	createPseudoConsole = func(size windows.Coord, in, out windows.Handle, flags uint32, console *windows.Handle) error {
		if result, _, _ := create.Call(coord(size), uintptr(in), uintptr(out), uintptr(flags), uintptr(unsafe.Pointer(console))); result != 0 {
			return windows.Errno(result & 0xffff)
		}
		return nil
	}
	resizePseudoConsole = func(console windows.Handle, size windows.Coord) error {
		if result, _, _ := resize.Call(uintptr(console), coord(size)); result != 0 {
			return windows.Errno(result & 0xffff)
		}
		return nil
	}
	closePseudoConsole = func(console windows.Handle) {
		closeConsole.Call(uintptr(console))
	}
	consoleServerPath = func() (string, error) {
		return filepath.Join(filepath.Dir(path), "OpenConsole.exe"), nil
	}
}

// Probe, not committed: the first bytes each console host writes.
func TestZZFirstBytes(t *testing.T) {
	progressPath := filepath.Join(t.TempDir(), "keys-read.log")
	console, err := Start(Spec{Args: []string{os.Args[0], "-test.run=^TestStrandedInputChild$", "--", "stranded-input-child", progressPath}, Cols: 120, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	defer console.Close()
	var seen []byte
	buffer := make([]byte, 4096)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline) && len(seen) < 300; {
		n, err := console.Read(buffer)
		seen = append(seen, buffer[:n]...)
		if err != nil {
			break
		}
	}
	t.Logf("first bytes: %q", seen[:min(len(seen), 300)])
}
