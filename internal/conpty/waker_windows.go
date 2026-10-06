package conpty

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows' inbox conhost (10.0.26100, as Windows 11 and Windows Server 2025
// ship it) can queue typed input without waking a program that waits for it
// in a blocking read, ReadConsoleInputW or ReadConsoleW, while another of its
// threads prints: the input then waits, read by nothing, until more input
// arrives. Microsoft's current conhost, which reworked that wakeup in
// microsoft/terminal#18228, does not; Windows does not ship it yet. So each
// pseudo console has an input waker: this program again, attached to the same
// console, told each time input is written. When input it was told of is
// still unread a moment later, it writes a menu event into the console's
// input, which wakes the reader and which every reader ignores, as documented
// for MENU_EVENT and as libuv, crossterm, .NET and conhost's own character
// reads do.

// wakerRole, as a program's first argument, makes a program built with this
// package the input waker of the pseudo console it was started in, instead
// of running.
const wakerRole = "cfo-conpty-waker"

func init() {
	if len(os.Args) == 1 && os.Args[0] == wakerRole {
		os.Exit(runWaker(os.Stdin, os.Stdout))
	}
}

// wakeLooks are how long after input was last written the waker looks for
// it still unread, each look after the one before. By the first, a reader
// conhost woke has long taken its input, which takes about a millisecond, and
// a key the waker has to wake still arrives within the 50 ms a key may take.
var wakeLooks = []time.Duration{25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1600 * time.Millisecond}

var (
	getNumberOfConsoleInputEvents = kernel32.NewProc("GetNumberOfConsoleInputEvents")
	writeConsoleInput             = kernel32.NewProc("WriteConsoleInputW")
)

// menuEvent is MENU_EVENT, the input record kind a console's window raises
// for its menus and every reader skips.
const menuEvent = 8

// inputRecord is a console's INPUT_RECORD: its kind, then the event, as long
// as the longest one, a key event.
type inputRecord struct {
	Kind  uint16
	_     uint16
	Event [16]byte
}

// wakerReady bounds how long a new waker takes to say it is ready.
const wakerReady = 10 * time.Second

// waker is the host's end of a console's input waker.
type waker struct {
	// typed takes one byte each time input is written.
	typed *os.File
	// report carries what the waker says, a line at a time, and lines reads
	// it.
	report *os.File
	lines  *bufio.Reader
}

