package host

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// Client is one viewer of a host's terminal.
type Client struct {
	pipe *os.File
	// sending keeps one frame whole when input, a resize and a close request
	// are sent at once.
	sending sync.Mutex
	// history is how many output bytes, from the first, replay the
	// terminal's history, for a client View opened.
	history int
	// isToldSizes is whether the host tells this client the terminal's sizes.
	isToldSizes bool
	// first is the history View read from a host that tells no sizes, for
	// Next to return first.
	first *Event
}

// Event is what the host sent: terminal output, the terminal's size from
// this point in its output on, or the terminal's end with its exit code.
type Event struct {
	Output []byte
	// Cols and Rows are set for a size.
	Cols, Rows int
	Exited     bool
	Code       uint32
}

// Dial connects to the host record names and says hello. The first event it
// then reads is the terminal's history.
func Dial(record Record) (*Client, error) {
	client, _, err := dial(record, hello{Version: Version, Token: record.Token})
	return client, err
}

// View connects to the host record names as a viewer that is told the
// terminal's size in order with its output: the size its history starts at,
// then each resize at its place, whichever viewer made it. History says how
// many output bytes replay the history. A host from before sizes were told
// sends none, and its history is its first output, which View reads now to
// count it.
func View(record Record) (*Client, error) {
	client, answer, err := dial(record, hello{Version: Version, Token: record.Token, Sizes: true})
	if err != nil {
		return nil, err
	}
	if answer.Sizes {
		client.history, client.isToldSizes = answer.History, true
		return client, nil
	}
	first, err := client.Next()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("host: read the history: %w", err)
	}
	client.history, client.first = len(first.Output), &first
	return client, nil
}

// History is how many output bytes, from the first, replay the terminal's
// history, for a client View opened.
func (c *Client) History() int {
	return c.history
}

// IsToldSizes is whether the host tells the client the terminal's sizes.
func (c *Client) IsToldSizes() bool {
	return c.isToldSizes
}

// dial connects and says hello, returning the host's answer with the client.
func dial(record Record, greeting hello) (*Client, hello, error) {
	pipe, err := dialPipe(record.Pipe, record.HostPID)
	if err != nil {
		return nil, hello{}, err
	}
	_ = pipe.SetDeadline(time.Now().Add(handshakeTimeout))
	if err := writeHello(pipe, greeting); err != nil {
		pipe.Close()
		return nil, hello{}, fmt.Errorf("host: say hello: %w", err)
	}
	answer, err := readHello(pipe)
	if err != nil {
		pipe.Close()
		return nil, hello{}, fmt.Errorf("host: no answer to the handshake: %w", err)
	}
	if answer.Error != "" {
		pipe.Close()
		return nil, hello{}, fmt.Errorf("host: refused the connection: %s", answer.Error)
	}
	_ = pipe.SetDeadline(time.Time{})
	return &Client{pipe: pipe}, answer, nil
}

// Next reads the next event.
func (c *Client) Next() (Event, error) {
	if first := c.first; first != nil {
		c.first = nil
		return *first, nil
	}
	kind, payload, err := readFrame(c.pipe)
	if err != nil {
		return Event{}, err
	}
	switch kind {
	case frameOutput:
		return Event{Output: payload}, nil
	case frameSize:
		cols, rows, ok := parseSize(payload)
		if !ok {
			return Event{}, errors.New("host: malformed size frame")
		}
		return Event{Cols: cols, Rows: rows}, nil
	case frameExit:
		if len(payload) != 4 {
			return Event{}, errors.New("host: malformed exit frame")
		}
		return Event{Exited: true, Code: binary.BigEndian.Uint32(payload)}, nil
	default:
		return Event{}, fmt.Errorf("host: unexpected frame %q", kind)
	}
}

// Input types p into the terminal.
func (c *Client) Input(p []byte) error {
	return c.send(frameInput, p)
}

// Resize asks the host to resize the terminal to cols by rows cells.
func (c *Client) Resize(cols, rows int) error {
	payload, err := sizePayload(cols, rows)
	if err != nil {
		return err
	}
	return c.send(frameResize, payload)
}

// CloseTerminal asks the host to end the terminal and everything in it.
func (c *Client) CloseTerminal() error {
	return c.send(frameClose, nil)
}

// Close leaves the terminal running and disconnects this viewer.
func (c *Client) Close() error {
	return c.pipe.Close()
}

func (c *Client) send(kind byte, payload []byte) error {
	c.sending.Lock()
	defer c.sending.Unlock()
	return writeFrame(c.pipe, kind, payload)
}
