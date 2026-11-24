package host

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
)

// handshakeTimeout bounds how long a client takes to say hello, and
// farewellTimeout how long an ending host waits for its viewers to hear how
// the terminal ended. Only time the machine ran spends either (awake), since
// a pause of the whole machine stops the other side too.
var (
	handshakeTimeout = 5 * time.Second
	farewellTimeout  = 2 * time.Second
)

// hostConnection is one connection between a host and a client, as either
// side holds it: a Windows named pipe's file, or a net.Conn.
type hostConnection interface {
	io.ReadWriteCloser
	SetDeadline(t time.Time) error
	SetReadDeadline(t time.Time) error
}

// terminal is what a host runs and serves its viewers: a Windows pseudo
// console (conpty.Console). Read returns what its program wrote, and io.EOF
// once that has all been read; Write types into its input. Screen reads its
// screen as the system holds it, where the system holds one. Done is closed
// once its program has exited, and ExitCode waits for that and returns the
// program's exit code. Close ends the program and everything it started; the
// host calls it once.
type terminal interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	Screen(timeout time.Duration) ([]string, error)
	Done() <-chan struct{}
	ExitCode() uint32
	Close() error
}

// RunArgs runs a host from its command line: --state, --id, --dir, --cols,
// --rows, --leave-cores-for-apps and --stream, then -- and the command to run.
func RunArgs(args []string) error {
	flags := flag.NewFlagSet("host", flag.ContinueOnError)
	stateDir := flags.String("state", "", "the CEO home's state directory, where the host records itself")
	var spec Spec
	flags.StringVar(&spec.ID, "id", "", "the terminal's id")
	flags.StringVar(&spec.Dir, "dir", "", "the directory the command starts in")
	flags.IntVar(&spec.Cols, "cols", 120, "the terminal's width in cells")
	flags.IntVar(&spec.Rows, "rows", 40, "the terminal's height in cells")
	flags.BoolVar(&spec.ShouldLeaveCoresForApps, "leave-cores-for-apps", false, "keep the terminal off the performance cores the fleet's work leaves to the Overlord's own apps")
	flags.BoolVar(&spec.IsStream, "stream", false, "run the command on pipes, one JSON object a line, rather than in a terminal")
	if err := flags.Parse(args); err != nil {
		return err
	}
	spec.Args = flags.Args()
	if *stateDir == "" || len(spec.Args) == 0 {
		return errors.New("host: --state and a command after -- are required")
	}
	return Run(*stateDir, spec)
}

// runTerminal serves console, whose output history keeps, to each viewer
// accept takes, until the terminal ends or a viewer closes it. Then it closes
// console, records how the terminal ended under stateDir, and gives its
// viewers up to farewellTimeout to hear how.
func runTerminal(stateDir string, spec Spec, record Record, console terminal, output *history, accept func() (hostConnection, error)) error {
	// The terminal's screen answers its program's queries, typed in by a
	// goroutine of their own so the output reader never waits on the
	// console's input: a console that reads no input until its output is
	// read would stall both.
	var answering sync.Mutex
	var answers []byte
	answered := make(chan struct{}, 1)
	go func() {
		for range answered {
			answering.Lock()
			typed := answers
			answers = nil
			answering.Unlock()
			if len(typed) > 0 {
				_, _ = console.Write(typed)
			}
		}
	}()
	// outputEnded closes once the terminal's output is read to its end, which
	// comes only after its process has exited and its pseudo console closed.
	outputEnded := make(chan struct{})
	go func() {
		defer close(answered)
		for {
			chunk := make([]byte, 32<<10)
			n, err := console.Read(chunk)
			if n > 0 {
				if answer := output.write(chunk[:n]); len(answer) > 0 {
					answering.Lock()
					answers = append(answers, answer...)
					answering.Unlock()
					select {
					case answered <- struct{}{}:
					default:
					}
				}
			}
			if err != nil {
				output.end()
				close(outputEnded)
				return
			}
		}
	}()
	closing := make(chan struct{}, 1)
	// screens is held shared by every screen read and whole to close the
	// console, so a read in flight is answered before the console closes.
	var screens sync.RWMutex
	var viewers sync.WaitGroup
	go func() {
		for {
			connection, err := accept()
			if err != nil {
				// A client that left before it connected costs a pause, never
				// a spin.
				time.Sleep(50 * time.Millisecond)
				continue
			}
			viewers.Add(1)
			go func() {
				defer viewers.Done()
				serve(connection, record.Token, console, output, closing, &screens)
			}()
		}
	}()

	// The terminal's last output is read before Close, which ends whatever
	// the process left running and discards the output of a terminal that is
	// closed early.
	isClosed := false
	select {
	case <-outputEnded:
	case <-closing:
		isClosed = true
	}
	screens.Lock()
	closeErr := console.Close()
	screens.Unlock()
	// How the program ended is kept before the record goes, so whoever finds
	// the terminal ended finds how.
	end := End{ID: spec.ID, HostPID: record.HostPID, ChildPID: record.ChildPID, ChildStart: record.ChildStart, Ended: time.Now().UTC(), Code: console.ExitCode(), Closed: isClosed}
	if err := writeEnd(stateDir, end); err != nil {
		fmt.Fprintf(os.Stderr, "%s host: record how terminal %s ended: %v\n", time.Now().UTC().Format(time.RFC3339Nano), spec.ID, err)
	}
	farewell := make(chan struct{})
	go func() {
		viewers.Wait()
		close(farewell)
	}()
	farewelling, cancelFarewelling := awake.WithTimeout(context.Background(), farewellTimeout)
	defer cancelFarewelling()
	select {
	case <-farewell:
	case <-farewelling.Done():
	}
	return closeErr
}

