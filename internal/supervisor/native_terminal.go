package supervisor

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/fpresta0607/code-goblins/internal/harness"
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

// screenSettle is how long a terminal's output pauses before its relay reads
// the screen, to tell its views whether the harness waits on an answer there,
// which it reads too as a view opens. A turn's spinner draws more often than
// this, so a turn costs no read, but
// while a view was last told the harness waits, the screen is read this long
// after the first output that follows, pause or not, so the view learns soon
// that the answer was given and a turn runs.
const screenSettle = 300 * time.Millisecond

// nativeRelay is one view's side of a native terminal: the output read from
// the host and not yet sent, the sizes the terminal took at their places in
// it, and how much of what was sent the view has acknowledged. One goroutine
// writes to the view, and each size goes out between the output before it and
// the output after it, as the host told it.
type nativeRelay struct {
	mu      sync.Mutex
	pending []byte
	// received counts every output byte read from the host, and sent and
	// acked the ones sent to the view and acknowledged by it.
	received, sent, acked int64
	sizes                 []relaySize
	// history tells the view, before any output, how many of the bytes that
	// follow replay the terminal's history.
	history []byte
	// unread tells the view whether key presses sit unread in the terminal's
	// input, while the view has not been told that yet.
	unread []byte
	// held tells the view whether typing is held, and why, while the view has
	// not been told that yet.
	held []byte
	// screen tells the view whether the harness waits on an answer at a
	// screen of its own, while the view has not been told that yet, asking is
	// what the view was last told, and looking says a read of the screen is
	// due.
	screen  []byte
	asking  *bool
	looking bool
	// closing is how the view closes once everything before it is sent.
	closing *websocket.CloseError
	wake    chan struct{}
}

// relaySize is a size to tell the view once the output before it is sent.
type relaySize struct {
	at      int64
	message []byte
}

// heldNotice tells a view whether typing is held, by custody, and why: a gate
// that owns the task, or a worktree on a detached HEAD. An empty reason says
// typing is no longer held.
func heldNotice(custody error) []byte {
	notice := struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	}{Type: "held"}
	if custody != nil {
		notice.Reason = custody.Error()
	}
	data, _ := json.Marshal(notice)
	return data
}

func (r *nativeRelay) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// size queues the terminal's size at the end of the output read so far. The
// caller holds r.mu.
func (r *nativeRelay) size(cols, rows int) {
	r.sizes = append(r.sizes, relaySize{at: r.received, message: []byte(fmt.Sprintf(`{"type":"size","cols":%d,"rows":%d}`, cols, rows))})
}

// announce tells every view of the terminal key names, the sender's too, the
// size the terminal took, for a host from before hosts told their viewers
// each resize themselves.
func (h *HTTP) announce(key string, cols, rows int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for relay := range h.relays[key] {
		relay.mu.Lock()
		relay.size(cols, rows)
		relay.mu.Unlock()
		relay.signal()
	}
}