// startWaker starts this program as the input waker of pseudo console pc,
// in job, suspended until it is in the job and scheduled as interactive, and
// returns once it says it is ready: it outlives a Ctrl-C or Ctrl-Break only
// from then on, so the console's program, which could raise one, runs only
// after.
func startWaker(pc, job windows.Handle) (*waker, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("conpty: find this program to run as the console's input waker: %w", err)
	}
	program, err := windows.UTF16PtrFromString(self)
	if err != nil {
		return nil, fmt.Errorf("conpty: input waker program: %w", err)
	}
	commandLine, err := windows.UTF16PtrFromString(wakerRole)
	if err != nil {
		return nil, err
	}
	// Only the waker's ends of its two pipes are inheritable, and only those
	// are handed to it.
	inheritable := windows.SecurityAttributes{InheritHandle: 1}
	inheritable.Length = uint32(unsafe.Sizeof(inheritable))
	var typedRead, typedWrite, reportRead, reportWrite windows.Handle
	if err := windows.CreatePipe(&typedRead, &typedWrite, &inheritable, 0); err != nil {
		return nil, fmt.Errorf("conpty: input waker pipe: %w", err)
	}
	defer windows.CloseHandle(typedRead)
	w := &waker{typed: os.NewFile(uintptr(typedWrite), "conpty-input-waker")}
	if err := windows.CreatePipe(&reportRead, &reportWrite, &inheritable, 0); err != nil {
		w.typed.Close()
		return nil, fmt.Errorf("conpty: input waker pipe: %w", err)
	}
	defer windows.CloseHandle(reportWrite)
	w.report = os.NewFile(uintptr(reportRead), "conpty-input-waker-report")
	fail := func(err error) (*waker, error) {
		w.typed.Close()
		w.report.Close()
		return nil, err
	}
	for _, end := range []windows.Handle{typedWrite, reportRead} {
		if err := windows.SetHandleInformation(end, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
			return fail(fmt.Errorf("conpty: input waker pipe: %w", err))
		}
	}
	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return fail(fmt.Errorf("conpty: input waker attribute list: %w", err))
	}
	defer attributes.Delete()
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&pc)), unsafe.Sizeof(pc)); err != nil {
		return fail(fmt.Errorf("conpty: attach the input waker: %w", err))
	}
	inherited := []windows.Handle{typedRead, reportWrite}
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&inherited[0]), uintptr(len(inherited))*unsafe.Sizeof(inherited[0])); err != nil {
		return fail(fmt.Errorf("conpty: input waker handles: %w", err))
	}
	startup := windows.StartupInfoEx{ProcThreadAttributeList: attributes.List()}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput, startup.StdOutput, startup.StdErr = typedRead, reportWrite, reportWrite
	var info windows.ProcessInformation
	if err := windows.CreateProcess(program, commandLine, nil, nil, true, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_SUSPENDED, nil, nil, &startup.StartupInfo, &info); err != nil {
		return fail(fmt.Errorf("conpty: start the input waker: %w", err))
	}
	defer windows.CloseHandle(info.Process)
	defer windows.CloseHandle(info.Thread)
	if err := windows.AssignProcessToJobObject(job, info.Process); err != nil {
		windows.TerminateProcess(info.Process, 1)
		return fail(fmt.Errorf("conpty: assign the input waker to the job: %w", err))
	}
	if err := interactiveScheduling(info.Process); err != nil {
		windows.TerminateProcess(info.Process, 1)
		return fail(fmt.Errorf("conpty: input waker scheduling: %w", err))
	}
	if _, err := windows.ResumeThread(info.Thread); err != nil {
		windows.TerminateProcess(info.Process, 1)
		return fail(fmt.Errorf("conpty: resume the input waker: %w", err))
	}
	w.lines = bufio.NewReader(w.report)
	said := make(chan error, 1)
	go func() {
		line, err := w.lines.ReadString('\n')
		if line = strings.TrimSpace(line); err == nil && line != "ready" {
			err = errors.New(line)
		}
		said <- err
	}()
	select {
	case err := <-said:
		if err != nil {
			windows.TerminateProcess(info.Process, 1)
			return fail(fmt.Errorf("conpty: the input waker did not start: %w", err))
		}
	case <-time.After(wakerReady):
		windows.TerminateProcess(info.Process, 1)
		return fail(fmt.Errorf("conpty: the input waker did not start within %s", wakerReady))
	}
	return w, nil
}

// relay tells the waker of input written, through typed, until done closes,
// and copies what it says into this process's standard error. A waker that
// stops listening before done or closing closes, which the console closes as
// Close begins, has failed, and says so.
func (w *waker) relay(typed, done, closing <-chan struct{}) {
	go func() {
		defer w.report.Close()
		lines := bufio.NewScanner(w.lines)
		for lines.Scan() {
			fmt.Fprintln(os.Stderr, "conpty: "+lines.Text())
		}
	}()
	defer w.typed.Close()
	for {
		select {
		case <-typed:
			if _, err := w.typed.Write([]byte{1}); err != nil {
				select {
				case <-done:
				case <-closing:
				default:
					fmt.Fprintf(os.Stderr, "conpty: the console's input waker stopped listening, so input left unread is no longer woken: %v\n", err)
				}
				return
			}
		case <-done:
			return
		}
	}
}

