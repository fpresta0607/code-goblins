package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/conpty"
)

// screenRole, as a program's first argument, makes a program built with this
// package read a console's screen and exit instead of running. A host runs its
// own program that way for every read of its terminal's screen, because a
// process attached to a console receives that console's Ctrl-C and its
// closing, and neither may reach the host. Each read is a process of its own
// that attaches once, so no process ever holds two consoles.
const screenRole = "cfo-host-screen"

// screenTimeout bounds one read of a terminal's screen.
var screenTimeout = 10 * time.Second

// screenHold keeps a read attached to the terminal's console that long before
// it reads, so a test can act on the terminal while a read is attached.
var screenHold time.Duration

var (
	kernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	attachConsole              = kernel32.NewProc("AttachConsole")
	setConsoleCtrlHandler      = kernel32.NewProc("SetConsoleCtrlHandler")
	readConsoleOutputCharacter = kernel32.NewProc("ReadConsoleOutputCharacterW")
)

func init() {
	if len(os.Args) > 1 && os.Args[0] == screenRole {
		os.Exit(printScreen(os.Args[1:]))
	}
}

// screen is a screen frame's payload: the rows of the terminal's window, or
// why they could not be read.
type screen struct {
	Rows  []string `json:"rows,omitempty"`
	Error string   `json:"error,omitempty"`
}

// ReadScreen reads the screen of the terminal record's host runs, as the
// terminal's console holds it: one string per row of its window, each as wide
// as the window. A read that fails is an error naming the terminal, never an
// empty screen.
func ReadScreen(record Record) ([]string, error) {
	rows, err := requestScreen(record)
	if err != nil {
		return nil, fmt.Errorf("host: read the screen of terminal %s: %w", record.ID, err)
	}
	return rows, nil
}

func requestScreen(record Record) ([]string, error) {
	client, err := dial(record, hello{Version: Version, Token: record.Token, Screen: true})
	if err != nil {
		return nil, err
	}
	defer client.Close()
	_ = client.pipe.SetReadDeadline(time.Now().Add(screenTimeout + handshakeTimeout))
	kind, payload, err := readFrame(client.pipe)
	if err != nil {
		return nil, err
	}
	if kind != frameScreen {
		return nil, fmt.Errorf("its host answered with frame %q, not a screen; a host started by an older cfo cannot read screens", kind)
	}
	var answer screen
	if err := json.Unmarshal(payload, &answer); err != nil {
		return nil, err
	}
	if answer.Error != "" {
		return nil, errors.New(answer.Error)
	}
	return answer.Rows, nil
}

// serveScreen answers a screen request once its token is checked: the screen
// of the terminal's console, or why it could not be read. It reads while
// holding screens shared, and never once the terminal's program has ended,
// since its pid may by then name another process.
func serveScreen(connection *os.File, console *conpty.Console, screens *sync.RWMutex) {
	if writeHello(connection, hello{Version: Version}) != nil {
		return
	}
	var answer screen
	screens.RLock()
	select {
	case <-console.Done():
		answer.Error = "the terminal has ended"
	default:
		rows, err := readScreenOf(console.PID())
		answer.Rows = rows
		if err != nil {
			answer = screen{Error: err.Error()}
		}
	}
	screens.RUnlock()
	payload, err := json.Marshal(answer)
	if err != nil {
		return
	}
	_ = writeFrame(connection, frameScreen, payload)
}

// readScreenOf reads the screen of the console pid runs in, in a process of
// its own: this program again, as the screen reader.
func readScreenOf(pid int) ([]string, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), screenTimeout)
	defer cancel()
	args := []string{strconv.Itoa(pid)}
	if screenHold > 0 {
		args = append(args, screenHold.String())
	}
	reader := exec.CommandContext(ctx, self, args...)
	reader.Args[0] = screenRole
	// Without a console of its own, the reader can attach to the terminal's.
	reader.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	var problem strings.Builder
	reader.Stderr = &problem
	output, err := reader.Output()
	if err != nil {
		return nil, fmt.Errorf("the screen reader failed (%v): %s", err, strings.TrimSpace(problem.String()))
	}
	var rows []string
	if err := json.Unmarshal(output, &rows); err != nil {
		return nil, fmt.Errorf("the screen reader printed %q, not a screen: %w", output, err)
	}
	return rows, nil
}

// printScreen is the screen reader: it attaches this process to the console
// pid runs in and prints the console's screen as JSON. A second argument
// holds the attached read that long first.
func printScreen(args []string) int {
	pid, err := strconv.Atoi(args[0])
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
	if len(args) > 1 {
		hold, _ := time.ParseDuration(args[1])
		time.Sleep(hold)
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
