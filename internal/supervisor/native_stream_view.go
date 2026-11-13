package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/coder/websocket"

	"github.com/fpresta0607/code-goblins/internal/claudestream"
	"github.com/fpresta0607/code-goblins/internal/host"
)

// streamMarker tells a view it shows a session on stream-json pipes, which
// takes no typing but stops a turn on Escape or Ctrl+C.
var streamMarker = []byte(`{"type":"stream"}`)

// streamRelay is one view's side of a stream session: the rows the host's
// state last said it shows, none before a read succeeds, whether the view has
// claimed a size to draw them at, and the screen report the view was last
// told. mu orders what goes to the view.
type streamRelay struct {
	ctx     context.Context
	view    *websocket.Conn
	header  string
	mu      sync.Mutex
	rows    []string
	isSized bool
	told    []byte
}

// streamView relays one view of a goblin's stream session, which runs on
// stream-json pipes with no terminal. The view is told so first, then that
// no history replays and that typing is held, since the board sends its
// messages as lines; the composer then saves through the board. As the
// session writes lines, its host's state, read soon after, is drawn once the
// view claims a size, and told as the view's screen: working while a turn
// runs, what it writes or thinks, and asking while a tool call waits for an
// answer. Escape or Ctrl+C interrupts the turn; other typing is dropped. The
// view closes when Claude Code ends, and when what it was opened for is gone.
func (h *HTTP) streamView(ctx context.Context, view *websocket.Conn, binding nativeBinding, record host.Record) {
	viewer, err := host.WatchStream(record)
	if err != nil {
		_ = view.Close(websocket.StatusTryAgainLater, "The stream session of "+binding.name+" did not answer.")
		return
	}
	defer viewer.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	subject := strings.ToUpper(binding.name[:1]) + binding.name[1:]
	relay := &streamRelay{ctx: ctx, view: view, header: subject + " runs Claude Code on stream-json pipes. goblins switch " + binding.id + " --terminal takes it over in a terminal."}
	held := heldNotice(errors.New(subject + " runs on stream-json pipes, so the board sends its messages"))
	for _, message := range [][]byte{streamMarker, []byte(`{"type":"history","bytes":0}`), held} {
		if !relay.write(websocket.MessageText, message) {
			return
		}
	}
	relay.refresh(record)
	// A read of the state is due soon after a line, and further lines do not
	// postpone it.
	var looking atomic.Bool
	go func() {
		defer cancel()
		for {
			event, err := viewer.Next()
			switch {
			case err != nil:
				_ = view.Close(websocket.StatusGoingAway, "The stream session's host stopped answering.")
				return
			case event.IsExited:
				_ = view.Close(websocket.StatusNormalClosure, fmt.Sprintf("Claude Code ended with exit code %d.", event.Code))
				return
			}
			if looking.CompareAndSwap(false, true) {
				time.AfterFunc(screenSettle, func() {
					looking.Store(false)
					relay.refresh(record)
				})
			}
		}
	}()
	go func() {
		tick := time.NewTicker(h.terminalTick)
		defer tick.Stop()
		for {
			check, stop := context.WithTimeout(ctx, custodyProbeBudget)
			_, revoked := binding.check(check)
			stop()
			if revoked != nil {
				_ = view.Close(closeCode(revoked), closeReason(revoked.Error()))
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-h.Service.done:
				_ = view.Close(websocket.StatusGoingAway, "The board is restarting.")
				return
			case <-tick.C:
			}
		}
	}()
	view.SetReadLimit(1 << 20)
	for {
		kind, data, err := view.Read(ctx)
		if err != nil {
			return
		}
		if kind == websocket.MessageBinary {
			if key := string(data); key != "\x1b" && key != "\x03" {
				continue
			}
			if err := interruptStream(record); err != nil {
				_ = view.Close(websocket.StatusGoingAway, "The stream session did not take the interrupt. Inspect it before stopping it again.")
				return
			}
			continue
		}
		var control struct {
			Type string `json:"type"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}
		if json.Unmarshal(data, &control) != nil || control.Type != "resize" && control.Type != "ack" && control.Type != "submit" {
			_ = view.Close(websocket.StatusUnsupportedData, "A text message must be a resize, an acknowledgement or a submission.")
			return
		}
		if control.Type != "resize" || control.Cols < minTerminalCols || control.Rows < minTerminalRows || control.Cols > maxTerminalCols || control.Rows > maxTerminalRows {
			continue
		}
		relay.resize(control.Cols, control.Rows)
	}
}

// write sends one message, and reports whether the view took it.
func (r *streamRelay) write(kind websocket.MessageType, data []byte) bool {
	write, stop := context.WithTimeout(r.ctx, 5*time.Second)
	defer stop()
	return r.view.Write(write, kind, data) == nil
}

// refresh reads the session's state from its host, draws its rows on a view
// that claimed a size, and tells the view its screen when that changed. A
// read that fails leaves the view as it was until the next line.
func (r *streamRelay) refresh(record host.Record) {
	if r.ctx.Err() != nil {
		return
	}
	session, err := host.ReadStreamState(record)
	if err != nil {
		return
	}
	screen := streamScreen(session)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = session.Rows()
	if r.isSized && !r.write(websocket.MessageBinary, streamDrawing(r.header, r.rows)) {
		return
	}
	if string(screen) != string(r.told) && r.write(websocket.MessageText, screen) {
		r.told = screen
	}
}

// resize tells the view the size it claimed, as a terminal that took it, and
// draws the session after it.
func (r *streamRelay) resize(cols, rows int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.write(websocket.MessageText, []byte(fmt.Sprintf(`{"type":"size","cols":%d,"rows":%d}`, cols, rows))) && r.write(websocket.MessageBinary, streamDrawing(r.header, r.rows)) {
		r.isSized = true
	}
}

// streamScreen is what a view is told of a stream session's screen: whether a
// tool call waits for an answer, whether a turn runs, and what its reply's
// newest block does, writing or thinking.
func streamScreen(session claudestream.Session) []byte {
	state := screenState{Asking: len(session.Asks) > 0, Working: session.IsWorking}
	if session.IsWorking && len(session.Live) > 0 {
		state.Doing = map[string]string{"text": "Writing", "thinking": "Thinking"}[session.Live[len(session.Live)-1].Kind]
	}
	message, _ := json.Marshal(struct {
		Type string `json:"type"`
		screenState
	}{"screen", state})
	return message
}

// streamDrawing is a stream session's rows drawn as a terminal draws its
// harness, over what was drawn before and under header, with every control
// character Claude Code's words hold left out.
func streamDrawing(header string, rows []string) []byte {
	var drawn strings.Builder
	drawn.WriteString("\x1b[H\x1b[2J\x1b[3J" + header + "\r\n")
	for _, row := range rows {
		drawn.WriteString("\r\n" + strings.Map(func(character rune) rune {
			if unicode.IsControl(character) && character != '\t' {
				return -1
			}
			return character
		}, row))
	}
	return []byte(drawn.String())
}

// interruptStream stops the turn of the stream session record names, as
// Escape in its terminal would.
func interruptStream(record host.Record) error {
	writer, err := host.DialStreamWriter(record)
	if err != nil {
		return err
	}
	defer writer.Close()
	return writer.Write(claudestream.RequestLine(rand.Text(), "interrupt", nil))
}
