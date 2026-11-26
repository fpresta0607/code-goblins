//go:build linux || darwin

package pty

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// output is what a terminal's program wrote, read until Read returns an
// error, which end keeps.
type output struct {
	mu    sync.Mutex
	text  strings.Builder
	err   error
	ended chan struct{}
}

// start runs script under sh in a terminal of cols by rows, reads its output
// as it comes, and closes the terminal when the test ends.
func start(t *testing.T, script string, cols, rows int) (*Terminal, *output) {
	t.Helper()
	return startSpec(t, Spec{Args: []string{"sh", "-c", script}, Cols: cols, Rows: rows})
}

// startSpec runs spec in a terminal, reads its output as it comes, and closes
// the terminal when the test ends.
func startSpec(t *testing.T, spec Spec) (*Terminal, *output) {
	t.Helper()
	terminal, err := Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	written := &output{ended: make(chan struct{})}
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := terminal.Read(buffer)
			written.mu.Lock()
			written.text.Write(buffer[:n])
			if err != nil {
				written.err = err
				written.mu.Unlock()
				close(written.ended)
				return
			}
			written.mu.Unlock()
		}
	}()
	return terminal, written
}

func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.text.String()
}

// await waits until the program has written pattern, and returns its first
// match.
func (o *output) await(t *testing.T, pattern string) []string {
	t.Helper()
	expression := regexp.MustCompile(pattern)
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if match := expression.FindStringSubmatch(o.String()); match != nil {
			return match
		}
	}
	t.Fatalf("the program did not write %q within 30 seconds; it wrote %q", pattern, o.String())
	return nil
}

// end waits until Read has returned an error, and returns all the program
// wrote and that error.
func (o *output) end(t *testing.T) (string, error) {
	t.Helper()
	select {
	case <-o.ended:
	case <-time.After(30 * time.Second):
		t.Fatalf("the terminal's output did not end within 30 seconds; it holds %q", o.String())
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.text.String(), o.err
}

func TestATerminalCarriesInputToItsProgramAndItsOutputBackUntilTheEnd(t *testing.T) {
	// Arrange
	terminal, written := start(t, `read line; echo "got $line"`, 80, 24)

	// Act
	if _, err := terminal.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}

	// Assert
	text, err := written.end(t)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Read ended with %v, not io.EOF", err)
	}
	if !strings.Contains(text, "got hello") {
		t.Fatalf("the program wrote %q, without got hello", text)
	}
	if code := terminal.ExitCode(); code != 0 {
		t.Fatalf("exit code %d, not 0", code)
	}
}

func TestReadEndsOnceTheProgramExitsWhileAHelperStillHoldsTheTerminal(t *testing.T) {
	// Arrange: the helper ignores the hangup its session's leader sends it on
	// exiting, and keeps the terminal open.
	terminal, written := start(t, `trap '' HUP; sleep 600 & echo "helper $!"; exit 0`, 80, 24)

	// Act
	text, err := written.end(t)

	// Assert
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Read ended with %v, not io.EOF", err)
	}
	match := regexp.MustCompile(`helper (\d+)`).FindStringSubmatch(text)
	if match == nil {
		t.Fatalf("the program wrote %q, without its helper", text)
	}
	helper, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := proc.Lookup(helper); err != nil {
		t.Fatalf("the helper %d no longer runs (%v), so nothing held the terminal", helper, err)
	}
	if err := terminal.Close(); err != nil {
		t.Fatal(err)
	}
	awaitGone(t, helper)
}

func TestATerminalTellsItsProgramItsSizeAndEachResize(t *testing.T) {
	// Arrange
	terminal, written := start(t, `stty size; read line; stty size`, 100, 30)
	written.await(t, `30 100`)

	// Act
	if err := terminal.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}

	// Assert
	written.await(t, `40 120`)
}

