package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// childLines answers the newest lines of a child's own output, for its
// terminal on the board: a child of the task's goblin that the board's last
// read of the goblin's tree holds, read as that read found it
// (fleettree.Reader.ChildLines), never from a path the browser names.
func (h *HTTP) childLines(meta state.TaskMeta, node string) ([]string, error) {
	h.Service.mu.Lock()
	tree, isRead := h.Service.trees[meta.ID]
	h.Service.mu.Unlock()
	if !isRead || tree.Generation != meta.SpawnGen || !slices.ContainsFunc(tree.Children, func(child fleettree.Node) bool {
		return child.ID == node && child.Kind != fleettree.KindHelper
	}) || h.Service.Options.Tree == nil {
		return nil, fleettree.ErrNoRecord
	}
	return h.Service.Options.Tree.ChildLines(meta.ID, node)
}

func (h *HTTP) agentLines(w http.ResponseWriter, r *http.Request, meta state.TaskMeta, node string) {
	if offMachine(r, h.Host) != "" {
		apiError(w, http.StatusForbidden, "Child output is available only on this computer")
		return
	}
	lines, err := h.childLines(meta, node)
	if errors.Is(err, fleettree.ErrNoRecord) {
		apiError(w, 404, "The child's output is not kept")
		return
	}
	if err != nil {
		apiError(w, 503, "The child's output cannot be read")
		return
	}
	if r.URL.Query().Get("stream") == "1" {
		h.streamChild(w, r, meta, node, lines)
		return
	}
	respond(w, 200, struct {
		Lines []string `json:"lines"`
	}{lines})
}

func (h *HTTP) streamChild(w http.ResponseWriter, r *http.Request, meta state.TaskMeta, node string, lines []string) {
	select {
	case h.streams <- struct{}{}:
		defer func() { <-h.streams }()
	default:
		apiError(w, 429, "Too many event streams")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, 500, "Streaming unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(event string, data []byte) bool {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	// Renew long-lived connections so a board that vanished holds no slot.
	end := time.NewTimer(10 * time.Minute)
	defer end.Stop()
	var previous []string
	for first := true; ; first = false {
		if first || !slices.Equal(previous, lines) {
			data, err := json.Marshal(struct {
				Lines []string `json:"lines"`
			}{lines})
			if err != nil || !send("output", data) {
				return
			}
			previous = lines
		}
		select {
		case <-r.Context().Done():
			return
		case <-end.C:
			return
		case <-tick.C:
		}
		var err error
		lines, err = h.childLines(meta, node)
		if err != nil {
			send("unavailable", []byte(`"The child's output is no longer available"`))
			return
		}
	}
}
