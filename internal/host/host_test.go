package host

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// testWait bounds each wait in these tests, so a host that stops answering
// fails the test instead of hanging it.
const testWait = 10 * time.Second

const testToken = "token"

// fakeTerminal stands in for a pseudo console: the test writes its program's
// output and reads back what was typed into it and each resize.
type fakeTerminal struct {
	output  *io.PipeReader
	program *io.PipeWriter
	typed   chan []byte
	resized chan [2]int
	done    chan struct{}
	ending  sync.Once
	code    uint32
}

func newFakeTerminal(code uint32) *fakeTerminal {
	output, program := io.Pipe()
	return &fakeTerminal{output: output, program: program, typed: make(chan []byte, 16), resized: make(chan [2]int, 16), done: make(chan struct{}), code: code}
}

func (f *fakeTerminal) Read(p []byte) (int, error) { return f.output.Read(p) }

func (f *fakeTerminal) Write(p []byte) (int, error) {
	f.typed <- bytes.Clone(p)
	return len(p), nil
}

func (f *fakeTerminal) Resize(cols, rows int) error {
	f.resized <- [2]int{cols, rows}
	return nil
}

func (f *fakeTerminal) Screen(timeout time.Duration) ([]string, error) {
	return nil, errors.New("the fake terminal has no screen")
}

func (f *fakeTerminal) PID() int { return 4242 }

func (f *fakeTerminal) Done() <-chan struct{} { return f.done }

func (f *fakeTerminal) ExitCode() uint32 {
	<-f.done
	return f.code
}

// exit ends the program by itself: its output ends and Done is closed.
func (f *fakeTerminal) exit() {
	f.ending.Do(func() {
		f.program.Close()
		close(f.done)
	})
}

func (f *fakeTerminal) Close() error {
	f.exit()
	return nil
}

// receive takes the next value from values, and fails the test once testWait
// passes without one.
func receive[T any](t *testing.T, values <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(testWait):
		t.Fatalf("no %s within %s", what, testWait)
		var zero T
		return zero
	}
}

// handshake hands host one end of a fresh in-memory connection and says
// greeting on the other, which it returns as a client with the host's answer.
func handshake(t *testing.T, host func(hostConnection), greeting hello) (*Client, hello) {
	t.Helper()
	hostSide, viewerSide := net.Pipe()
	t.Cleanup(func() { viewerSide.Close() })
	go host(hostSide)
	if err := viewerSide.SetDeadline(time.Now().Add(testWait)); err != nil {
		t.Fatal(err)
	}
	if err := writeHello(viewerSide, greeting); err != nil {
		t.Fatal(err)
	}
	answer, err := readHello(viewerSide)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{pipe: viewerSide, his: answer.His, view: answer.View, isToldSizes: answer.Sizes, history: answer.History}, answer
}

// serveWith serves each connection it is handed as a terminal host's serve
// does, for a client that knows testToken.
func serveWith(console terminal, output *history, closing chan<- struct{}) func(hostConnection) {
	return func(connection hostConnection) {
		serve(connection, testToken, console, output, closing, &sync.RWMutex{})
	}
}

// showUntil reads viewer's events until its output shows want, and returns
// them as one transcript: each size as [size COLSxROWS] at its place in the
// output.
func showUntil(t *testing.T, viewer *Client, want string) string {
	t.Helper()
	var shown strings.Builder
	for !strings.Contains(shown.String(), want) {
		event, err := viewer.Next()
		if err != nil {
			t.Fatalf("Next: %v, with %q shown so far", err, shown.String())
		}
		if event.Cols > 0 {
			fmt.Fprintf(&shown, "[size %dx%d]", event.Cols, event.Rows)
		}
		shown.Write(event.Output)
	}
	return shown.String()
}

// runFakeHost runs console's terminal as Run does once it has recorded the
// host, with viewers connected through connect, and returns the record, the
// state directory and the run's result.
func runFakeHost(t *testing.T, console *fakeTerminal) (record Record, stateDir string, connect func(hostConnection), ran <-chan error) {
	t.Helper()
	stateDir = t.TempDir()
	record = Record{ID: "g1", Pipe: "in-memory", Token: testToken, Version: Version, HostPID: os.Getpid(), ChildPID: console.PID(), Started: time.Now().UTC()}
	if err := writeRecord(stateDir, record); err != nil {
		t.Fatal(err)
	}
	output := newTestHistory(t, 80, 24)
	connections := make(chan hostConnection)
	result := make(chan error, 1)
	go func() {
		result <- runTerminal(stateDir, Spec{ID: record.ID}, record, console, output, func() (hostConnection, error) { return <-connections, nil })
	}()
	return record, stateDir, func(connection hostConnection) { connections <- connection }, result
}

func TestInputFromAViewerOrADeliveryReachesTheTerminal(t *testing.T) {
	tests := []struct {
		name     string
		greeting hello
		send     func(client *Client, input []byte) error
	}{
		{name: "a viewer", greeting: hello{Version: Version, Token: testToken, Sizes: true}, send: (*Client).Input},
		{name: "a delivery", greeting: hello{Version: Version, Token: testToken, Deliver: true}, send: func(client *Client, input []byte) error {
			return (&Delivery{client: client}).Write(input)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			console := newFakeTerminal(0)
			client, _ := handshake(t, serveWith(console, newTestHistory(t, 80, 24), make(chan struct{}, 1)), test.greeting)

			// Act
			err := test.send(client, []byte("typed keys"))

			// Assert
			if err != nil {
				t.Fatalf("sending the input: %v", err)
			}
			if typed := receive(t, console.typed, "input in the terminal"); string(typed) != "typed keys" {
				t.Errorf("the terminal was typed %q, want %q", typed, "typed keys")
			}
		})
	}
}