func TestAResizedTerminalStaysNonblocking(t *testing.T) {
	// Arrange: a blocking file would wait out a read's deadline.
	terminal, err := Start(Spec{Args: []string{"sh", "-c", "read line"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	if err := terminal.Resize(120, 40); err != nil {
		t.Fatal(err)
	}

	// Act
	// stall-accepted: the deadline is what the test measures, a read the poller ends once it passes.
	if err := terminal.ptmx.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	go func() {
		_, err := terminal.ptmx.Read(make([]byte, 4096))
		read <- err
	}()

	// Assert
	select {
	case err := <-read:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("a read of the idle terminal past its deadline gave %v, not os.ErrDeadlineExceeded", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a read of the idle terminal still waits past its deadline, so its file turned blocking")
	}
}

func TestTheProgramLeadsItsOwnSessionWithTheTerminalAsItsControllingTerminal(t *testing.T) {
	// Arrange: /dev/tty opens only for a process with a controlling terminal.
	terminal, written := start(t, `printf 'input %s end\n' "$(tty)"; if : < /dev/tty; then echo controlling; fi; read line`, 80, 24)

	// Act
	match := written.await(t, `input (\S+) end`)
	written.await(t, `controlling`)

	// Assert
	if !strings.HasPrefix(match[1], "/dev/") {
		t.Fatalf("the program's input is %q, not a terminal device", match[1])
	}
	session, err := unix.Getsid(terminal.PID())
	if err != nil {
		t.Fatal(err)
	}
	if session != terminal.PID() {
		t.Fatalf("the program %d is in session %d, not its own", terminal.PID(), session)
	}
}

func TestTheExitCodeIsTheProgramsOwnOr128AndTheSignalThatEndedIt(t *testing.T) {
	for _, test := range []struct {
		name   string
		script string
		code   uint32
	}{
		{"an exit", "exit 3", 3},
		{"a signal", "kill -TERM $$", 128 + uint32(unix.SIGTERM)},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			terminal, written := start(t, test.script, 80, 24)

			// Act
			_, _ = written.end(t)

			// Assert
			if code := terminal.ExitCode(); code != test.code {
				t.Fatalf("exit code %d, not %d", code, test.code)
			}
		})
	}
}

func TestCloseEndsTheProgramAndWhatItStarted(t *testing.T) {
	for _, test := range []struct {
		name   string
		script string
	}{
		{"a helper it waits for", `sleep 600 & echo "helper $!"; wait`},
		// env -i starts the helper with no mark and its parent exits, so only
		// the session's group still finds it, and it ignores the hangup the
		// program sends it as it ends.
		{"a helper that cleared its environment and outlived its parent", `(trap '' HUP; env -i sleep 600 & echo "helper $!"); read line`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			terminal, written := start(t, test.script, 80, 24)
			helper, err := strconv.Atoi(written.await(t, `helper (\d+)`)[1])
			if err != nil {
				t.Fatal(err)
			}

			// Act
			if err := terminal.Close(); err != nil {
				t.Fatal(err)
			}

			// Assert
			select {
			case <-terminal.Done():
			default:
				t.Fatal("Close returned before the program exited")
			}
			awaitGone(t, helper)
		})
	}
}

// awaitGone waits until helper no longer runs.
func awaitGone(t *testing.T, helper int) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := proc.Lookup(helper); errors.Is(err, fs.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the helper %d the program started still runs", helper)
		}
	}
}

// A program that exits at once still has when it started, which the host's
// record keeps, since it is read before anything waits for the program: here
// it has exited before the read.
func TestAProgramThatExitsAtOnceStillHasItsStart(t *testing.T) {
	// Arrange
	before := time.Now().Add(-time.Minute)
	beforeStartRead = func(pid int) { awaitGone(t, pid) }
	t.Cleanup(func() { beforeStartRead = nil })

	// Act
	terminal, written := start(t, `exit 7`, 80, 24)
	_, _ = written.end(t)
	<-terminal.Done()

	// Assert
	started := terminal.StartTime()
	if started.Before(before) || started.After(time.Now().Add(time.Minute)) {
		t.Fatalf("the program started at %v, not within a minute of now", started)
	}
	if code := terminal.ExitCode(); code != 7 {
		t.Fatalf("the program exited with %d, not 7", code)
	}
}

func TestATerminalRefusesASizeOutsideTwoTo1000Cells(t *testing.T) {
	// Arrange
	terminal, _ := start(t, `read line`, 80, 24)
	for _, size := range []struct{ cols, rows int }{{1, 24}, {80, 1}, {1001, 24}, {80, 1001}} {
		// Act
		_, startErr := Start(Spec{Args: []string{"sh", "-c", "exit 0"}, Cols: size.cols, Rows: size.rows})
		resizeErr := terminal.Resize(size.cols, size.rows)

		// Assert
		if startErr == nil || resizeErr == nil {
			t.Fatalf("a %dx%d terminal: Start gave %v and Resize %v, not both refused", size.cols, size.rows, startErr, resizeErr)
		}
	}
}

func TestStartRefusesAProgramItCannotFindAndKeepsNoDescriptor(t *testing.T) {
	// Arrange: a first refusal opens what this process keeps for every file,
	// the poller's descriptors, so the second shows only what it leaks.
	spec := Spec{Args: []string{"/nonexistent/goblins-program"}, Cols: 80, Rows: 24}
	if _, err := Start(spec); err == nil {
		t.Fatal("Start ran a program that does not exist")
	}
	before := openDescriptors(t)

	// Act
	_, err := Start(spec)

	// Assert
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Start gave %v, not a missing program", err)
	}
	if after := openDescriptors(t); after != before {
		t.Fatalf("%d descriptors were open before a refused Start and %d after", before, after)
	}
}

// openDescriptors counts the files this process has open.
func openDescriptors(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}
