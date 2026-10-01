package herdr

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
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

// MaxPasteBytes leaves room for the envelope in Herdr's 1 MiB socket request.
// The control stream permits 2 MiB, so this bound fits both routes.
const MaxPasteBytes = (1 << 20) - 1024

func pasteText(text string) (string, bool) {
	inside, isPaste := strings.CutPrefix(text, "\x1b[200~")
	if !isPaste {
		return "", false
	}
	return strings.CutSuffix(inside, "\x1b[201~")
}

var pasteEscapes = regexp.MustCompile(`\x1b(?:\[20[01]~)?`)

// CleanPaste reframes a complete paste without any escape inside it, so no
// pasted text can end the paste early and be typed as keys.
func CleanPaste(paste string) string {
	inside, _ := pasteText(paste)
	return "\x1b[200~" + pasteEscapes.ReplaceAllString(inside, "") + "\x1b[201~"
}

func (c TerminalCommand) Validate() error {
	switch c.Type {
	case "terminal.input":
		limit := 64 << 10
		if _, isPaste := pasteText(c.Text); isPaste {
			limit = MaxPasteBytes
			encoded, err := json.Marshal(c.Text)
			if err != nil || len(encoded) > limit {
				return errors.New("paste exceeds Herdr's encoded request limit")
			}
		}
		if len(c.Text) == 0 || len(c.Text) > limit || c.Cols != 0 || c.Rows != 0 || c.Direction != "" || c.Lines != 0 || c.Source != "" {
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

// OpenTerminal attaches to a terminal as terminalSessionArgs describes. The
// exact terminal ID prevents a removed pane alias from redirecting this stream into a new pane.
func OpenTerminal(ctx context.Context, session, terminal string, control bool, cols, rows int) (TerminalStream, error) {
	if session == "" || terminal == "" || strings.ContainsAny(session+terminal, "\x00\r\n") || strings.HasPrefix(terminal, "-") {
		return nil, errors.New("native terminal identity is required")
	}
	if err := (TerminalCommand{Type: "terminal.resize", Cols: cols, Rows: rows}).Validate(); err != nil {
		return nil, err
	}
	return startTerminal(ctx, "herdr", terminalSessionArgs(session, terminal, control, cols, rows)...)
}

// closeGrace is how long a closed terminal process has to deliver what it was
// sent and exit on its own before it is killed.
const closeGrace = time.Second

// startTerminal runs a terminal session process that reads commands on its
// stdin and writes frames on its stdout.
func startTerminal(ctx context.Context, name string, args ...string) (*terminalProcess, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := execx.CommandContext(ctx, name, args...)
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

// terminalSessionArgs is the Herdr command that attaches to a terminal. An
// observer sees the pane at the size it asks for and changes nothing; a
// controller sizes the pane to cols and rows. The most recent client to take
// control wins, so a controller takes over from any other: Herdr then ends the
// other one, and a Herdr window showing the pane takes its size back once the
// controller leaves. Attaching sends the program nothing.
func terminalSessionArgs(session, terminal string, control bool, cols, rows int) []string {
	mode := "observe"
	if control {
		mode = "control"
	}
	args := []string{"--session", session, "terminal", "session", mode, terminal, "--cols", strconv.Itoa(cols), "--rows", strconv.Itoa(rows)}
	if control {
		args = append(args, "--takeover")
	}
	return args
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

// Close ends the process's input and lets it deliver what it was last sent,
// such as the size a controller hands back, before it is killed.
func (p *terminalProcess) Close() error {
	p.once.Do(func() {
		_ = p.stdin.Close()
		exited := make(chan struct{})
		go func() { _ = p.cmd.Wait(); close(exited) }()
		select {
		case <-exited:
		case <-time.After(closeGrace):
		}
		p.cancel()
		<-exited
	})
	return nil
}
