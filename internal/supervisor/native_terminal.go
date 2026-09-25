package supervisor

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// nativeTerminal relays one view of a native task's terminal over a
// WebSocket: the host's output goes out as binary messages, typing comes back
// as binary messages, and a resize as a JSON text message. The view is bound
// to its terminal once, when it connects, by the host's own pipe, so a key
// costs no check and starts no process; custody and the task's generation are
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
	case h.terminalSlots <- struct{}{}:
		defer func() { <-h.terminalSlots }()
	default:
		_ = view.Close(websocket.StatusTryAgainLater, "Too many terminal views are open.")
		return
	}
	selection := terminalSelection{Task: r.URL.Query().Get("task"), Generation: r.URL.Query().Get("generation")}
	meta, err := h.Service.nativeTask(selection)
	if err != nil {
		_ = view.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}
	record, err := host.ReadRecord(h.Service.Store.Home.State, meta.ID)
	if err != nil {
		_ = view.Close(websocket.StatusPolicyViolation, "No terminal is running for this task.")
		return
	}
	terminal, err := host.Dial(record)
	if err != nil {
		_ = view.Close(websocket.StatusPolicyViolation, "This task's terminal did not answer.")
		return
	}
	defer terminal.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// mu guards custody, which each tick refreshes.
	var mu sync.Mutex
	check, stop := context.WithTimeout(ctx, 8*time.Second)
	custody := h.Service.validateTerminalControl(check, meta)
	stop()

	go func() {
		defer cancel()
		for {
			event, err := terminal.Next()
			switch {
			case err != nil:
				_ = view.Close(websocket.StatusGoingAway, "The terminal's host stopped answering.")
				return
			case event.Exited:
				_ = view.Close(websocket.StatusNormalClosure, fmt.Sprintf("The terminal ended with exit code %d.", event.Code))
				return
			}
			write, stop := context.WithTimeout(ctx, 5*time.Second)
			err = view.Write(write, websocket.MessageBinary, event.Output)
			stop()
			if err != nil {
				return
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
				current, err := h.Service.nativeTask(selection)
				if err != nil {
					_ = view.Close(websocket.StatusPolicyViolation, err.Error())
					return
				}
				check, stop := context.WithTimeout(ctx, 8*time.Second)
				refused := h.Service.validateTerminalControl(check, current)
				stop()
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
			var resize struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &resize) != nil || resize.Type != "resize" {
				_ = view.Close(websocket.StatusUnsupportedData, "A text message must be a resize.")
				return
			}
			mu.Lock()
			refused := custody
			mu.Unlock()
			if refused != nil {
				continue
			}
			if terminal.Resize(resize.Cols, resize.Rows) != nil {
				_ = view.Close(websocket.StatusUnsupportedData, "A text message must be a resize.")
				return
			}
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