func TestAResizeReachesTheTerminalAndEveryViewerAtItsPlaceInTheOutput(t *testing.T) {
	// Arrange
	console := newFakeTerminal(0)
	output := newTestHistory(t, 80, 24)
	output.write([]byte("before"))
	viewers := make([]*Client, 2)
	for i := range viewers {
		viewers[i], _ = handshake(t, serveWith(console, output, make(chan struct{}, 1)), hello{Version: Version, Token: testToken, Sizes: true})
	}

	// Act
	err := viewers[0].Resize(100, 30)
	resized := receive(t, console.resized, "resize of the terminal")
	output.write([]byte("after"))

	// Assert
	if err != nil || resized != [2]int{100, 30} {
		t.Fatalf("Resize = %v, and the terminal was resized to %v; want 100x30", err, resized)
	}
	inOrder := regexp.MustCompile(`(?s)^\[size 80x24\]before\[size 100x30\].*after`)
	for i, viewer := range viewers {
		if shown := showUntil(t, viewer, "after"); !inOrder.MatchString(shown) {
			t.Errorf("viewer %d was shown %q; want the first size, the output before the resize, the new size, then the output after it", i, shown)
		}
	}
}

func TestATerminalsEndIsRecordedAndEveryViewerHearsItsExitCode(t *testing.T) {
	tests := []struct {
		name       string
		end        func(closer *Client, console *fakeTerminal) error
		wantClosed bool
	}{
		{name: "a viewer closes it", end: func(closer *Client, console *fakeTerminal) error { return closer.CloseTerminal() }, wantClosed: true},
		{name: "its program ends by itself", end: func(closer *Client, console *fakeTerminal) error {
			console.exit()
			return nil
		}, wantClosed: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			console := newFakeTerminal(7)
			record, stateDir, connect, ran := runFakeHost(t, console)
			greeting := hello{Version: Version, Token: testToken, Sizes: true}
			closer, _ := handshake(t, connect, greeting)
			watcher, _ := handshake(t, connect, greeting)
			ends := make(chan Event, 2)
			for _, viewer := range []*Client{closer, watcher} {
				go func() {
					for {
						event, err := viewer.Next()
						if err != nil || event.Exited {
							ends <- event
							return
						}
					}
				}()
			}

			// Act
			err := test.end(closer, console)

			// Assert
			if err != nil {
				t.Fatalf("ending the terminal: %v", err)
			}
			if err := receive(t, ran, "end of the host's run"); err != nil {
				t.Errorf("the run ended with %v, want nil", err)
			}
			for range 2 {
				if heard := receive(t, ends, "viewer's end"); !heard.Exited || heard.Code != 7 {
					t.Errorf("a viewer heard %+v at its end, want the exit code 7", heard)
				}
			}
			end, err := ReadEnd(stateDir, record.ID)
			if err != nil || end.Code != 7 || end.Closed != test.wantClosed || end.ChildPID != record.ChildPID {
				t.Errorf("the recorded end is %+v, %v; want code 7, closed %t, child pid %d", end, err, test.wantClosed, record.ChildPID)
			}
		})
	}
}

func TestTheHostAnswersATerminalsQueryIntoItsInput(t *testing.T) {
	// Arrange
	console := newFakeTerminal(0)
	_, _, _, ran := runFakeHost(t, console)
	defer func() {
		console.exit()
		receive(t, ran, "end of the host's run")
	}()

	// Act
	_, err := console.program.Write([]byte("status?\x1b[5n"))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if typed := receive(t, console.typed, "answer in the terminal's input"); string(typed) != "\x1b[0n" {
		t.Errorf("the terminal was typed %q, want the status report %q", typed, "\x1b[0n")
	}
}

func TestServeRefusesAClientOfAnotherVersionTokenOrKindOfHost(t *testing.T) {
	tests := []struct {
		name        string
		greeting    hello
		wantRefusal string
	}{
		{name: "another version", greeting: hello{Version: Version + 1, Token: testToken}, wantRefusal: fmt.Sprintf("this host speaks protocol %d, not %d", Version, Version+1)},
		{name: "another token", greeting: hello{Version: Version, Token: "not the token"}, wantRefusal: "the token does not match this host"},
		{name: "a stream session's client", greeting: hello{Version: Version, Token: testToken, Stream: true}, wantRefusal: "this host runs a terminal, not a stream session"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			console := newFakeTerminal(0)

			// Act
			client, answer := handshake(t, serveWith(console, newTestHistory(t, 80, 24), make(chan struct{}, 1)), test.greeting)

			// Assert
			if answer.Error != test.wantRefusal {
				t.Errorf("the host answered %+v, want the refusal %q", answer, test.wantRefusal)
			}
			if _, _, err := readFrame(client.pipe); !errors.Is(err, io.EOF) {
				t.Errorf("after the refusal the connection read %v, want it closed", err)
			}
		})
	}
}
