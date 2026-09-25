package supervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type terminalSelection struct {
	Task       string `json:"task"`
	Session    string `json:"session"`
	Generation string `json:"generation"`
}

type unavailableTerminal string

func (e unavailableTerminal) Error() string { return string(e) }

type terminalBinding struct {
	Selection terminalSelection
	Target    herdr.Target
	Terminal  string
	Identity  string
	Process   lock.Info
}

func (s *Service) resolveTerminal(ctx context.Context, selected terminalSelection, write bool) (terminalBinding, error) {
	var b terminalBinding
	c := s.Options.CFO
	if c == nil || c.Terminals == nil {
		return b, errors.New("Native terminal transport is unavailable.")
	}
	b.Selection = selected
	if selected.Task == "" && selected.Session == "" {
		file, err := openPrimary(filepath.Join(c.State, "primary.json"))
		if err != nil {
			return b, errNotRegistered
		}
		defer file.Close()
		p, identity, err := decodePrimary(file)
		if err != nil {
			return b, err
		}
		if p.Host != "" {
			return b, unavailableTerminal("The CFO runs in a native terminal, which this view cannot show yet.")
		}
		if err := c.verify(ctx, p); err != nil {
			return b, err
		}
		b.Target, b.Terminal, b.Process, b.Identity = p.Target, p.Terminal, p.Process, identity
		return b, nil
	}
	if state.ValidTaskID(selected.Task) != nil || selected.Generation == "" {
		return b, unavailableTerminal("This session has no separately reported native terminal.")
	}
	meta, err := state.ReadTaskMeta(s.Store.Home.State, selected.Task)
	if err != nil || meta.SpawnGen != selected.Generation {
		return b, errors.New("This task restarted or was replaced. Select its current session.")
	}
	if selected.Session != "" && s.Store.Snapshot().TaskSessions[meta.ID] != selected.Session {
		return b, unavailableTerminal("This child has no separate terminal. Its owning task has its own terminal.")
	}
	if write {
		if err := s.validateTerminalControl(ctx, meta); err != nil {
			return b, err
		}
	}
	if meta.HerdrSession == "" || meta.HerdrPaneID == "" || meta.HerdrTabID == "" || meta.HerdrWorkspaceID == "" {
		return b, unavailableTerminal("No native terminal is registered for this task.")
	}
	client := c.Terminals(meta.HerdrSession)
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		return b, errors.New("Herdr is unavailable. Reconnect when its session is running.")
	}
	for _, pane := range snapshot.Panes {
		if pane.ID == meta.HerdrPaneID && pane.TabID == meta.HerdrTabID && pane.WorkspaceID == meta.HerdrWorkspaceID {
			b.Terminal = pane.TerminalID
		}
	}
	found := false
	for _, agent := range snapshot.Agents {
		if agent.PaneID == meta.HerdrPaneID && agent.TabID == meta.HerdrTabID && agent.WorkspaceID == meta.HerdrWorkspaceID && agent.Agent == meta.Harness && fsx.SamePath(agent.Cwd, meta.Worktree) {
			found = true
		}
	}
	if !found || b.Terminal == "" {
		return b, errors.New("This native session is closed or its agent registration changed.")
	}
	b.Target = herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}
	p, err := client.PaneProcessInfo(ctx, b.Target)
	if err != nil || p.ForegroundProcessGroupID <= 0 || p.ForegroundProcessGroupID == p.ShellPID {
		return b, errors.New("This agent has exited. No input will be sent to its shell.")
	}
	entries, err := proc.Ancestry(p.ForegroundProcessGroupID, 1)
	if err != nil || len(entries) != 1 {
		return b, errors.New("Native process identity is unavailable.")
	}
	hostname, err := os.Hostname()
	if err != nil {
		return b, err
	}
	b.Process = lock.Info{PID: entries[0].PID, Start: entries[0].Start, Hostname: hostname}
	// Delivery/PR/policy metadata is not recipient identity. Custody is checked
	// independently when a view opens and on every tick, even when this stable
	// binding is unchanged.
	data, err := json.Marshal(struct {
		Task, Generation, Worktree, Harness, Session, Workspace, Tab, Pane, Terminal string
		Process                                                                      lock.Info
	}{meta.ID, meta.SpawnGen, meta.Worktree, meta.Harness, meta.HerdrSession, meta.HerdrWorkspaceID, meta.HerdrTabID, meta.HerdrPaneID, b.Terminal, b.Process})
	if err != nil {
		return b, err
	}
	sum := sha256.Sum256(data)
	b.Identity = hex.EncodeToString(sum[:])
	return b, nil
}

