package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// socketTimeout bounds one request when the caller's context sets no
// deadline of its own.
const socketTimeout = 5 * time.Second

// Socket is a Herdr session's socket API: the named pipe the session's server
// listens on. Herdr answers one request on a connection and then closes it
// (0.9.0-preview, protocol 22), so every request dials afresh. A request is a
// pipe round trip of about a millisecond, where the same request made through
// the herdr command costs a process start.
type Socket struct {
	pipe string
}

// Socket asks the session's Herdr where its server listens. Herdr's own status
// is the contract for that path, so it is read rather than derived.
func (c *Client) Socket(ctx context.Context) (Socket, error) {
	session := c.session()
	result, err := c.required(ctx, session, Target{}, "status", "status", "--json")
	if err != nil {
		return Socket{}, err
	}
	var status struct {
		Server struct {
			Running *bool  `json:"running"`
			Socket  string `json:"socket"`
		} `json:"server"`
	}
	if err := json.Unmarshal(result.Stdout, &status); err != nil {
		return Socket{}, fmt.Errorf("herdr: decode status response for session %q: %w", session, err)
	}
	if status.Server.Running == nil || !*status.Server.Running {
		return Socket{}, fmt.Errorf("herdr: server for session %q is not running", session)
	}
	if status.Server.Socket == "" {
		return Socket{}, fmt.Errorf("herdr: status for session %q names no socket", session)
	}
	return Socket{pipe: `\\.\pipe\` + status.Server.Socket}, nil
}

// Typist types literal text into the session's panes over its socket, one
// request per call and no process, for a view that types key by key.
func (c *Client) Typist(ctx context.Context) (func(context.Context, Target, string) error, error) {
	socket, err := c.Socket(ctx)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, target Target, text string) error {
		return socket.SendText(ctx, target.Pane, text)
	}, nil
}

// SendText types unsubmitted literal text into a pane, as pane send-text does.
func (s Socket) SendText(ctx context.Context, pane, text string) error {
	return s.request(ctx, "pane.send_text", map[string]string{"pane_id": pane, "text": text})
}

func (s Socket) request(ctx context.Context, method string, params any) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(socketTimeout)
	}
	conn, err := s.dial(ctx, deadline)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	request, err := json.Marshal(struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{method, method, params})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(request, '\n')); err != nil {
		return fmt.Errorf("herdr: %s: %w", method, err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("herdr: %s: no answer: %w", method, err)
	}
	var response struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		return fmt.Errorf("herdr: %s: decode answer: %w", method, err)
	}
	switch {
	case response.ID != method:
		return fmt.Errorf("herdr: %s: answer is for request %q", method, response.ID)
	case response.Error != nil:
		return fmt.Errorf("herdr: %s: %s: %s", method, response.Error.Code, response.Error.Message)
	case len(response.Result) == 0:
		return fmt.Errorf("herdr: %s: answer carries no result", method)
	}
	return nil
}

// dial opens a connection, waiting while every instance of the pipe is busy
// with another client.
func (s Socket) dial(ctx context.Context, deadline time.Time) (*os.File, error) {
	for {
		conn, err := os.OpenFile(s.pipe, os.O_RDWR|syscall.FILE_FLAG_OVERLAPPED, 0)
		if err == nil {
			return conn, nil
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) || time.Now().After(deadline) {
			return nil, fmt.Errorf("herdr: connect to %s: %w", s.pipe, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}
