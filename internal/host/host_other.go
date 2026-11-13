//go:build !windows

package host

import (
	"errors"
	"fmt"
	"time"
)

// errNotPorted is what every host call returns on this system: a native
// terminal runs in a Windows pseudo console, and the terminal for other
// systems is still to be written.
var errNotPorted = fmt.Errorf("host: native terminals are not ported to this system yet: %w", errors.ErrUnsupported)

// RunArgs refuses, since no host runs on this system yet.
func RunArgs(args []string) error { return errNotPorted }

// Launch refuses, since no host runs on this system yet.
func Launch(stateDir string, command, env []string, spec Spec) (Record, error) {
	return Record{}, errNotPorted
}

// Close refuses, since no host runs on this system yet.
func Close(stateDir string, launched Record, answerWait time.Duration) error {
	return errNotPorted
}

// Running reports no host running, since none runs on this system yet.
func Running(record Record) bool { return false }

// ReadScreen refuses, since no host runs on this system yet.
func ReadScreen(record Record) ([]string, error) { return nil, errNotPorted }

// Client is one viewer of a host's terminal, which this system has none of.
type Client struct{}

// Dial refuses, since no host runs on this system yet.
func Dial(record Record) (*Client, error) { return nil, errNotPorted }

// View refuses, since no host runs on this system yet.
func View(record Record) (*Client, error) { return nil, errNotPorted }

// Watch refuses, since no host runs on this system yet.
func Watch(record Record) (*Client, error) { return nil, errNotPorted }

// History is how many output bytes replay the terminal's history: none.
func (c *Client) History() int { return 0 }

// IsToldSizes is whether the host tells this client the terminal's sizes.
func (c *Client) IsToldSizes() bool { return false }

// Next refuses, since no host runs on this system yet.
func (c *Client) Next() (Event, error) { return Event{}, errNotPorted }

// Input refuses, since no host runs on this system yet.
func (c *Client) Input(p []byte) error { return errNotPorted }

// MarkHis refuses, since no host runs on this system yet.
func (c *Client) MarkHis() error { return errNotPorted }

// MarkSubmission refuses, since no host runs on this system yet.
func (c *Client) MarkSubmission(id, harness string) error { return errNotPorted }

// Resize refuses, since no host runs on this system yet.
func (c *Client) Resize(cols, rows int) error { return errNotPorted }

// CloseTerminal refuses, since no host runs on this system yet.
func (c *Client) CloseTerminal() error { return errNotPorted }

// Close does nothing, since Dial connects to nothing.
func (c *Client) Close() error { return nil }

// Screen refuses, since no host runs on this system yet.
func (c *Client) Screen(record Record, submission string) (rows []string, observed string, hasStarted bool, err error) {
	return nil, "", false, errNotPorted
}

// Delivery types into a terminal, which this system has none of.
type Delivery struct{}

// DialDelivery refuses, since no host runs on this system yet.
func DialDelivery(record Record) (*Delivery, error) { return nil, errNotPorted }

// Write refuses, since no host runs on this system yet.
func (d *Delivery) Write(p []byte) error { return errNotPorted }

// MarkHis refuses, since no host runs on this system yet.
func (d *Delivery) MarkHis() error { return errNotPorted }

// Close does nothing, since DialDelivery connects to nothing.
func (d *Delivery) Close() error { return nil }
