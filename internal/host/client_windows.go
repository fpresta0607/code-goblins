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
}

// Event is what the host sent: terminal output, or the terminal's end with
// its exit code.
type Event struct {
	Output []byte
	Exited bool
	Code   uint32
}

// Dial connects to the host record names and says hello. The first event it
// then reads is the terminal's history.
func Dial(record Record) (*Client, error) {
	return dial(record, Version, record.Token)
}

func dial(record Record, version int, token string) (*Client, error) {
	pipe, err := dialPipe(record.Pipe, record.HostPID)
	if err != nil {
		return nil, err
	}
	_ = pipe.SetDeadline(time.Now().Add(handshakeTimeout))
	if err := writeHello(pipe, hello{Version: version, Token: token}); err != nil {
		pipe.Close()
		return nil, fmt.Errorf("host: say hello: %w", err)
	}
	answer, err := readHello(pipe)
	if err != nil {
		pipe.Close()
		return nil, fmt.Errorf("host: no answer to the handshake: %w", err)
	}
	if answer.Error != "" {
		pipe.Close()
		return nil, fmt.Errorf("host: refused the connection: %s", answer.Error)
	}
	_ = pipe.SetDeadline(time.Time{})
	return &Client{pipe: pipe}, nil
}

// Next reads the next event.
func (c *Client) Next() (Event, error) {
	kind, payload, err := readFrame(c.pipe)
	if err != nil {
		return Event{}, err
	}
	switch kind {
	case frameOutput:
		return Event{Output: payload}, nil
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
	if cols < 0 || rows < 0 || cols > 0xFFFF || rows > 0xFFFF {
		return fmt.Errorf("host: size %dx%d does not fit a resize frame", cols, rows)
	}
	var size [4]byte
	binary.BigEndian.PutUint16(size[:2], uint16(cols))
	binary.BigEndian.PutUint16(size[2:], uint16(rows))
	return c.send(frameResize, size[:])
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
