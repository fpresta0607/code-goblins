package host

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/conpty"
)

// streamCloseWait bounds how long a stream session's program has to end by
// itself once a client closes the session, which closes its input, before
// the host ends it and everything it started. Claude Code ends at the end of
// its input, its transcript written. Only time the machine ran spends it.
var streamCloseWait = 5 * time.Second

// runStream hosts spec's program on pipes until it ends or a client closes
// it, as Run hosts a terminal: the same job, record, proof and end, with
// lines in place of a terminal's output and screen.
func runStream(stateDir string, spec Spec, proof string, processors uintptr) error {
	program, err := conpty.StartPiped(conpty.PipedSpec{Args: spec.Args, Dir: spec.Dir, Env: terminalEnvironment(spec.ID, proof), Stderr: os.Stderr, Processors: processors})
	if err != nil {
		return err
	}
	record, pipe, err := announce(stateDir, spec.ID, program.PID(), proof, true)
	if err != nil {
		_ = program.Close()
		return err
	}
	defer removeRecord(stateDir, spec.ID, record.HostPID)

	lines := newLineHistory()
	outputEnded := make(chan struct{})
	go func() {
		defer close(outputEnded)
		defer lines.end()
		output := bufio.NewReader(program)
		for {
			line, length, err := readStreamLine(output)
			if errors.Is(err, errLineTooLong) {
				fmt.Fprintf(os.Stderr, "%s host: left out a %d-byte line of the session, over the %d-byte bound\n", time.Now().UTC().Format(time.RFC3339Nano), length, maxStreamLine)
				continue
			}
			if err != nil {
				return
			}
			if len(line) == 0 {
				continue
			}
			object, err := streamObject(line)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s host: the program wrote a line that is not a JSON object: %.200s\n", time.Now().UTC().Format(time.RFC3339Nano), line)
				continue
			}
			lines.add(object, false)
		}
	}()
	closing := make(chan struct{}, 1)
	// writing keeps each line whole in the program's input, and the lines
	// written in the history in the order the program reads them. A line is
	// kept before it is written, so it comes before anything the program
	// writes once it read it; one its closed input refused stays kept, as
	// the session ends.
	var writing sync.Mutex
	write := func(line json.RawMessage) error {
		writing.Lock()
		defer writing.Unlock()
		lines.add(line, true)
		_, err := program.Write(append(line, '\n'))
		return err
	}
	var clients sync.WaitGroup
	go func() {
		for {
			connection, err := pipe.accept()
			if err != nil {
				// A client that left before it connected costs a pause, never
				// a spin.
				time.Sleep(50 * time.Millisecond)
				continue
			}
			clients.Add(1)
			go func() {
				defer clients.Done()
				serveStream(connection, record.Token, program, lines, write, closing)
			}()
		}
	}()

	isClosed := false
	select {
	case <-outputEnded:
	case <-closing:
		isClosed = true
		_ = program.CloseInput()
		ending, cancelEnding := awake.WithTimeout(context.Background(), streamCloseWait)
		select {
		case <-outputEnded:
		case <-ending.Done():
		}
		cancelEnding()
	}
	closeErr := program.Close()
	<-outputEnded
	end := End{ID: spec.ID, HostPID: record.HostPID, ChildPID: record.ChildPID, ChildStart: record.ChildStart, Ended: time.Now().UTC(), Code: program.ExitCode(), Closed: isClosed}
	if err := writeEnd(stateDir, end); err != nil {
		fmt.Fprintf(os.Stderr, "%s host: record how session %s ended: %v\n", time.Now().UTC().Format(time.RFC3339Nano), spec.ID, err)
	}
	farewell := make(chan struct{})
	go func() {
		clients.Wait()
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

// serveStream is one client's connection to a stream session. A viewer is
// sent the lines kept, then each line as it comes, then how the program
// ended. A writer's lines are written to the program, each acknowledged
// once written or with why not. Either may close the session.
func serveStream(connection *os.File, token string, program *conpty.Piped, lines *lineHistory, write func(json.RawMessage) error, closing chan<- struct{}) {
	defer connection.Close()
	greeting, ok := greet(connection, token, true)
	if !ok {
		return
	}
	askClose := func() {
		select {
		case closing <- struct{}{}:
		default:
		}
	}
	if writeHello(connection, hello{Version: Version, Stream: true, Deliver: greeting.Deliver}) != nil {
		return
	}
	if greeting.Deliver {
		for {
			kind, payload, err := readFrame(connection)
			if err != nil {
				return
			}
			switch kind {
			case frameLine:
				var refusal []byte
				if line, err := streamObject(payload); err != nil {
					refusal = []byte(err.Error())
				} else if err := write(line); err != nil {
					refusal = []byte(err.Error())
				}
				if writeFrame(connection, frameAck, refusal) != nil {
					return
				}
			case frameClose:
				askClose()
			default:
				return
			}
		}
	}
	past, feed, detach := lines.attach()
	defer detach()
	reading := make(chan struct{})
	go func() {
		// A viewer that leaves or sends a broken frame is let go, which ends
		// the loop below.
		defer close(reading)
		defer detach()
		for {
			kind, _, err := readFrame(connection)
			if err != nil {
				return
			}
			if kind == frameClose {
				askClose()
			}
		}
	}()
	// A viewer reads each line byte for byte as its program wrote it, which
	// json.Marshal would not keep, since it writes each <, > and & in a line
	// as a Unicode escape.
	send := func(batch []StreamLine) error {
		for _, line := range batch {
			var payload bytes.Buffer
			encoder := json.NewEncoder(&payload)
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(line); err != nil {
				return err
			}
			if err := writeFrame(connection, frameLine, bytes.TrimSuffix(payload.Bytes(), []byte("\n"))); err != nil {
				return err
			}
		}
		return nil
	}
	if send(past) != nil {
		<-reading
		return
	}
	for {
		batch, open := feed.take()
		if !open {
			break
		}
		if send(batch) != nil {
			<-reading
			return
		}
	}
	if !lines.ended() {
		return
	}
	<-program.Done()
	var code [4]byte
	binary.BigEndian.PutUint32(code[:], program.ExitCode())
	_ = writeFrame(connection, frameExit, code[:])
}

// StreamViewer reads a stream session's lines: those its host kept, then
// each as it comes, whoever wrote it, then how the program ended.
type StreamViewer struct {
	pipe    *os.File
	sending sync.Mutex
}

// StreamEvent is a line of the session, or its program's end with its exit
// code.
type StreamEvent struct {
	Line     StreamLine
	IsExited bool
	Code     uint32
}

// WatchStream connects to the stream host record names as a viewer.
func WatchStream(record Record) (*StreamViewer, error) {
	client, answer, err := dial(record, hello{Version: Version, Token: record.Token, Stream: true})
	if err != nil {
		return nil, err
	}
	if !answer.Stream {
		_ = client.Close()
		return nil, errNotStream
	}
	return &StreamViewer{pipe: client.pipe}, nil
}

// errNotStream is a host that runs a terminal: one from before stream
// sessions, which answered as to a terminal's viewer.
var errNotStream = errors.New("host: this host runs a terminal, not a stream session")

// Next reads the next line, or the program's end.
func (v *StreamViewer) Next() (StreamEvent, error) {
	kind, payload, err := readFrame(v.pipe)
	if err != nil {
		return StreamEvent{}, err
	}
	switch kind {
	case frameLine:
		var line StreamLine
		if err := json.Unmarshal(payload, &line); err != nil {
			return StreamEvent{}, fmt.Errorf("host: malformed line frame: %w", err)
		}
		return StreamEvent{Line: line}, nil
	case frameExit:
		if len(payload) != 4 {
			return StreamEvent{}, errors.New("host: malformed exit frame")
		}
		return StreamEvent{IsExited: true, Code: binary.BigEndian.Uint32(payload)}, nil
	default:
		return StreamEvent{}, fmt.Errorf("host: unexpected frame %q", kind)
	}
}

// CloseSession asks the host to close the program's input, give it
// streamCloseWait to end, and then end it and everything it started.
func (v *StreamViewer) CloseSession() error {
	v.sending.Lock()
	defer v.sending.Unlock()
	return writeFrame(v.pipe, frameClose, nil)
}

// Close leaves the session running and disconnects this viewer.
func (v *StreamViewer) Close() error {
	return v.pipe.Close()
}

// StreamWriter writes lines into a stream session and learns, line by line,
// that its host wrote each into the program's input.
type StreamWriter struct {
	pipe *os.File
}

// DialStreamWriter connects to the stream host record names to write lines.
func DialStreamWriter(record Record) (*StreamWriter, error) {
	client, answer, err := dial(record, hello{Version: Version, Token: record.Token, Stream: true, Deliver: true})
	if err != nil {
		return nil, err
	}
	if !answer.Stream || !answer.Deliver {
		_ = client.Close()
		return nil, errNotStream
	}
	return &StreamWriter{pipe: client.pipe}, nil
}

// Write sends line, one JSON object, and returns once the host wrote it into
// the program's input, or why it did not. A program that has stopped reading
// leaves the line unconfirmed after deliveryTimeout, not lost.
func (w *StreamWriter) Write(line []byte) error {
	if err := writeFrame(w.pipe, frameLine, line); err != nil {
		return fmt.Errorf("host: send the line: %w", err)
	}
	lift := awake.Deadline(w.pipe.SetReadDeadline, deliveryTimeout)
	kind, payload, err := readFrame(w.pipe)
	lift()
	if err != nil {
		return fmt.Errorf("host: no confirmation that the session took the line: %w", err)
	}
	if kind != frameAck {
		return fmt.Errorf("host: answered the line with frame %q, not a confirmation", kind)
	}
	if len(payload) > 0 {
		return fmt.Errorf("host: the session refused the line: %s", payload)
	}
	return nil
}

// CloseSession asks the host to close the session, as StreamViewer's does.
func (w *StreamWriter) CloseSession() error {
	return writeFrame(w.pipe, frameClose, nil)
}

// Close disconnects this writer and leaves the session running.
func (w *StreamWriter) Close() error {
	return w.pipe.Close()
}
