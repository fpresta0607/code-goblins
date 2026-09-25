package host

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/conpty"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// handshakeTimeout bounds how long a client takes to say hello, and
// farewellTimeout how long an ending host waits for its viewers to hear how
// the terminal ended.
var (
	handshakeTimeout = 5 * time.Second
	farewellTimeout  = 2 * time.Second
)

// Spec is one terminal for a host to run.
type Spec struct {
	ID string
	// Args is the command and its arguments; the caller resolves Args[0] to
	// a program CreateProcess can run.
	Args       []string
	Dir        string
	Cols, Rows int
}

// RunArgs runs a host from its command line: --state, --id, --dir, --cols and
// --rows, then -- and the command to run.
func RunArgs(args []string) error {
	flags := flag.NewFlagSet("host", flag.ContinueOnError)
	stateDir := flags.String("state", "", "the CFO home's state directory, where the host records itself")
	var spec Spec
	flags.StringVar(&spec.ID, "id", "", "the terminal's id")
	flags.StringVar(&spec.Dir, "dir", "", "the directory the command starts in")
	flags.IntVar(&spec.Cols, "cols", 120, "the terminal's width in cells")
	flags.IntVar(&spec.Rows, "rows", 40, "the terminal's height in cells")
	if err := flags.Parse(args); err != nil {
		return err
	}
	spec.Args = flags.Args()
	if *stateDir == "" || len(spec.Args) == 0 {
		return errors.New("host: --state and a command after -- are required")
	}
	return Run(*stateDir, spec)
}

// Run hosts spec's terminal until it ends or a viewer closes it: it records
// itself under stateDir, serves viewers over its pipe, and removes its record
// on the way out. The terminal's process and everything it starts share one
// job, which ends with this process however it ends.
func Run(stateDir string, spec Spec) error {
	if err := state.ValidTaskID(spec.ID); err != nil {
		return err
	}
	console, err := conpty.Start(conpty.Spec{Args: spec.Args, Dir: spec.Dir, Cols: spec.Cols, Rows: spec.Rows})
	if err != nil {
		return err
	}
	record, pipe, err := announce(stateDir, spec.ID, console.PID())
	if err != nil {
		_ = console.Close()
		return err
	}
	defer removeRecord(stateDir, spec.ID, record.HostPID)

	output := newHistory()
	// outputEnded closes once the terminal's output is read to its end, which
	// comes only after its process has exited and its pseudo console closed.
	outputEnded := make(chan struct{})
	go func() {
		for {
			chunk := make([]byte, 32<<10)
			n, err := console.Read(chunk)
			if n > 0 {
				output.write(chunk[:n])
			}
			if err != nil {
				output.end()
				close(outputEnded)
				return
			}
		}
	}()
	closing := make(chan struct{}, 1)
	var viewers sync.WaitGroup
	go func() {
		for {
			connection, err := pipe.accept()
			if err != nil {
				// A client that left before it connected costs a pause, never
				// a spin.
				time.Sleep(50 * time.Millisecond)
				continue
			}
			viewers.Add(1)
			go func() {
				defer viewers.Done()
				serve(connection, record.Token, console, output, closing)
			}()
		}
	}()

	// The terminal's last output is read before Close, which ends whatever
	// the process left running and discards the output of a terminal that is
	// closed early.
	select {
	case <-outputEnded:
	case <-closing:
	}
	closeErr := console.Close()
	farewell := make(chan struct{})
	go func() {
		viewers.Wait()
		close(farewell)
	}()
	select {
	case <-farewell:
	case <-time.After(farewellTimeout):
	}
	return closeErr
}

// announce opens the host's pipe and records where to find it.
func announce(stateDir, id string, childPID int) (Record, *listener, error) {
	name, err := pipeName()
	if err != nil {
		return Record{}, nil, err
	}
	pipe, err := listen(name)
	if err != nil {
		return Record{}, nil, err
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return Record{}, nil, err
	}
	record := Record{ID: id, Pipe: name, Token: hex.EncodeToString(secret[:]), Version: Version, HostPID: os.Getpid(), ChildPID: childPID, Started: time.Now().UTC()}
	if err := writeRecord(stateDir, record); err != nil {
		return Record{}, nil, fmt.Errorf("host: record the host: %w", err)
	}
	return record, pipe, nil
}

// serve is one viewer's connection: the handshake, the history, then live
// output one way and input, resizes and a close request the other, until the
// terminal ends or the viewer leaves.
func serve(connection *os.File, token string, console *conpty.Console, output *history, closing chan<- struct{}) {
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(handshakeTimeout))
	greeting, err := readHello(connection)
	if err != nil {
		return
	}
	switch {
	case greeting.Version != Version:
		_ = writeHello(connection, hello{Version: Version, Error: fmt.Sprintf("this host speaks protocol %d, not %d", Version, greeting.Version)})
		return
	case subtle.ConstantTimeCompare([]byte(greeting.Token), []byte(token)) != 1:
		_ = writeHello(connection, hello{Version: Version, Error: "the token does not match this host"})
		return
	}
	_ = connection.SetReadDeadline(time.Time{})
	past, feed, detach := output.attach()
	defer detach()
	if writeHello(connection, hello{Version: Version}) != nil || writeFrame(connection, frameOutput, past) != nil {
		return
	}
	go func() {
		// A viewer that leaves or sends a broken frame is detached, which
		// ends the output loop below.
		defer detach()
		for {
			kind, payload, err := readFrame(connection)
			if err != nil {
				return
			}
			switch kind {
			case frameInput:
				_, _ = console.Write(payload)
			case frameResize:
				if len(payload) == 4 {
					_ = console.Resize(int(binary.BigEndian.Uint16(payload)), int(binary.BigEndian.Uint16(payload[2:])))
				}
			case frameClose:
				select {
				case closing <- struct{}{}:
				default:
				}
			}
		}
	}()
	for chunk := range feed {
		if writeFrame(connection, frameOutput, chunk) != nil {
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
