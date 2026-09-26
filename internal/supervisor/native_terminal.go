package supervisor

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// The sizes a view may give a terminal: a smaller one was measured while the
// view was hidden, and a larger one is no window.
const (
	minTerminalCols, minTerminalRows = 20, 5
	maxTerminalCols, maxTerminalRows = 1000, 500
)

// relayMessage bounds one output message to a view.
const relayMessage = 256 << 10

// nativeRelay is one view's side of a native terminal: the output read from
// the host and not yet sent, how much of what was sent the view has
// acknowledged, and a size to tell it. One goroutine writes to the view, and
// a size goes out before any output still pending, which the pseudo console's
// repaint at that size follows.
type nativeRelay struct {
	mu          sync.Mutex
	pending     []byte
	sent, acked int64
	size        []byte
	// history tells the view, before any output, how many of the bytes that
	// follow replay the terminal's history.
	history []byte
	// closing is how the view closes once everything before it is sent.
	closing *websocket.CloseError
	wake    chan struct{}
}

func (r *nativeRelay) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// announce tells every view of task's terminal but the sender's the size the
// terminal took.
func (h *HTTP) announce(task string, sender *nativeRelay, cols, rows int) {
	message := []byte(fmt.Sprintf(`{"type":"size","cols":%d,"rows":%d}`, cols, rows))
	h.mu.Lock()
	defer h.mu.Unlock()
	for relay := range h.relays[task] {
		if relay == sender {
			continue
		}
		relay.mu.Lock()
		relay.size = message
		relay.mu.Unlock()
		relay.signal()
	}
}

