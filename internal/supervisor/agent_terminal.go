package supervisor

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// childOutput answers a child's own output, for its terminal on the board: a
// child of the task's goblin that the board's last read of the goblin's tree
// holds, read as that read found it (fleettree.Reader.ChildOutput), never
// from a path the browser names.
func (h *HTTP) childOutput(meta state.TaskMeta, node string) (fleettree.Output, error) {
	h.Service.mu.Lock()
	tree, isRead := h.Service.trees[meta.ID]
	h.Service.mu.Unlock()
	if !isRead || tree.Generation != meta.SpawnGen || !slices.ContainsFunc(tree.Children, func(child fleettree.Node) bool {
		return child.ID == node && child.Kind != fleettree.KindHelper
	}) || h.Service.Options.Tree == nil {
		return fleettree.Output{}, fleettree.ErrNoRecord
	}
	return h.Service.Options.Tree.ChildOutput(meta.ID, node)
}

func (h *HTTP) agentOutput(w http.ResponseWriter, r *http.Request, meta state.TaskMeta, node string) {
	if offMachine(r, h.Host) != "" {
		apiError(w, http.StatusForbidden, "Child output is available only on this computer")
		return
	}
	output, err := h.childOutput(meta, node)
	if errors.Is(err, fleettree.ErrNoRecord) {
		apiError(w, 404, "The child's output is not kept")
		return
	}
	if err != nil {
		apiError(w, 503, "The child's output cannot be read")
		return
	}
	if r.URL.Query().Get("stream") == "1" {
		h.streamChild(w, r, meta, node, output)
		return
	}
	respond(w, 200, output)
}

func (h *HTTP) streamChild(w http.ResponseWriter, r *http.Request, meta state.TaskMeta, node string, output fleettree.Output) {
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
	var previous []byte
	for {
		data, err := json.Marshal(output)
		if err != nil {
			return
		}
		if !bytes.Equal(previous, data) {
			if !send("output", data) {
				return
			}
			previous = data
		}
		select {
		case <-r.Context().Done():
			return
		case <-end.C:
			return
		case <-tick.C:
		}
		output, err = h.childOutput(meta, node)
		if err != nil {
			send("unavailable", []byte(`"The child's output is no longer available"`))
			return
		}
	}
}

// messagesBinding is the session a request for messages names: a task's
// goblin of the generation it names, or the CFO in the terminal it names.
// current says whether that session still runs, and names its conversation
// as the session knows it: the task's generation, the CFO's session. locate
// is that conversation's transcript, empty while Claude Code has written
// none.
type messagesBinding struct {
	current func() (string, error)
	locate  func(ctx context.Context, current string) string
}

// messagesRefused is why a request names no session whose messages the
// board reads, with the status it answers.
type messagesRefused struct {
	status int
	reason string
}

func (refused messagesRefused) Error() string { return refused.reason }

var claudeOnly = messagesRefused{404, "Messages are read from Claude Code sessions only."}

// bindMessages binds a request to the session its query names.
func (h *HTTP) bindMessages(query url.Values) (messagesBinding, error) {
	stateDir, tree := h.Service.Store.Home.State, h.Service.Options.Tree
	if want := query.Get("cfo"); want != "" {
		return messagesBinding{current: func() (string, error) {
			cfo := readCFOState(stateDir)
			harness, session, _ := strings.Cut(cfo.session, "/")
			switch {
			case cfo.terminal == "":
				return "", messagesRefused{404, "No CFO is running in a native terminal."}
			case cfo.terminal != want:
				return "", messagesRefused{409, "The CFO runs in another terminal now. Open the CFO again."}
			case harness != "claude":
				return "", claudeOnly
			}
			return session, nil
		}, locate: func(_ context.Context, session string) string {
			return fleettree.SessionTranscript(tree.Home, "claude", session)
		}}, nil
	}
	task, generation := query.Get("task"), query.Get("generation")
	if state.ValidTaskID(task) != nil || generation == "" {
		return messagesBinding{}, messagesRefused{400, "Select a task's current session or the CFO."}
	}
	return messagesBinding{current: func() (string, error) {
		meta, err := state.ReadTaskMeta(stateDir, task)
		switch {
		case err != nil:
			return "", messagesRefused{404, "Task metadata unavailable"}
		case meta.SpawnGen != generation:
			return "", messagesRefused{409, "This task restarted or was replaced. Select its current session."}
		case meta.Harness != "claude":
			return "", claudeOnly
		}
		return generation, nil
	}, locate: func(ctx context.Context, _ string) string {
		// The goblin's own conversation, chosen as the board's read of its
		// tree chooses it (readTrees).
		meta, err := state.ReadTaskMeta(stateDir, task)
		if err != nil {
			return ""
		}
		goblin := fleettree.Goblin{Meta: meta}
		if record, err := host.ReadRecord(stateDir, meta.ID); meta.Backend == "native" && err == nil {
			goblin.HarnessPID, goblin.HarnessStarted = record.ChildPID, record.ChildStart
		}
		return tree.Conversation(ctx, goblin)
	}}, nil
}