// reach connects to the host record names as the client its kind of host
// takes, a stream session's or a terminal's, to learn that it answers or to
// ask it to close.
func reach(record Record) (*Client, error) {
	client, _, err := dial(record, hello{Version: Version, Token: record.Token, Stream: record.Stream})
	return client, err
}

// Answer connects to the host record names, a terminal's or a stream
// session's, and leaves, which proves the host answers, or says why it did
// not.
func Answer(record Record) error {
	client, err := reach(record)
	if err != nil {
		return err
	}
	return client.Close()
}

// isServing reports whether the host record names answers.
func isServing(record Record) bool {
	return Answer(record) == nil
}

// greet reads a client's handshake and refuses a client of another version,
// with another token, or that asks for the other kind of host than this one.
func greet(connection hostConnection, token string, isStream bool) (hello, bool) {
	lift := awake.Deadline(connection.SetReadDeadline, handshakeTimeout)
	greeting, err := readHello(connection)
	lift()
	if err != nil {
		return hello{}, false
	}
	refusal := ""
	switch {
	case greeting.Version != Version:
		refusal = fmt.Sprintf("this host speaks protocol %d, not %d", Version, greeting.Version)
	case subtle.ConstantTimeCompare([]byte(greeting.Token), []byte(token)) != 1:
		refusal = "the token does not match this host"
	case greeting.Stream && !isStream:
		refusal = "this host runs a terminal, not a stream session"
	case !greeting.Stream && isStream:
		refusal = "this host runs a stream session, not a terminal"
	}
	if refusal != "" {
		_ = writeHello(connection, hello{Version: Version, Error: refusal})
		return hello{}, false
	}
	return greeting, true
}

// serve is one viewer's connection: the handshake, then input, resizes and a
// close request one way and the history and live output the other, until the
// terminal ends or the viewer leaves. Input is read from the handshake on, so a
// viewer that only types never waits on the output it does not read. A viewer
// that asked for sizes is told each at its place in the output, whichever
// viewer resized the terminal. A screen request gets the terminal's screen
// alone.
func serve(connection hostConnection, token string, console terminal, output *history, closing chan<- struct{}, screens *sync.RWMutex) {
	defer connection.Close()
	greeting, ok := greet(connection, token, false)
	if !ok {
		return
	}
	if greeting.Screen {
		serveScreen(connection, console, screens, output, greeting)
		return
	}
	if greeting.Deliver {
		serveDelivery(connection, console, output)
		return
	}
	past, sizes, feed, detach := output.attach()
	defer detach()
	answer := hello{Version: Version, Unread: greeting.Unread, View: feed.id, His: true}
	if greeting.Sizes {
		answer.Sizes, answer.History = true, len(past)
	}
	if writeHello(connection, answer) != nil {
		return
	}
	// stream writes output, and for a viewer that asked for sizes each size
	// at its place in it.
	stream := func(data []byte, sizes []geometry) error {
		if !greeting.Sizes {
			return writeFrame(connection, frameOutput, data)
		}
		start := 0
		for _, size := range sizes {
			if size.At > start {
				if err := writeFrame(connection, frameOutput, data[start:size.At]); err != nil {
					return err
				}
				start = size.At
			}
			payload, err := sizePayload(size.Cols, size.Rows)
			if err != nil {
				return err
			}
			if err := writeFrame(connection, frameSize, payload); err != nil {
				return err
			}
		}
		if start == len(data) {
			return nil
		}
		return writeFrame(connection, frameOutput, data[start:])
	}
	reading := make(chan struct{})
	go func() {
		// A viewer that leaves or sends a broken frame is detached, which
		// ends the output loop below.
		defer close(reading)
		defer detach()
		isHis := false
		for {
			kind, payload, err := readFrame(connection)
			if err != nil {
				return
			}
			switch kind {
			case frameSubmission:
				var mark submission
				if json.Unmarshal(payload, &mark) != nil || feed.submit(mark) != nil {
					return
				}
			case frameHis:
				isHis = true
			case frameInput:
				_, _ = console.Write(payload)
				output.typedFrom(feed, isHis)
			case frameResize:
				if cols, rows, ok := parseSize(payload); ok {
					_ = output.resize(cols, rows, func() error { return console.Resize(cols, rows) })
				}
			case frameClose:
				select {
				case closing <- struct{}{}:
				default:
				}
			}
		}
	}()
	// A write fails only once the viewer has left, so the input it sent
	// before leaving is read to the end before the connection closes.
	if stream(past, sizes) != nil {
		<-reading
		return
	}
	for {
		chunk, sizes, open := feed.next()
		if !open {
			break
		}
		if unread := feed.takeUnread(); unread != nil && greeting.Unread {
			state := []byte{0}
			if *unread {
				state[0] = 1
			}
			if writeFrame(connection, frameUnread, state) != nil {
				<-reading
				return
			}
		}
		// A viewer that did not ask for sizes has nothing to take from a
		// resize alone.
		if len(chunk) == 0 && !greeting.Sizes {
			continue
		}
		if stream(chunk, sizes) != nil {
			<-reading
			return
		}
	}
	if !output.ended() {
		return
	}
	<-console.Done()
	var code [4]byte
	binary.BigEndian.PutUint32(code[:], console.ExitCode())
	_ = writeFrame(connection, frameExit, code[:])
}
