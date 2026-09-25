// Package host runs one goblin terminal in a process of its own, so the
// terminal outlives the supervisor, every window and whatever started it, and
// serves it over a named pipe only this Windows user can open.
package host

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Version is the host protocol. A client and a host that speak different
// versions refuse each other at the handshake.
const Version = 1

// A frame is its kind, its payload's length as four big-endian bytes, and the
// payload.
const (
	// frameHello opens a connection: the client's version and token, then
	// the host's version or its refusal.
	frameHello byte = 'h'
	// frameOutput carries terminal output to a client. The first one after
	// the handshake is the history, even when it is empty, so the client
	// knows where the replay ends and the live output begins.
	frameOutput byte = 'o'
	// frameInput carries typed bytes to the terminal.
	frameInput byte = 'i'
	// frameResize carries the terminal's new width and height in cells, two
	// big-endian bytes each.
	frameResize byte = 'r'
	// frameClose asks the host to end the terminal and everything in it.
	frameClose byte = 'c'
	// frameExit tells a client the terminal ended, with its exit code as four
	// big-endian bytes; the host closes the connection after it.
	frameExit byte = 'x'
	// frameScreen answers a screen request with the terminal's screen, as
	// JSON; the host closes the connection after it.
	frameScreen byte = 's'
)

// maxFrame bounds one frame's payload.
const maxFrame = 8 << 20

// hello is the handshake frame's payload both ways.
type hello struct {
	Version int    `json:"version"`
	Token   string `json:"token,omitempty"`
	// Screen, from a client, asks for the terminal's screen alone, with no
	// history, output or input. It needs no new version: a host that does
	// not know it answers as to a viewer, and ReadScreen refuses the output
	// frame that comes first.
	Screen bool   `json:"screen,omitempty"`
	Error  string `json:"error,omitempty"`
}

func writeFrame(w io.Writer, kind byte, payload []byte) error {
	if len(payload) > maxFrame {
		return fmt.Errorf("host: a %d-byte frame is over the %d-byte limit", len(payload), maxFrame)
	}
	frame := make([]byte, 5+len(payload))
	frame[0] = kind
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)
	_, err := w.Write(frame)
	return err
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	size := binary.BigEndian.Uint32(header[1:])
	if size > maxFrame {
		return 0, nil, fmt.Errorf("host: a %d-byte frame is over the %d-byte limit", size, maxFrame)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return header[0], payload, nil
}

func writeHello(w io.Writer, message hello) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return writeFrame(w, frameHello, payload)
}

func readHello(r io.Reader) (hello, error) {
	kind, payload, err := readFrame(r)
	if err != nil {
		return hello{}, err
	}
	if kind != frameHello {
		return hello{}, errors.New("host: the connection did not open with a handshake")
	}
	var message hello
	if err := json.Unmarshal(payload, &message); err != nil {
		return hello{}, fmt.Errorf("host: unreadable handshake: %w", err)
	}
	return message, nil
}
