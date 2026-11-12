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

// PaneInput refuses: Herdr's socket is reached only on Windows.
func (c *Client) PaneInput(ctx context.Context) (PaneInput, error) {
	return nil, fmt.Errorf("herdr: the session's socket is not reached on this system: %w", errors.ErrUnsupported)
}