// nativeTerminal relays one view of a native task's terminal, or of the
// registered CFO's, over a WebSocket: the host's output goes out as binary
// messages, typing comes back as binary messages, and a resize or an
// acknowledgement as a JSON text message. The host's history comes first; a view repaints the screen by
// sending its size, since the pseudo console redraws its whole window on every
// resize. Every view, the one that sent it too, is told each size the
// terminal took at its place in the output, whichever viewer resized it, so
// it draws each output at the size it was written for. A view is told as
// `{"type":"unread","unread":true}` when key presses have sat unread in the
// terminal's input, as a busy program leaves them, and with false once its
// program has read them. The view is sent
// at most terminalWindow bytes it has not acknowledged, and one that falls
// terminalBacklog bytes behind is closed so it reconnects, so the host never
// waits on a slow window and no byte is dropped from a view that stays. The
// view is bound to its terminal once, when it connects, by the host's own
// pipe, so a key costs no check and starts no process; what it is bound to is
// checked again on every tick. While custody holds typing, as a gate that
// owns the task does, the view stays open and live: it is told
// `{"type":"held","reason":"..."}` when it opens and whenever that changes,
// an empty reason once typing is free, and its typing and resizes are
// dropped. xterm sends input by itself, a focus report or a reply to a query
// the replayed history holds, so closing the view on input under custody
// closed every view of a task a gate owned as it opened (#1304). For a
// harness whose screens it knows, the relay reads the terminal's screen as
// the view opens, once its output pauses, and soon after output follows a
// screen that asked, and tells the view as
// `{"type":"screen","asking":true}` when the harness waits on an answer at a
// screen of its own, which only its terminal takes, as Claude Code's
// permission prompt, and with false once it no longer does.
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
		_ = view.Close(closeCode(err), err.Error())
		return
	}
	if binding.localOnly != "" && offMachine(r, h.Host) != "" {
		_ = view.Close(websocket.StatusPolicyViolation, binding.localOnly)
		return
	}
	// A goblin's session shows on the board as soon as its start or resume
	// writes it, a second or more before its terminal's host runs, so its
	// view waits for the host while the start or resume runs.
	var record host.Record
	for {
		isLaunching := binding.isLaunching != nil && binding.isLaunching()
		record, err = host.ReadRecord(binding.dir, binding.id)
		if err == nil || !isLaunching {
			break
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(launchWatchEvery):
		}
	}
	if err != nil {
		_ = view.Close(websocket.StatusNormalClosure, "No terminal is running for "+binding.name+".")
		return
	}
	// A host that did not answer may be starting or busy, so the view tries
	// again, as the board does for a view that may come back.
	terminal, err := host.Watch(record)
	if err != nil {
		_ = view.Close(websocket.StatusTryAgainLater, "The terminal of "+binding.name+" did not answer.")
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
		_ = view.Close(closeCode(err), closeReason(err.Error()))
		return
	}

	relay := &nativeRelay{wake: make(chan struct{}, 1), history: []byte(fmt.Sprintf(`{"type":"history","bytes":%d}`, terminal.History())), looking: true}
	if custody != nil {
		relay.held = heldNotice(custody)
	}
	look := time.AfterFunc(0, func() {
		relay.mu.Lock()
		relay.looking = false
		relay.mu.Unlock()
		if binding.harness == nil {
			return
		}
		screens, readable := harness.NativeScreens(harness.Kind(binding.harness()))
		if !readable {
			return
		}
		rows, err := host.ReadScreen(record)
		if err != nil {
			return
		}
		asking := screens.IsAsking(rows)
		relay.mu.Lock()
		if relay.asking == nil || *relay.asking != asking {
			relay.asking = &asking
			relay.screen = []byte(fmt.Sprintf(`{"type":"screen","asking":%t}`, asking))
		}
		relay.mu.Unlock()
		relay.signal()
	})
	defer look.Stop()
	h.mu.Lock()
	if h.relays[binding.key] == nil {
		h.relays[binding.key] = map[*nativeRelay]struct{}{}
	}
	h.relays[binding.key][relay] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.relays[binding.key], relay)
		if len(h.relays[binding.key]) == 0 {
			delete(h.relays, binding.key)
		}
		h.mu.Unlock()
	}()

	go func() {
		defer relay.signal()
		for {
			event, err := terminal.Next()
			relay.mu.Lock()
			switch {
			case err != nil:
				relay.closing = &websocket.CloseError{Code: websocket.StatusGoingAway, Reason: "The terminal's host stopped answering."}
			case event.Exited:
				relay.closing = &websocket.CloseError{Code: websocket.StatusNormalClosure, Reason: fmt.Sprintf("The terminal ended with exit code %d.", event.Code)}
			case event.Cols > 0:
				relay.size(event.Cols, event.Rows)
			case event.Unread != nil:
				relay.unread = []byte(fmt.Sprintf(`{"type":"unread","unread":%t}`, *event.Unread))
			case len(relay.pending)+len(event.Output) > h.terminalBacklog:
				relay.pending = nil
				relay.closing = &websocket.CloseError{Code: websocket.StatusTryAgainLater, Reason: "The view fell behind the terminal's output."}
			default:
				relay.pending = append(relay.pending, event.Output...)
				relay.received += int64(len(event.Output))
				if !relay.looking || relay.asking == nil || !*relay.asking {
					relay.looking = true
					look.Reset(screenSettle)
				}
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
			history, unread, held, screen := relay.history, relay.unread, relay.held, relay.screen
			relay.history, relay.unread, relay.held, relay.screen = nil, nil, nil, nil
			var size, output []byte
			if len(relay.sizes) > 0 && relay.sizes[0].at == relay.sent {
				size = relay.sizes[0].message
				relay.sizes = relay.sizes[1:]
			}
			if unacknowledged := relay.sent - relay.acked; size == nil && len(relay.pending) > 0 && unacknowledged < int64(h.terminalWindow) {
				n := min(len(relay.pending), relayMessage, h.terminalWindow-int(unacknowledged))
				// Output stops at the next size, which goes out before the rest.
				if len(relay.sizes) > 0 {
					n = min(n, int(relay.sizes[0].at-relay.sent))
				}
				output = append([]byte(nil), relay.pending[:n]...)
				relay.pending = relay.pending[n:]
				if len(relay.pending) == 0 {
					relay.pending = nil
				}
				relay.sent += int64(n)
			}
			closing := relay.closing
			if len(relay.pending) > 0 || output != nil || size != nil || history != nil || unread != nil || held != nil || screen != nil {
				closing = nil
			}
			relay.mu.Unlock()
			for _, message := range []struct {
				kind websocket.MessageType
				data []byte
			}{{websocket.MessageText, history}, {websocket.MessageText, held}, {websocket.MessageText, size}, {websocket.MessageBinary, output}, {websocket.MessageText, unread}, {websocket.MessageText, screen}} {
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
			if history == nil && size == nil && output == nil && unread == nil && held == nil && screen == nil {
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
					_ = view.Close(closeCode(err), closeReason(err.Error()))
					return
				}
				mu.Lock()
				isChanged := (custody == nil) != (refused == nil) || custody != nil && custody.Error() != refused.Error()
				custody = refused
				mu.Unlock()
				if isChanged {
					relay.mu.Lock()
					relay.held = heldNotice(refused)
					relay.mu.Unlock()
					relay.signal()
				}
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
			if !terminal.IsToldSizes() {
				h.announce(binding.key, control.Cols, control.Rows)
			}
			continue
		}
		mu.Lock()
		refused := custody
		mu.Unlock()
		if refused != nil {
			continue
		}
		if terminal.Input(data) != nil {
			_ = view.Close(websocket.StatusGoingAway, "Input outcome is unknown. Inspect the terminal before typing again.")
			return
		}
	}
}

// nativeBinding is the terminal a native view shows: a task's, the
// registered CFO's, or a run item's on its card. Its host records itself as
// id in the state directory dir, and key names it among every view's: every
// run item's terminal is recorded as run, in the item's own directory. check
// repeats what opening it proved: it returns an error once the view must
// close, and custody, the reason typing is held while a gate owns the task.
// localOnly, when set, is why a view opens only on the board's own page on
// this PC: a terminal where values are typed, or one that mends remote
// access, which only this PC does. isLaunching, when set, says the terminal's host may not run
// yet because its goblin starts or resumes now. harness, when set, returns the
// harness the terminal runs at the moment, whose screens the relay reads: for
// the CFO, the one it registered as. A run item's terminal has none.
type nativeBinding struct {
	id, name, dir, key string
	harness            func() string
	localOnly          string
	check              func(ctx context.Context) (custody, err error)
	isLaunching        func() bool
}

func (s *Service) nativeBinding(query url.Values) (nativeBinding, error) {
	// A view of a run item's terminal names the item, which shows its
	// terminal while its command runs.
	if id := query.Get("run"); id != "" {
		r, found := s.runOnCard(id, "")
		if !found {
			return nativeBinding{}, terminalEnded("This command is not running.")
		}
		localOnly := ""
		switch {
		case r.CredentialRequest != "":
			localOnly = "Values are typed only on the board's own page on this PC."
		case r.RemoteFix != "":
			localOnly = "Remote access is mended only on the board's own page on this PC."
		}
		return nativeBinding{id: runTerminal, name: "this command", dir: runDir(s.Store.Home.State, r), key: "run:" + id, localOnly: localOnly, check: func(context.Context) (error, error) {
			if _, found := s.runOnCard(id, r.RunAction); !found {
				return nil, terminalEnded("The command ended.")
			}
			return nil, nil
		}}, nil
	}
	// A view of the CFO names the terminal it expects the CFO in, so a CFO
	// that moved is opened again rather than shown in a view of another.
	if want := query.Get("cfo"); want != "" {
		id := readCFOState(s.Store.Home.State).terminal
		if id == "" {
			return nativeBinding{}, terminalEnded("The CFO does not run in a native terminal.")
		}
		if id != want {
			return nativeBinding{}, terminalEnded("The CFO runs in another terminal now. Open the CFO again.")
		}
		return nativeBinding{id: id, name: "the CFO", dir: s.Store.Home.State, key: id, harness: func() string {
			return readCFOState(s.Store.Home.State).harness
		}, check: func(context.Context) (error, error) {
			if readCFOState(s.Store.Home.State).terminal != id {
				return nil, terminalEnded("The CFO no longer runs in this terminal. Open the CFO again.")
			}
			return nil, nil
		}}, nil
	}
	selection := terminalSelection{Task: query.Get("task"), Generation: query.Get("generation")}
	meta, err := s.nativeTask(selection)
	if err != nil {
		return nativeBinding{}, err
	}
	return nativeBinding{id: meta.ID, name: "this task", dir: s.Store.Home.State, key: meta.ID, harness: func() string { return meta.Harness }, check: func(ctx context.Context) (error, error) {
		current, err := s.nativeTask(selection)
		if err != nil {
			return nil, err
		}
		return s.boardTerminalControl(ctx, current), nil
	}, isLaunching: func() bool {
		s.starts.Lock()
		defer s.starts.Unlock()
		return s.starting == meta.ID || s.changing[meta.ID] == "resume"
	}}, nil
}

// runOnCard is the run item id while its command runs in its terminal, and,
// when action is set, still under the Run that action pressed.
func (s *Service) runOnCard(id, action string) (Run, bool) {
	runs := s.Store.Snapshot().Runs
	i := slices.IndexFunc(runs, func(r Run) bool {
		return r.ID == id && r.State == "running" && r.Terminal && (action == "" || r.RunAction == action)
	})
	if i < 0 {
		return Run{}, false
	}
	return runs[i], true
}

// nativeTask is the task a view selected, while that generation is current
// and its terminal is native.
func (s *Service) nativeTask(selected terminalSelection) (state.TaskMeta, error) {
	if state.ValidTaskID(selected.Task) != nil || selected.Generation == "" {
		return state.TaskMeta{}, errors.New("Select a task's current session to open its terminal.")
	}
	meta, err := state.ReadTaskMeta(s.Store.Home.State, selected.Task)
	if err != nil || meta.SpawnGen != selected.Generation {
		return state.TaskMeta{}, terminalEnded("This task restarted or was replaced. Select its current session.")
	}
	if meta.Backend != "native" {
		return state.TaskMeta{}, errors.New("This task's terminal runs in Herdr, not natively.")
	}
	return meta, nil
}

// endedTerminal is why a view closes when the terminal it names no longer
// runs: its command or its task's session ended or was replaced, or the CFO
// left it. No reconnect brings such a terminal back, so the view closes as
// ended, a normal close, which the board shows without Reconnect, and a new
// session or terminal is one the board opens by itself.
type endedTerminal struct{ reason string }

func (ended endedTerminal) Error() string { return ended.reason }

func terminalEnded(reason string) error { return endedTerminal{reason} }

// closeCode is how a view closes for err: as ended for a terminal that no
// longer runs, and as refused for anything else, which Reconnect may get past.
func closeCode(err error) websocket.StatusCode {
	if errors.As(err, new(endedTerminal)) {
		return websocket.StatusNormalClosure
	}
	return websocket.StatusPolicyViolation
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
