package host

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/conpty"
)

// ErrNoDelivery is a host that cannot confirm what it types: one started by an
// older cfo. Nothing was typed into its terminal.
var ErrNoDelivery = errors.New("host: this terminal's host was started by an older cfo and cannot confirm what it types; nothing was typed")

// deliveryTimeout bounds the wait for the host to write one part. A write
// into a terminal whose program has stopped reading can block once its input
// buffer is full, and then the part is unconfirmed, not lost.
const deliveryTimeout = 10 * time.Second

// Delivery types into a terminal and learns, part by part, that its host
// wrote each into the terminal's input.
type Delivery struct {
	client *Client
}

// DialDelivery connects to the host record names for a delivery. A host that
// cannot confirm writes is ErrNoDelivery, before anything is typed.
func DialDelivery(record Record) (*Delivery, error) {
	client, answer, err := dial(record, hello{Version: Version, Token: record.Token, Deliver: true})
	if err != nil {
		return nil, err
	}
	if !answer.Deliver {
		_ = client.Close()
		return nil, ErrNoDelivery
	}
	return &Delivery{client: client}, nil
}

// Write types p into the terminal and returns once the host wrote all of it
// into the terminal's input, or why it did not.
func (d *Delivery) Write(p []byte) error {
	if err := d.client.send(frameInput, p); err != nil {
		return fmt.Errorf("host: send the input: %w", err)
	}
	_ = d.client.pipe.SetReadDeadline(time.Now().Add(deliveryTimeout))
	kind, payload, err := readFrame(d.client.pipe)
	if err != nil {
		return fmt.Errorf("host: no confirmation that the terminal took the input: %w", err)
	}
	if kind != frameAck {
		return fmt.Errorf("host: answered the input with frame %q, not a confirmation", kind)
	}
	if len(payload) > 0 {
		return fmt.Errorf("host: the terminal's input refused it: %s", payload)
	}
	return nil
}

// Close ends the delivery and leaves the terminal running.
func (d *Delivery) Close() error {
	return d.client.Close()
}

// serveDelivery writes each input frame a delivery sends into the terminal
// and acknowledges it, with no history or output.
func serveDelivery(connection *os.File, console *conpty.Console) {
	if writeHello(connection, hello{Version: Version, Deliver: true}) != nil {
		return
	}
	for {
		kind, payload, err := readFrame(connection)
		if err != nil || kind != frameInput {
			return
		}
		var refusal []byte
		if _, err := console.Write(payload); err != nil {
			refusal = []byte(err.Error())
		}
		if writeFrame(connection, frameAck, refusal) != nil {
			return
		}
	}
}
