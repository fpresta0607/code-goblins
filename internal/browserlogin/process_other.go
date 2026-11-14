//go:build !windows

package browserlogin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// errNotPorted is what starting or reaching a Chrome returns on this system:
// a login's Chrome runs in a Windows job object, and its stand-in here is
// still to be written.
var errNotPorted = fmt.Errorf("browserlogin: a login's Chrome is not ported to this system yet: %w", errors.ErrUnsupported)

// Process is a Chrome started on a login's profile, which this system starts
// none of yet.
type Process struct {
	PID     int
	Started time.Time
	URL     string
}

// Start refuses, since no login's Chrome starts on this system yet.
func Start(ctx context.Context, launch Launch) (Process, error) {
	return Process{}, errNotPorted
}

// ServeClient refuses the client, since no login's Chrome runs on this system
// yet.
func (process Process) ServeClient(w http.ResponseWriter, r *http.Request, hear func(Command) string) {
	http.Error(w, errNotPorted.Error(), http.StatusNotImplemented)
}

// End refuses, since no login's Chrome runs on this system yet.
func End(ctx context.Context, process Process) error {
	return errNotPorted
}

// Gone reports true: no login's Chrome runs on this system, so nothing of one
// is left.
func Gone(process Process) bool { return true }

// Running reports false, since no login's Chrome runs on this system yet.
func Running(process Process) bool { return false }

// Executable refuses: finding Google Chrome is not ported to this system yet.
func Executable() (string, error) {
	return "", fmt.Errorf("browserlogin: finding Google Chrome is not ported to this system yet: %w", errors.ErrUnsupported)
}

// OwnerOnly refuses every request: proving a client runs as this process's
// own user is not ported to this system yet.
func OwnerOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "browserlogin: proving a client's owner is not ported to this system yet", http.StatusForbidden)
	})
}

// Client is a DevTools connection to a login's Chrome, which this system has
// none of.
type Client struct{}

// Open refuses, since no login's Chrome runs on this system yet.
func (process Process) Open(ctx context.Context) (*Client, error) {
	return nil, errNotPorted
}

// Events is a channel that carries no event.
func (c *Client) Events() <-chan Event { return nil }

// Call refuses, since no login's Chrome runs on this system yet.
func (c *Client) Call(ctx context.Context, session, method string, params, result any) error {
	return errNotPorted
}

// Close does nothing, since Open connects to nothing.
func (c *Client) Close() {}