type terminalLease struct {
	mu      sync.Mutex
	binding terminalBinding
	stream  herdr.TerminalStream
	control bool
	seq     uint64
	closed  bool
	cancel  context.CancelFunc
	// typist types into a pane the way cfo send does, for a lease that only
	// observes.
	typist func(context.Context, herdr.Target, string) error
	// custody is whether a no-mistakes gate lets the Overlord type into the
	// pane, checked when the view opens and on every tick rather than per key;
	// nil lets input through.
	custody error
}

// typedPiece is the most runes one pane send-text carries: a Windows command
// line holds 32767 UTF-16 units, and escaping can double an argument.
const typedPiece = 4096

// A lease exists only while its one output stream is open. Sequence numbers
// are consumed before a write, never persisted with secret-bearing key data.
// Restart/reconnect invalidates the lease instead of replaying any input.
func (l *terminalLease) input(ctx context.Context, seq uint64, command herdr.TerminalCommand, verify func(context.Context, terminalBinding, bool) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return errors.New("Terminal is disconnected. Reconnect for a fresh screen; input was not replayed.")
	}
	if !l.control && command.Type != "terminal.input" {
		return errors.New("A live pane view keeps the pane's own size and screen, so it sends only typing.")
	}
	if !l.control && strings.ContainsRune(command.Text, 0) {
		return errors.New("A NUL key such as Ctrl+Space cannot be typed into a live pane view. Nothing was sent.")
	}
	if seq != l.seq+1 {
		return errors.New("Terminal input is out of order or already submitted. It will not be replayed.")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	l.seq = seq
	if l.custody != nil {
		l.closed = true
		l.cancel()
		return l.custody
	}
	if err := verify(ctx, l.binding, true); err != nil {
		l.closed = true
		l.cancel()
		return err
	}
	if !l.control {
		// Typing goes into the verified pane itself, never through the
		// observer, in order and in pieces a command line can carry.
		runes := []rune(command.Text)
		for start := 0; start < len(runes); start += typedPiece {
			if err := l.typist(ctx, l.binding.Target, string(runes[start:min(start+typedPiece, len(runes))])); err != nil {
				l.closed = true
				l.cancel()
				return errors.New("Input outcome is unknown. Inspect the native screen before typing again; input was not retried.")
			}
		}
		return nil
	}
	stop := context.AfterFunc(ctx, func() { l.cancel(); _ = l.stream.Close() })
	err := l.stream.Send(command)
	stop()
	if err != nil || ctx.Err() != nil {
		l.closed = true
		l.cancel()
		return errors.New("Input outcome is unknown. Inspect the native screen before reconnecting; input was not retried.")
	}
	return nil
}

// stillBound is the check each input makes before it is typed, and it starts
// no process: it rereads the task's record, or the CFO's registration, to see
// that the view's pane and generation are unchanged and that the process
// verified when the view opened is still alive. Only a change pays for the
// full verification, which refuses a changed recipient; the view is verified
// in full, custody included, when it opens and on every tick.
func (h *HTTP) stillBound(ctx context.Context, b terminalBinding, write bool) error {
	if b.Process.VerifiedAlive() && h.Service.sameBinding(b) {
		return nil
	}
	return h.verifyTerminal(ctx, b, write)
}

// sameBinding reports from local files alone that nothing a view is bound to
// was replaced: the CFO's registration, or the task's generation, session and
// pane.
func (s *Service) sameBinding(b terminalBinding) bool {
	if b.Selection.Task == "" {
		file, err := openPrimary(filepath.Join(s.Options.CFO.State, "primary.json"))
		if err != nil {
			return false
		}
		defer file.Close()
		_, identity, err := decodePrimary(file)
		return err == nil && identity == b.Identity
	}
	if b.Selection.Session != "" && s.Store.Snapshot().TaskSessions[b.Selection.Task] != b.Selection.Session {
		return false
	}
	meta, err := state.ReadTaskMeta(s.Store.Home.State, b.Selection.Task)
	return err == nil && meta.SpawnGen == b.Selection.Generation && (herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}) == b.Target
}

