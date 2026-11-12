//go:build !windows

package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// SocketCache would keep each session's Herdr socket, which this system does
// not reach: every read goes through the herdr command.
type SocketCache struct{}

// NewSocketCache returns an empty cache.
func NewSocketCache() *SocketCache { return &SocketCache{} }

// socketRead reports false, so the caller reads through the herdr command.
func (c *Client) socketRead(ctx context.Context, method string, params map[string]any) (json.RawMessage, bool) {
	return nil, false
}

// PaneInput types into a session's panes and reads their history with no
// process per call, for a view that types key by key.
type PaneInput interface {
	// SendText types unsubmitted literal text into a pane, and a complete
	// bracketed paste with the program's own paste mode.
	SendText(ctx context.Context, pane, text string) error
	// History returns up to lines of the pane's most recent output, history
	// included, with its colors.
	History(ctx context.Context, pane string, lines int) (string, error)
}

// PaneInput refuses: Herdr's socket is reached only on Windows.
func (c *Client) PaneInput(ctx context.Context) (PaneInput, error) {
	return nil, fmt.Errorf("herdr: the session's socket is not reached on this system: %w", errors.ErrUnsupported)
}