// messages answers a Claude Code session's messages for the board's message
// view, read by fleettree.Reader from the conversation the supervisor finds
// for the session the query names, never a path the browser names: a page
// of them, the newest or those before seq before; a picture one of them
// holds; or, with stream=1, a stream of server-sent events: a page, then the
// messages that began or changed, as they are written.
func (h *HTTP) messages(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	// A browser's event stream cannot send the board's token in a header,
	// so it comes in the query, as a terminal view's does.
	if subtle.ConstantTimeCompare([]byte(query.Get("token")), []byte(h.Service.Instance)) != 1 {
		apiError(w, http.StatusForbidden, "Refresh the board before reading its messages")
		return
	}
	before := -1
	if query.Has("before") {
		var err error
		if before, err = strconv.Atoi(query.Get("before")); err != nil || before < 0 {
			apiError(w, 400, "before names no message")
			return
		}
	}
	if h.Service.Options.Tree == nil {
		apiError(w, 503, "Messages cannot be read")
		return
	}
	binding, err := h.bindMessages(query)
	if err != nil {
		messagesError(w, err)
		return
	}
	current, err := binding.current()
	if err != nil {
		messagesError(w, err)
		return
	}
	path := binding.locate(r.Context(), current)
	switch {
	case query.Has("picture"):
		mediaType, data, err := h.Service.Options.Tree.MessagePicture(path, query.Get("picture"))
		if err != nil {
			messagesError(w, err)
			return
		}
		w.Header().Set("Content-Type", mediaType)
		_, _ = w.Write(data)
	case query.Get("stream") == "1":
		h.streamMessages(w, r, binding, current, path)
	default:
		page, _, err := h.messagePage(path, before)
		if err != nil {
			messagesError(w, err)
			return
		}
		respond(w, 200, page)
	}
}

// messagePage is the page of the messages of the conversation at path
// before seq before, or its newest page, and none while there is no
// conversation.
func (h *HTTP) messagePage(path string, before int) (fleettree.MessagePage, fleettree.MessageCursor, error) {
	if path == "" {
		return fleettree.MessagePage{Messages: []fleettree.Message{}}, fleettree.MessageCursor{}, nil
	}
	return h.Service.Options.Tree.MessagePage(path, before)
}

func messagesError(w http.ResponseWriter, err error) {
	var refused messagesRefused
	switch {
	case errors.As(err, &refused):
		apiError(w, refused.status, refused.reason)
	case errors.Is(err, fleettree.ErrNoRecord):
		apiError(w, 404, "No such picture is kept")
	default:
		apiError(w, 503, "The session's messages cannot be read")
	}
}

// streamMessages streams a session's messages: a page event with its newest
// page, then a messages event with each message that began or changed, read
// every half second, which a view puts in place of the one with the same id
// or adds by its seq. A page event again replaces everything the view holds:
// the session moved to another conversation, or its conversation was
// replaced. The session is checked as often as a terminal view's, and an
// unavailable event says why it no longer runs before the stream ends.
func (h *HTTP) streamMessages(w http.ResponseWriter, r *http.Request, binding messagesBinding, current, path string) {
	select {
	case h.messageSlots <- struct{}{}:
		defer func() { <-h.messageSlots }()
	default:
		apiError(w, 429, "Too many message views are open")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, 500, "Streaming unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(event string, value any) bool {
		data, err := json.Marshal(value)
		if err != nil {
			return false
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	var cursor fleettree.MessageCursor
	page := func() bool {
		messages, now, err := h.messagePage(path, -1)
		if err != nil {
			send("unavailable", "The session's messages can no longer be read")
			return false
		}
		cursor = now
		return send("page", messages)
	}
	if !page() {
		return
	}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	check := time.NewTicker(h.terminalTick)
	defer check.Stop()
	// Renew long-lived connections so a board that vanished holds no slot.
	end := time.NewTimer(10 * time.Minute)
	defer end.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-end.C:
			return
		case <-check.C:
			now, err := binding.current()
			if err != nil {
				send("unavailable", err.Error())
				return
			}
			if now == current && path != "" {
				continue
			}
			current = now
			if located := binding.locate(r.Context(), now); located != path {
				path = located
				if !page() {
					return
				}
			}
		case <-tick.C:
			if path == "" {
				continue
			}
			messages, now, isAnew, err := h.Service.Options.Tree.MessagesSince(path, cursor)
			switch {
			case err != nil:
				send("unavailable", "The session's messages can no longer be read")
				return
			case isAnew:
				if !page() {
					return
				}
			case len(messages) > 0:
				cursor = now
				if !send("messages", messages) {
					return
				}
			}
		}
	}
}