// terminalCustody is whether a no-mistakes gate lets the Overlord type into a
// goblin's pane now; the CFO's own terminal has no gate.
func (s *Service) terminalCustody(ctx context.Context, b terminalBinding) error {
	if b.Selection.Task == "" {
		return nil
	}
	meta, err := state.ReadTaskMeta(s.Store.Home.State, b.Selection.Task)
	if err != nil {
		return err
	}
	return s.validateTerminalControl(ctx, meta)
}

func (h *HTTP) verifyTerminal(ctx context.Context, b terminalBinding, write bool) error {
	fresh, err := h.Service.resolveTerminal(ctx, b.Selection, write)
	if err != nil {
		return err
	}
	if fresh.Identity != b.Identity || fresh.Terminal != b.Terminal || !b.Process.VerifiedAlive() {
		return errors.New("Native terminal identity changed. Select the current session; input was discarded.")
	}
	return nil
}

// paneSize is the size Herdr lays the pane out at, clamped to what an observer
// accepts.
func (h *HTTP) paneSize(ctx context.Context, target herdr.Target) (int, int, error) {
	client := h.Service.Options.CFO.Terminals(target.Session)
	check, stop := context.WithTimeout(ctx, 8*time.Second)
	defer stop()
	snapshot, err := client.Snapshot(check)
	if err == nil {
		for _, layout := range snapshot.Layouts {
			for _, pane := range layout.Panes {
				if pane.ID == target.Pane {
					return min(max(pane.Rect.Width, 20), 400), min(max(pane.Rect.Height, 5), 160), nil
				}
			}
		}
	}
	return 0, 0, errors.New("Herdr did not report this pane's size, so its screen cannot be shown whole.")
}

func decodeBody(w http.ResponseWriter, r *http.Request, value interface{}, limit int64) error {
	if r.Header.Get("Content-Type") != "application/json" {
		return errors.New("JSON required")
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return errors.New("Invalid request JSON")
	}
	if d.Decode(new(json.RawMessage)) != io.EOF {
		return errors.New("Only one request is accepted")
	}
	return nil
}

func (h *HTTP) terminalInput(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Lease   string                `json:"lease"`
		Seq     uint64                `json:"seq"`
		Command herdr.TerminalCommand `json:"command"`
	}
	if err := decodeBody(w, r, &input, 400<<10); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	h.mu.Lock()
	lease := h.terminals[input.Lease]
	h.mu.Unlock()
	if lease == nil {
		apiError(w, 409, "Terminal connection expired. Input was not replayed.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := lease.input(ctx, input.Seq, input.Command, h.stillBound); err != nil {
		apiError(w, 409, err.Error())
		return
	}
	respond(w, 200, struct {
		Seq uint64 `json:"seq"`
	}{input.Seq})
}

