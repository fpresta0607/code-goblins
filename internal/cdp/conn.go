// Package cdp speaks the Chrome DevTools Protocol to one target over its
// WebSocket: a command, its answer, and nothing else. Events are read past
// and dropped, so a caller asks for what it wants to know instead of
// listening for it.
package cdp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/coder/websocket"
)

// maxMessage bounds one message from the browser. An answer that carries a
// page's text or accessibility tree can run to megabytes.
const maxMessage = 32 << 20

// Conn is one DevTools connection, to the browser or to one of its pages.
// One command runs at a time.
type Conn struct {
	mu   sync.Mutex
	ws   *websocket.Conn
	next int64
}

// Error is the browser's refusal of a command.
type Error struct {
	Method  string
	Code    int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s (%d)", e.Method, e.Message, e.Code)
}

// Dial connects to a target's WebSocket debugger URL, which Chrome gives on
// /json/version for the browser and /json/list for each page.
func Dial(ctx context.Context, url string) (*Conn, error) {
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(maxMessage)
	return &Conn{ws: ws}, nil
}

// Call runs method with params and decodes its result into result, which may
// be nil. A refusal is an *Error.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	id := c.next
	request, err := json.Marshal(struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params,omitempty"`
	}{id, method, params})
	if err != nil {
		return err
	}
	if err := c.ws.Write(ctx, websocket.MessageText, request); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
		var answer struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &answer); err != nil {
			return fmt.Errorf("%s: the browser's answer is not JSON: %w", method, err)
		}
		if answer.ID != id {
			continue
		}
		if answer.Error != nil {
			return &Error{Method: method, Code: answer.Error.Code, Message: answer.Error.Message}
		}
		body := bytes.TrimSpace(answer.Result)
		if len(body) == 0 || body[0] != '{' {
			return fmt.Errorf("%s: the browser's reply has no result object", method)
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(answer.Result, result)
	}
}

// Close ends the connection at once, without the closing handshake, which a
// browser that just closed itself, as after Browser.close, never answers.
func (c *Conn) Close() {
	_ = c.ws.CloseNow()
}