// runWaker is the input waker, attached to its pseudo console. Each byte
// typed brings is input the host wrote; report takes what it has to say.
func runWaker(typed io.Reader, report io.Writer) int {
	// Ctrl-C and Ctrl-Break reach every process attached to the console, and
	// the waker outlives both. The console closing still ends it.
	signal.Notify(make(chan os.Signal, 1), os.Interrupt)
	name, err := windows.UTF16PtrFromString("CONIN$")
	if err != nil {
		fmt.Fprintln(report, err)
		return 1
	}
	input, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		fmt.Fprintf(report, "the input waker cannot open its console's input: %v\n", err)
		return 1
	}
	fmt.Fprintln(report, "ready")
	written := make(chan struct{}, 1)
	go func() {
		defer close(written)
		signals := make([]byte, 64)
		for {
			if _, err := typed.Read(signals); err != nil {
				return
			}
			select {
			case written <- struct{}{}:
			default:
			}
		}
	}()
	reports := &reporter{out: report}
	for range written {
		if err := wakeWhileUnread(input, written, reports); errors.Is(err, errInputEnded) {
			return 0
		} else if err != nil {
			fmt.Fprintf(report, "the input waker stopped: %v\n", err)
			return 1
		}
	}
	return 0
}

// errInputEnded is the host closing its end: no more input will be written.
var errInputEnded = errors.New("no more input will be written")

// wakeWhileUnread looks for written input left unread at each of wakeLooks,
// writing a menu event at each look that finds some, until a look finds none
// or the looks run out. Input written meanwhile starts the looks again. It
// reports each run of looks that woke the reader.
func wakeWhileUnread(input windows.Handle, written <-chan struct{}, report *reporter) error {
	look, woken := 0, 0
	last := time.Now()
	timer := time.NewTimer(wakeLooks[look])
	defer timer.Stop()
	for {
		select {
		case _, isOpen := <-written:
			if !isOpen {
				return errInputEnded
			}
			if woken > 0 {
				report.say(fmt.Sprintf("input sat unread in the console, so its reader was woken %s before more was written %s after it", wakes(woken), time.Since(last).Round(time.Millisecond)))
			}
			look, woken, last = 0, 0, time.Now()
			timer.Reset(wakeLooks[look])
			continue
		case <-timer.C:
		}
		var unread uint32
		if ok, _, err := getNumberOfConsoleInputEvents.Call(uintptr(input), uintptr(unsafe.Pointer(&unread))); ok == 0 {
			return fmt.Errorf("count the console's unread input: %w", err)
		}
		if unread == 0 {
			if woken > 0 {
				report.say(fmt.Sprintf("input sat unread in the console, so its reader was woken %s; it was read within %s of being written", wakes(woken), time.Since(last).Round(time.Millisecond)))
			}
			return nil
		}
		record := inputRecord{Kind: menuEvent}
		var count uint32
		if ok, _, err := writeConsoleInput.Call(uintptr(input), uintptr(unsafe.Pointer(&record)), 1, uintptr(unsafe.Pointer(&count))); ok == 0 {
			return fmt.Errorf("wake the console's reader: %w", err)
		}
		woken++
		if look++; look == len(wakeLooks) {
			report.say(fmt.Sprintf("input sat unread in the console for %s and its reader was woken %s; %d input records are still unread", time.Since(last).Round(time.Millisecond), wakes(woken), unread))
			return nil
		}
		timer.Reset(wakeLooks[look])
	}
}

// wakes says how many times the reader was woken.
func wakes(count int) string {
	if count == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", count)
}

// reporter says what the waker has to say about its wakes at most once a
// minute, counting what it held back since: a program slow to read can leave
// every key unread past the first look, and its host's log keeps every line.
type reporter struct {
	out  io.Writer
	last time.Time
	held int
}

func (r *reporter) say(line string) {
	if !r.last.IsZero() && time.Since(r.last) < time.Minute {
		r.held++
		return
	}
	if r.held > 0 {
		line += fmt.Sprintf(" (and %d more since the last of these)", r.held)
	}
	fmt.Fprintln(r.out, line)
	r.last, r.held = time.Now(), 0
}