// nativeTerminal relays one view of a native task's terminal, or of the
// registered CFO's, over a WebSocket: the host's output goes out as binary messages, typing comes back
// as binary messages, and a resize or an acknowledgement as a JSON text
// message. The host's history comes first; a view repaints the screen by
// sending its size, since the pseudo console redraws its whole window on every
// resize, and every other view is told the size the terminal took. The view is sent
// at most terminalWindow bytes it has not acknowledged, and one that falls
// terminalBacklog bytes behind is closed so it reconnects, so the host never
// waits on a slow window and no byte is dropped from a view that stays. The
// view is bound to its terminal once, when it connects, by the host's own
// pipe, so a key costs no check and starts no process; what it is bound to is
// checked again on every tick. A resize under custody is ignored.
func (h *HTTP) nativeTerminal(w http.ResponseWriter, r *http.Request) {
	// A browser cannot send the board's token in a WebSocket header, so it
	// comes in the query.
	if r.Header.Get("Origin") != "http://"+h.Host || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(h.Service.Instance)) != 1 {
		apiError(w, 403, "Refresh the board before opening a terminal")
		return
	}
	view, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer view.CloseNow()
	// From here every refusal closes the view with its reason, which a
	// browser can read where it cannot read a refused upgrade's body.
	select {
	case h.nativeSlots <- struct{}{}:
		defer func() { <-h.nativeSlots }()
	default:
		_ = view.Close(websocket.StatusTryAgainLater, "Too many terminal views are open.")
		return
	}
	binding, err := h.Service.nativeBinding(r.URL.Query())
	if err != nil {
		_ = view.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}
	record, err := host.ReadRecord(h.Service.Store.Home.State, binding.id)
	if err != nil {
		_ = view.Close(websocket.StatusPolicyViolation, "No terminal is running for "+binding.name+".")
		return
	}
	terminal, err := host.Dial(record)
	if err != nil {
		_ = view.Close(websocket.StatusPolicyViolation, "The terminal of "+binding.name+" did not answer.")
		return
	}
	defer terminal.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// mu guards custody, which each tick refreshes.
	var mu sync.Mutex
	check, stop := context.WithTimeout(ctx, 8*time.Second)
	custody, err := binding.check(check)
	stop()
	if err != nil {
		_ = view.Close(websocket.StatusPolicyViolation, closeReason(err.Error()))
		return
	}

	relay := &nativeRelay{wake: make(chan struct{}, 1)}
	h.mu.Lock()
	if h.relays[binding.id] == nil {
		h.relays[binding.id] = map[*nativeRelay]struct{}{}
	}
	h.relays[binding.id][relay] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.relays[binding.id], relay)
		if len(h.relays[binding.id]) == 0 {
			delete(h.relays, binding.id)
		}
		h.mu.Unlock()
	}()

	go func() {
		defer relay.signal()
		// The host's first output is its history, even when it is empty.
		first := true
		for {
			event, err := terminal.Next()
			relay.mu.Lock()
			if first && err == nil && !event.Exited {
				relay.history = []byte(fmt.Sprintf(`{"type":"history","bytes":%d}`, len(event.Output)))
				first = false
			}
			switch {
			case err != nil:
				relay.closing = &websocket.CloseError{Code: websocket.StatusGoingAway, Reason: "The terminal's host stopped answering."}
			case event.Exited:
				relay.closing = &websocket.CloseError{Code: websocket.StatusNormalClosure, Reason: fmt.Sprintf("The terminal ended with exit code %d.", event.Code)}
			case len(relay.pending)+len(event.Output) > h.terminalBacklog:
				relay.pending = nil
				relay.closing = &websocket.CloseError{Code: websocket.StatusTryAgainLater, Reason: "The view fell behind the terminal's output."}
			default:
				relay.pending = append(relay.pending, event.Output...)
			}
			closing := relay.closing != nil
			relay.mu.Unlock()
			if closing {
				return
			}
			relay.signal()
		}
	}()
	go func() {
		defer cancel()
		for {
			relay.mu.Lock()
			history, size := relay.history, relay.size
			relay.history, relay.size = nil, nil
			var output []byte
			if unacknowledged := relay.sent - relay.acked; len(relay.pending) > 0 && unacknowledged < int64(h.terminalWindow) {
				n := min(len(relay.pending), relayMessage, h.terminalWindow-int(unacknowledged))
				output = append([]byte(nil), relay.pending[:n]...)
				relay.pending = relay.pending[n:]
				if len(relay.pending) == 0 {
					relay.pending = nil
				}
				relay.sent += int64(n)
			}
			closing := relay.closing
			if len(relay.pending) > 0 || output != nil || size != nil || history != nil {
				closing = nil
			}
			relay.mu.Unlock()
			for _, message := range []struct {
				kind websocket.MessageType
				data []byte
			}{{websocket.MessageText, history}, {websocket.MessageText, size}, {websocket.MessageBinary, output}} {
				if message.data == nil {
					continue
				}
				write, stop := context.WithTimeout(ctx, 5*time.Second)
				err := view.Write(write, message.kind, message.data)
				stop()
				if err != nil {
					return
				}
			}
			if closing != nil {
				_ = view.Close(closing.Code, closing.Reason)
				return
			}
			if history == nil && size == nil && output == nil {
				select {
				case <-relay.wake:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	go func() {
		tick := time.NewTicker(h.terminalTick)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-h.Service.done:
				_ = view.Close(websocket.StatusGoingAway, "The board is restarting.")
				return
			case <-tick.C:
				check, stop := context.WithTimeout(ctx, 8*time.Second)
				refused, err := binding.check(check)
				stop()
				if err != nil {
					_ = view.Close(websocket.StatusPolicyViolation, closeReason(err.Error()))
					return
				}
				mu.Lock()
				custody = refused
				mu.Unlock()
			}
		}
	}()

	view.SetReadLimit(1 << 20)
	for {
		kind, data, err := view.Read(ctx)
		if err != nil {
			return
		}
		if kind == websocket.MessageText {
			var control struct {
				Type  string `json:"type"`
				Cols  int    `json:"cols"`
				Rows  int    `json:"rows"`
				Bytes int64  `json:"bytes"`
			}
			if json.Unmarshal(data, &control) != nil || control.Type != "resize" && control.Type != "ack" {
				_ = view.Close(websocket.StatusUnsupportedData, "A text message must be a resize or an acknowledgement.")
				return
			}
			if control.Type == "ack" {
				relay.mu.Lock()
				relay.acked = max(relay.acked, min(control.Bytes, relay.sent))
				relay.mu.Unlock()
				relay.signal()
				continue
			}
			mu.Lock()
			refused := custody
			mu.Unlock()
			if refused != nil || control.Cols < minTerminalCols || control.Rows < minTerminalRows || control.Cols > maxTerminalCols || control.Rows > maxTerminalRows {
				continue
			}
			if terminal.Resize(control.Cols, control.Rows) != nil {
				_ = view.Close(websocket.StatusGoingAway, "The terminal's host stopped answering.")
				return
			}
			h.announce(binding.id, relay, control.Cols, control.Rows)
			continue
		}
		mu.Lock()
		refused := custody
		mu.Unlock()
		if refused != nil {
			_ = view.Close(websocket.StatusPolicyViolation, closeReason(refused.Error()))
			return
		}
		if terminal.Input(data) != nil {
			_ = view.Close(websocket.StatusGoingAway, "Input outcome is unknown. Inspect the terminal before typing again.")
			return
		}
	}
}

// nativeBinding is the terminal a native view shows: a task's, or the
// registered CFO's. check repeats what opening it proved: it returns an error
// once the view must close, and custody, the reason typing is held while a
// gate owns the task.
type nativeBinding struct {
	id, name string
	check    func(ctx context.Context) (custody, err error)
}

func (s *Service) nativeBinding(query url.Values) (nativeBinding, error) {
	// A view of the CFO names the terminal it expects the CFO in, so a CFO
	// that moved is opened again rather than shown in a view of another.
	if want := query.Get("cfo"); want != "" {
		id, live := NativeCFO(s.Store.Home.State)
		if !live {
			return nativeBinding{}, errors.New("The CFO does not run in a native terminal.")
		}
		if id != want {
			return nativeBinding{}, errors.New("The CFO runs in another terminal now. Open the CFO again.")
		}
		return nativeBinding{id: id, name: "the CFO", check: func(context.Context) (error, error) {
			if current, live := NativeCFO(s.Store.Home.State); !live || current != id {
				return nil, errors.New("The CFO no longer runs in this terminal. Open the CFO again.")
			}
			return nil, nil
		}}, nil
	}
	selection := terminalSelection{Task: query.Get("task"), Generation: query.Get("generation")}
	meta, err := s.nativeTask(selection)
	if err != nil {
		return nativeBinding{}, err
	}
	return nativeBinding{id: meta.ID, name: "this task", check: func(ctx context.Context) (error, error) {
		current, err := s.nativeTask(selection)
		if err != nil {
			return nil, err
		}
		return s.validateTerminalControl(ctx, current), nil
	}}, nil
}

// nativeTask is the task a view selected, while that generation is current
// and its terminal is native.
func (s *Service) nativeTask(selected terminalSelection) (state.TaskMeta, error) {
	if state.ValidTaskID(selected.Task) != nil || selected.Generation == "" {
		return state.TaskMeta{}, errors.New("Select a task's current session to open its terminal.")
	}
	meta, err := state.ReadTaskMeta(s.Store.Home.State, selected.Task)
	if err != nil || meta.SpawnGen != selected.Generation {
		return state.TaskMeta{}, errors.New("This task restarted or was replaced. Select its current session.")
	}
	if meta.Backend != "native" {
		return state.TaskMeta{}, errors.New("This task's terminal runs in Herdr, not natively.")
	}
	return meta, nil
}

// closeReason fits text into a WebSocket close frame, which carries at most
// 123 bytes of it.
func closeReason(text string) string {
	if len(text) <= 123 {
		return text
	}
	cut := 120
	for !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}
