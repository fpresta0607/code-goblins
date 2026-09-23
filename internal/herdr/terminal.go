package herdr

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TerminalFrame is Herdr's rendered native screen, not historical PTY output.
type TerminalFrame struct {
	Type     string `json:"type"`
	Seq      uint64 `json:"seq,omitempty"`
	Encoding string `json:"encoding,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	Full     bool   `json:"full,omitempty"`
	Bytes    string `json:"bytes,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type TerminalCommand struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
	Direction string `json:"direction,omitempty"`
	Lines     int    `json:"lines,omitempty"`
	Source    string `json:"source,omitempty"`
}

func (c TerminalCommand) Validate() error {
	switch c.Type {
	case "terminal.input":
		if len(c.Text) == 0 || len(c.Text) > 64<<10 || c.Cols != 0 || c.Rows != 0 || c.Direction != "" || c.Lines != 0 || c.Source != "" {
			return errors.New("invalid terminal input")
		}
	case "terminal.resize":
		if c.Cols < 20 || c.Cols > 400 || c.Rows < 5 || c.Rows > 160 || c.Text != "" || c.Direction != "" || c.Lines != 0 || c.Source != "" {
			return errors.New("invalid terminal dimensions")
		}
	case "terminal.scroll":
		if (c.Direction != "up" && c.Direction != "down") || c.Lines < 1 || c.Lines > 200 || (c.Source != "wheel" && c.Source != "page_key") || c.Text != "" || c.Cols != 0 || c.Rows != 0 {
			return errors.New("invalid terminal scroll")
		}
	default:
		return errors.New("unsupported terminal operation")
	}
	return nil
}

type TerminalStream interface {
	Next() (TerminalFrame, error)
	Send(TerminalCommand) error
	Close() error
}

type terminalProcess struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	cancel  context.CancelFunc
	once    sync.Once
}

// OpenTerminal never takes over an existing controller. The exact terminal ID
// prevents a removed pane alias from redirecting this stream into a new pane.
func OpenTerminal(ctx context.Context, session, terminal string, control bool, cols, rows int) (TerminalStream, error) {
	if session == "" || terminal == "" || strings.ContainsAny(session+terminal, "\x00\r\n") || strings.HasPrefix(terminal, "-") {
		return nil, errors.New("native terminal identity is required")
	}
	if err := (TerminalCommand{Type: "terminal.resize", Cols: cols, Rows: rows}).Validate(); err != nil {
		return nil, err
	}
	mode := "observe"
	if control {
		mode = "control"
	}
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, "herdr", "--session", session, "terminal", "session", mode, terminal, "--cols", strconv.Itoa(cols), "--rows", strconv.Itoa(rows))
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		return nil, err
	}
	// Errors can contain native terminal contents. Never persist CLI output.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		return nil, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	return &terminalProcess{cmd: cmd, stdin: stdin, scanner: scanner, cancel: cancel}, nil
}

func (p *terminalProcess) Next() (TerminalFrame, error) {
	if !p.scanner.Scan() {
		if err := p.scanner.Err(); err != nil {
			return TerminalFrame{}, err
		}
		return TerminalFrame{}, io.EOF
	}
	var f TerminalFrame
	if err := json.Unmarshal(p.scanner.Bytes(), &f); err != nil {
		return f, errors.New("invalid native terminal frame")
	}
	if f.Type == "terminal.closed" {
		return f, nil
	}
	if f.Type != "terminal.frame" || f.Encoding != "ansi" || f.Width < 1 || f.Width > 400 || f.Height < 1 || f.Height > 160 {
		return f, errors.New("unsupported native terminal frame")
	}
	if _, err := base64.StdEncoding.DecodeString(f.Bytes); err != nil {
		return f, errors.New("invalid native terminal bytes")
	}
	return f, nil
}

func (p *terminalProcess) Send(c TerminalCommand) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return json.NewEncoder(p.stdin).Encode(c)
}

func (p *terminalProcess) Close() error {
	p.once.Do(func() { _ = p.stdin.Close(); p.cancel(); _ = p.cmd.Wait() })
	return nil
}
