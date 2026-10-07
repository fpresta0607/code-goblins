package conpty

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// standInWaker relays for a waker the test plays: asks reads what the waker
// would be sent, and answers writes what it says.
func standInWaker(t *testing.T) (w *waker, asks, answers *os.File) {
	t.Helper()
	asks, typed, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	report, answers, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w = &waker{typed: typed, report: report, lines: bufio.NewReader(report), answered: make(chan struct{})}
	done := make(chan struct{})
	go w.relay(make(chan struct{}), done, make(chan struct{}))
	t.Cleanup(func() {
		close(done)
		_ = answers.Close()
		_ = asks.Close()
	})
	return w, asks, answers
}

// waitForAsks reads count asks for the screen from what the waker is sent.
func waitForAsks(t *testing.T, asks io.Reader, count int) {
	t.Helper()
	sent := make([]byte, count)
	if _, err := io.ReadFull(asks, sent); err != nil {
		t.Fatalf("read what the waker was sent: %v", err)
	}
	for _, signal := range sent {
		if signal != screenAsked {
			t.Fatalf("the waker was sent %v, want only asks for the screen", sent)
		}
	}
}

// An answer that comes after its read gave up never answers a later read,
// which takes only an answer read after its own ask.
func TestAnAnswerTooLateForOneReadNeverAnswersTheNext(t *testing.T) {
	w, asks, answers := standInWaker(t)
	if _, err := w.screen(50 * time.Millisecond); err == nil || !strings.Contains(err.Error(), "within 50ms") {
		t.Fatalf("a read nothing answered = %v, want it given up within 50ms", err)
	}
	waitForAsks(t, asks, 1)
	fmt.Fprintln(answers, `screen 1 {"rows":["too late"]}`)
	read := make(chan []string, 1)
	go func() {
		rows, err := w.screen(10 * time.Second)
		if err != nil {
			t.Errorf("the second read: %v", err)
		}
		read <- rows
	}()
	waitForAsks(t, asks, 1)

	fmt.Fprintln(answers, `screen 2 {"rows":["in time"]}`)

	if rows := <-read; !slices.Equal(rows, []string{"in time"}) {
		t.Errorf("the second read = %q, want the answer to its own ask", rows)
	}
}

// A read waiting when the waker ends fails then, not when it times out, and
// so does every read after.
func TestAReadWaitingWhenTheWakerEndsFailsAtOnce(t *testing.T) {
	w, asks, answers := standInWaker(t)
	read := make(chan error, 1)
	go func() {
		_, err := w.screen(time.Minute)
		read <- err
	}()
	waitForAsks(t, asks, 1)

	_ = answers.Close()

	select {
	case err := <-read:
		if err == nil || !strings.Contains(err.Error(), "has ended") {
			t.Errorf("the read = %v, want an error saying the waker has ended", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the read still waits after the waker ended")
	}
	if _, err := w.screen(time.Minute); err == nil || !strings.Contains(err.Error(), "has ended") {
		t.Errorf("a read after = %v, want an error saying the waker has ended", err)
	}
}

// A screen the waker could not read is an error saying why.
func TestAScreenTheWakerCouldNotReadIsAnError(t *testing.T) {
	w, asks, answers := standInWaker(t)
	read := make(chan error, 1)
	go func() {
		rows, err := w.screen(10 * time.Second)
		if rows != nil {
			err = fmt.Errorf("rows %q with %w", rows, err)
		}
		read <- err
	}()
	waitForAsks(t, asks, 1)

	fmt.Fprintln(answers, `screen 1 {"error":"The handle is invalid."}`)

	if err := <-read; err == nil || !strings.Contains(err.Error(), "could not read its screen: The handle is invalid.") {
		t.Errorf("the read = %v, want the waker's error", err)
	}
}

// Reads that ask at once are each answered with the console's screen, read
// by its waker after they asked.
func TestTheWakerAnswersReadsThatAskAtOnce(t *testing.T) {
	console, s := startChild(t, Spec{Cols: 80, Rows: 25})
	typeLine(t, console, "hello goblin")
	s.waitFor(t, "got hello goblin")
	reads := make(chan error, 16)

	for range cap(reads) {
		go func() {
			rows, err := console.Screen(10 * time.Second)
			if err == nil && (len(rows) != 25 || len(rows[0]) != 80 || strings.TrimRight(rows[2], " ") != "got hello goblin") {
				err = fmt.Errorf("read %d rows: %q", len(rows), rows)
			}
			reads <- err
		}()
	}

	for range cap(reads) {
		if err := <-reads; err != nil {
			t.Errorf("Screen: %v", err)
		}
	}
}

// A host started by a cfo from before input wakers read screens starts its
// own program as a screen reader for every read, and an update puts this
// build at that program's path: this build attaches to the console and prints
// the screen the console's waker reads.
func TestThisBuildAnswersTheScreenReadOfAnOlderHost(t *testing.T) {
	console, s := startChild(t, Spec{Cols: 80, Rows: 25})
	typeLine(t, console, "hello goblin")
	s.waitFor(t, "got hello goblin")
	want, err := console.Screen(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reader := exec.Command(os.Args[0], strconv.Itoa(console.PID()))
	reader.Args[0] = olderHostScreenRole
	// Without a console of its own, the reader can attach to the terminal's.
	reader.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}

	output, err := reader.Output()

	if err != nil {
		t.Fatalf("the reader an older host starts failed: %v", err)
	}
	var rows []string
	if err := json.Unmarshal(output, &rows); err != nil {
		t.Fatalf("the reader printed %q: %v", output, err)
	}
	if !slices.Equal(rows, want) {
		t.Errorf("the older host's reader read\n%q\nthe waker read\n%q", rows, want)
	}
}