func (h *HTTP) terminalStream(w http.ResponseWriter, r *http.Request) {
	var input struct {
		terminalSelection
		Control  bool   `json:"control"`
		Identity string `json:"identity"`
		Cols     int    `json:"cols"`
		Rows     int    `json:"rows"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if err := (herdr.TerminalCommand{Type: "terminal.resize", Cols: input.Cols, Rows: input.Rows}).Validate(); input.Control && err != nil {
		apiError(w, 400, err.Error())
		return
	}
	select {
	case h.terminalSlots <- struct{}{}:
		defer func() { <-h.terminalSlots }()
	default:
		apiError(w, 429, "Too many native terminal views")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	check, stop := context.WithTimeout(ctx, 8*time.Second)
	b, err := h.Service.resolveTerminal(check, input.terminalSelection, input.Control)
	stop()
	if err != nil {
		var unavailable unavailableTerminal
		var stale registrationProblem
		switch {
		case errors.As(err, &unavailable):
			respond(w, 409, struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}{err.Error(), "terminal_unavailable"})
			return
		case errors.As(err, &stale):
			respond(w, 409, struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}{err.Error(), "registration_stale"})
			return
		}
		apiError(w, 409, err.Error())
		return
	}
	if input.Control && (input.Identity == "" || b.Identity != input.Identity) {
		apiError(w, 409, "Recipient changed. Observe the current terminal before connecting input.")
		return
	}
	cols, rows := input.Cols, input.Rows
	if !input.Control {
		// An observer sees only the part of the screen its size covers and
		// hears of no change outside it, so a view smaller than the pane is a
		// frozen top-left corner. Observe the pane at its own size.
		if cols, rows, err = h.paneSize(ctx, b.Target); err != nil {
			apiError(w, 409, err.Error())
			return
		}
	}
	open := h.openTerminal
	stream, err := open(ctx, b.Target.Session, b.Terminal, input.Control, cols, rows)
	if err != nil {
		apiError(w, 503, "Native terminal attachment is unavailable.")
		return
	}
	defer stream.Close()
	var id [24]byte
	if _, err := rand.Read(id[:]); err != nil {
		apiError(w, 500, "Terminal connection could not be allocated")
		return
	}
	key := hex.EncodeToString(id[:])
	ask, asked := context.WithTimeout(ctx, 8*time.Second)
	custody := h.Service.terminalCustody(ask, b)
	asked()
	lease := &terminalLease{binding: b, stream: stream, control: input.Control, cancel: cancel, typist: h.Service.Options.CFO.Terminals("").SendLiteral, custody: custody}
	h.mu.Lock()
	h.terminals[key] = lease
	h.mu.Unlock()
	unregister := func() {
		h.mu.Lock()
		delete(h.terminals, key)
		h.mu.Unlock()
	}
	defer func() {
		// Refuse new input before waiting for the observer to exit, and close
		// the pipe before acquiring the input mutex: a native stdin write may be
		// holding it while the output side disconnects.
		unregister()
		cancel()
		_ = stream.Close()
		lease.mu.Lock()
		lease.closed = true
		lease.mu.Unlock()
	}()
	frames := make(chan herdr.TerminalFrame)
	go func() {
		defer close(frames)
		for {
			f, err := stream.Next()
			if err != nil {
				return
			}
			select {
			case frames <- f:
			case <-ctx.Done():
				return
			}
			if f.Type == "terminal.closed" {
				return
			}
		}
	}()
	w.Header().Set("Content-Type", "application/x-ndjson")
	controller := http.NewResponseController(w)
	write := func(value interface{}) error {
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := json.NewEncoder(w).Encode(value); err != nil {
			return err
		}
		return controller.Flush()
	}
	first := time.NewTimer(8 * time.Second)
	defer first.Stop()
	tick := time.NewTicker(h.terminalTick)
	defer tick.Stop()
	full := false
	var seq uint64
	closed := func(reason string) {
		unregister()
		_ = write(herdr.TerminalFrame{Type: "terminal.closed", Reason: reason})
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.Service.done:
			return
		case <-first.C:
			closed("Native terminal did not provide its initial screen. It may be closed or controlled by another client.")
			return
		case <-tick.C:
			check, stop := context.WithTimeout(ctx, 8*time.Second)
			err := h.verifyTerminal(check, b, false)
			var custody error
			if err == nil {
				custody = h.Service.terminalCustody(check, b)
			}
			stop()
			if err != nil {
				closed(err.Error())
				return
			}
			lease.mu.Lock()
			lease.custody = custody
			lease.mu.Unlock()
			if !input.Control {
				paneCols, paneRows, err := h.paneSize(ctx, b.Target)
				if err != nil {
					closed(err.Error())
					return
				}
				if paneCols != cols || paneRows != rows {
					closed("The pane was resized. Reconnect to see it whole at its new size.")
					return
				}
			}
			if err := write(struct {
				Type string `json:"type"`
			}{"terminal.alive"}); err != nil {
				return
			}
		case f, ok := <-frames:
			if !ok {
				closed("Native terminal disconnected or is already controlled elsewhere. No takeover was attempted.")
				return
			}
			if f.Type == "terminal.closed" {
				closed("Native connection ended: " + redact(bounded(f.Reason, 300)))
				return
			}
			if (!full && !f.Full) || (full && !f.Full && f.Seq != seq+1) {
				closed("Native screen synchronization was lost. Reconnect for a full frame.")
				return
			}
			if !full {
				check, stop := context.WithTimeout(ctx, 8*time.Second)
				err := h.verifyTerminal(check, b, input.Control)
				stop()
				if err != nil {
					closed(err.Error())
					return
				}
				if err := write(struct {
					Type     string `json:"type"`
					Lease    string `json:"lease"`
					Identity string `json:"identity"`
					Terminal string `json:"terminal"`
					Control  bool   `json:"control"`
				}{"terminal.ready", key, b.Identity, b.Terminal, input.Control}); err != nil {
					return
				}
				full = true
				first.Stop()
			}
			seq = f.Seq
			if err := write(f); err != nil {
				return
			}
		}
	}
}
