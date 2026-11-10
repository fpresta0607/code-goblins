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
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/sessionstream"
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

// messagesRefused is why a request names no session whose messages the
// board reads, with the status it answers.
type messagesRefused struct {
	status int
	reason string
}

func (refused messagesRefused) Error() string { return refused.reason }

// noMessages is why a session of a harness whose record the board cannot read
// as messages answers: any harness but Claude Code, Codex and pi.
var noMessages = messagesRefused{404, "Messages are read from Claude Code, Codex and pi sessions only."}

// messagesHarness says whether the board reads a harness's session as messages.
func messagesHarness(harness string) bool {
	return harness == "claude" || harness == "codex" || harness == "pi"
}

// messagesSession is the session a request for messages names: the record its
// harness keeps of the conversation, and the identity of the session stream
// the fleet keeps for it, which the board renders.
type messagesSession struct {
	Path     string
	Identity sessionstream.Identity
	// Stale says why the conversation the board read is not the one the
	// session writes now, and is empty when it is, so a record naming a
	// conversation that stopped is reported rather than rendered as current.
	Stale string
}

// messagesLocator finds the session a request for messages names, a task's
// goblin of the generation it names or the CFO in the terminal it names,
// while that session runs in a native terminal: no path while its harness has
// written none, and messagesRefused once the session no longer runs or is one
// the board does not read. A goblin's locating reads no process and parses no
// store file, since a view's stream asks again as often as a terminal view's
// binding is checked; the CFO's proves the live conversation its harness is
// in, which it records when the record named another, so a stale record is
// reported rather than rendered as current.
func (h *HTTP) messagesLocator(ctx context.Context, query url.Values) (func() (messagesSession, error), error) {
	stateDir, home := h.Service.Store.Home.State, h.Service.Options.Tree.Home
	if want := query.Get("cfo"); want != "" {
		return func() (messagesSession, error) {
			cfo := readCFOState(stateDir)
			switch {
			case cfo.terminal == "":
				return messagesSession{}, messagesRefused{404, "No CFO is running in a native terminal."}
			case cfo.terminal != want:
				return messagesSession{}, messagesRefused{409, "The CFO runs in another terminal now. Open the CFO again."}
			case cfo.harness != "" && !messagesHarness(cfo.harness):
				return messagesSession{}, noMessages
			}
			// A CFO still starting has registered no session yet.
			harness := cfo.harness
			if harness == "" {
				harness = "claude"
			}
			// The conversation its harness is in now, which the record is
			// brought up to date with, so a session that moved without
			// registering again is never read from the one it left (#2172).
			session, stale := h.cfoConversation(ctx, cfo)
			// The CFO is started in the fleet's home, so its conversation is one
			// that home's folder holds, and another folder's is never read as
			// the CFO's.
			path := ""
			if fleettree.HoldsTranscript(home, harness, h.Service.Store.Home.Root, session) {
				path = fleettree.HeldTranscript(home, harness, h.Service.Store.Home.Root, session)
			}
			return messagesSession{
				Path:     path,
				Identity: sessionstream.Identity{Harness: harness, Session: session},
				Stale:    stale,
			}, nil
		}, nil
	}
	task, generation := query.Get("task"), query.Get("generation")
	if state.ValidTaskID(task) != nil || generation == "" {
		return nil, messagesRefused{400, "Select a task's current session or the CFO."}
	}
	return func() (messagesSession, error) {
		meta, err := state.ReadTaskMeta(stateDir, task)
		switch {
		case err != nil:
			return messagesSession{}, messagesRefused{404, "Task metadata unavailable"}
		case meta.SpawnGen != generation:
			return messagesSession{}, messagesRefused{409, "This task restarted or was replaced. Select its current session."}
		case meta.Backend != "native":
			// The view is a native terminal's: its box types through that
			// terminal, and its stream reads the record the terminal's
			// harness writes. A pane of another kind names no such record
			// to read, so it keeps its own terminal and task views alone.
			return messagesSession{}, messagesRefused{404, "Messages are read from a session in a native terminal."}
		case !messagesHarness(meta.Harness):
			return messagesSession{}, noMessages
		}
		// The conversation the board recorded for the generation, else the
		// one its read of the goblin's tree proved the running harness in
		// and kept (keepConversation), as fleettree.OwnedSession reads them.
		session := h.Service.Store.recordedSession(meta)
		if session == "" {
			if session, err = fleettree.KeptSession(stateDir, meta); err != nil {
				return messagesSession{}, err
			}
		}
		return messagesSession{
			Path: fleettree.HeldTranscript(home, meta.Harness, meta.Worktree, session),
			Identity: sessionstream.Identity{
				Task: meta.ID, Generation: meta.SpawnGen, Harness: meta.Harness, Session: session,
			},
		}, nil
	}, nil
}

// messages answers a Claude Code, Codex or pi session's messages for the
// board's message view, read by fleettree.Reader from the conversation the
// supervisor finds for the session the query names, never a path the browser
// names: a page of them, the newest or those before seq before; a picture one
// of them holds; or, with stream=1, a stream of server-sent events: a page,
// then the messages that began or changed, as they are written.
func (h *HTTP) messages(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Has("document") {
		h.messageDocument(w, r)
		return
	}
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
		if query.Get("conversation") == "" {
			apiError(w, 400, "conversation is required for older messages")
			return
		}
	}
	if h.Service.Options.Tree == nil {
		apiError(w, 503, "Messages cannot be read")
		return
	}
	locate, err := h.messagesLocator(r.Context(), query)
	if err != nil {
		messagesError(w, err)
		return
	}
	session, err := locate()
	if err != nil {
		messagesError(w, err)
		return
	}
	path := session.Path
	switch {
	case query.Has("picture"):
		if query.Get("conversation") == "" {
			apiError(w, http.StatusBadRequest, "conversation is required for pictures")
			return
		}
		mediaType, data, err := h.Service.Options.Tree.MessagePicture(path, query.Get("conversation"), query.Get("picture"))
		if err != nil {
			messagesError(w, err)
			return
		}
		w.Header().Set("Content-Type", mediaType)
		_, _ = w.Write(data)
	case query.Get("stream") == "1":
		h.streamMessages(w, r, locate, session, query.Get("task"))
	default:
		page, _, err := h.messagePage(path, before, query.Get("task"))
		if err != nil {
			messagesError(w, err)
			return
		}
		if before >= 0 && page.Conversation != query.Get("conversation") {
			apiError(w, http.StatusConflict, "Conversation changed")
			return
		}
		if recorded, err := h.recordSession(session, page.Messages, before < 0, before < 0); err == nil {
			page = pageOfStream(page, recorded, before)
		}
		respond(w, 200, page)
	}
}

// messagePage is the page of the messages of the conversation at path
// before seq before, or its newest page, and none while there is no
// conversation.
func (h *HTTP) messagePage(path string, before int, task string) (fleettree.MessagePage, fleettree.MessageCursor, error) {
	if path == "" {
		cursor := fleettree.MessageCursor{}
		return fleettree.MessagePage{Conversation: cursor.Conversation(), Messages: []fleettree.Message{}}, cursor, nil
	}
	page, cursor, err := h.Service.Options.Tree.MessagePage(path, before)
	page.Messages, page.Waiting = shownShellCommands(h.Service.Store.Home.State, page.Messages, page.Waiting)
	page.Waiting = h.Service.Store.namedWaiting(task, page.Waiting)
	return page, cursor, err
}

// pageOfStream is the page the board draws: the messages the fleet's stream
// holds for the session, in place of the harness's own read of them, with the
// conversation the view names and whether older messages come before it. A
// page the stream does not hold, as an older one before a stream that began
// after it, is answered from the harness's record instead, so a session
// recorded before streams existed still renders as it did.
func pageOfStream(page fleettree.MessagePage, projection []fleettree.Message, before int) fleettree.MessagePage {
	if len(projection) == 0 {
		return page
	}
	kept := projection
	if before >= 0 {
		older := make([]fleettree.Message, 0, len(projection))
		for _, message := range projection {
			if message.Seq < before {
				older = append(older, message)
			}
		}
		kept = older
	}
	if len(kept) == 0 {
		return page
	}
	if len(kept) > fleettree.MessagesPerPage {
		kept = kept[len(kept)-fleettree.MessagesPerPage:]
		page.More = true
	}
	page.Messages = kept
	return page
}

// changedOfStream is the messages a view is told changed, as the fleet's
// stream holds them now, so a view draws the same words the stream does.
func changedOfStream(changed, projection []fleettree.Message) []fleettree.Message {
	held := make(map[string]fleettree.Message, len(projection))
	for _, message := range projection {
		held[message.ID] = message
	}
	kept := make([]fleettree.Message, 0, len(changed))
	for _, message := range changed {
		if current, ok := held[message.ID]; ok {
			kept = append(kept, current)
			continue
		}
		kept = append(kept, message)
	}
	return kept
}

func messagesError(w http.ResponseWriter, err error) {
	var refused messagesRefused
	switch {
	case errors.As(err, &refused):
		apiError(w, refused.status, refused.reason)
	case errors.Is(err, fleettree.ErrNoRecord):
		apiError(w, 404, "No such picture is kept")
	default:
		apiError(w, 503, unreadMessages)
	}
}

const unreadMessages = "The session's messages cannot be read"

// streamMessages streams a session's messages: a page event with its newest
// page, then a messages event with each message that began or changed, read
// every half second, which a view puts in place of the one with the same id
// or adds by its seq. Each messages and usage event names its page's
// conversation. A page event of another conversation replaces everything the
// view holds: the session moved to another conversation, or its conversation
// was replaced. The session is located again as often as a terminal view's
// binding is checked, and an unavailable event says why it no longer runs, or
// cannot be read, before the stream ends.
//
// A session event says what the fleet's stream says of the session: its
// newest turn, whether it is running, ended or not reported, and where the
// stream's head is. A view that has drawn less than the head is behind, which
// it says itself; nothing infers staleness from how long the stream was
// silent. The same event keeps the connection alive.
func (h *HTTP) streamMessages(w http.ResponseWriter, r *http.Request, locate func() (messagesSession, error), session messagesSession, task string) {
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
	var sent time.Time
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
		sent = time.Now()
		return true
	}
	var cursor fleettree.MessageCursor
	var lastUsage *fleettree.Usage
	var lastSession string
	var lastWaiting []fleettree.WaitingPrompt
	// usage tells the board what the session has spent, from what the last read
	// already found, so the footer's stats keep up with its turns.
	usage := func() bool {
		if held := h.Service.Options.Tree.UsageAt(session.Path, cursor); held != nil && (lastUsage == nil || *held != *lastUsage) {
			if !send("usage", struct {
				Conversation string           `json:"conversation"`
				Usage        *fleettree.Usage `json:"usage"`
			}{cursor.Conversation(), held}) {
				return false
			}
			lastUsage = held
		}
		return true
	}
	// turn says what the fleet's stream says of the session's turn, so a view
	// knows from the stream itself whether a turn runs, ended, or was never
	// reported, and where the head of the stream it draws is.
	turn := func() bool {
		// A session whose harness has written no conversation yet has no
		// stream and no turn, and nothing to report of either.
		if session.Identity.Check() != nil {
			return true
		}
		_, recordErr := h.recordSession(session, nil, true, false)
		held, err := h.Service.sessionStreamOf(session.Identity)
		if err != nil {
			return true
		}
		messages, events := held.conversation()
		turnID, state, why := streamTurn(messages, events)
		head := len(events) - 1
		// -1 is a session whose stream holds no message, so a view holding
		// none either is not behind it.
		newest := -1
		if len(messages) > 0 {
			newest = messages[len(messages)-1].Seq
		}
		problem := ""
		if recordErr != nil {
			problem = "The board cannot write or read this session's conversation stream, so it draws the session's own record: " + recordErr.Error()
		}
		reported := struct {
			Conversation string `json:"conversation"`
			Turn         string `json:"turn"`
			State        string `json:"state"`
			Why          string `json:"why,omitempty"`
			Newest       int    `json:"newest"`
			Head         int    `json:"head"`
			Stale        string `json:"stale,omitempty"`
			Problem      string `json:"problem,omitempty"`
		}{cursor.Conversation(), turnID, state, why, newest, head, session.Stale, problem}
		data, err := json.Marshal(reported)
		if err != nil || string(data) == lastSession {
			return true
		}
		lastSession = string(data)
		return send("session", reported)
	}
	page := func() bool {
		messages, now, err := h.messagePage(session.Path, -1, task)
		if err != nil {
			send("unavailable", unreadMessages)
			return false
		}
		cursor = now
		if recorded, err := h.recordSession(session, messages.Messages, true, true); err == nil {
			messages = pageOfStream(messages, recorded, -1)
		}
		if !send("page", messages) {
			return false
		}
		lastWaiting = messages.Waiting
		lastUsage = nil
		return usage() && turn()
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
			located, err := locate()
			if err != nil {
				reason, refused := unreadMessages, messagesRefused{}
				if errors.As(err, &refused) {
					reason = refused.reason
				}
				send("unavailable", reason)
				return
			}
			// A record that goes stale, or stops being stale, while the session
			// itself stands is told by the next turn's report.
			isSame := located.Path == session.Path && located.Identity == session.Identity
			session = located
			if !isSame && !page() {
				return
			}
		case <-tick.C:
			// A stream that has nothing to send says it is still there, and
			// what the session's turn is, so a board that hears nothing at
			// all knows the stream itself stopped rather than the session
			// being quiet.
			if time.Since(sent) >= h.terminalTick && !send("alive", struct{}{}) {
				return
			}
			if !turn() {
				return
			}
			if session.Path == "" {
				continue
			}
			messages, waiting, now, isAnew, err := h.Service.Options.Tree.MessagesSince(session.Path, cursor)
			messages, waiting = shownShellCommands(h.Service.Store.Home.State, messages, waiting)
			waiting = h.Service.Store.namedWaiting(task, waiting)
			switch {
			case err != nil:
				send("unavailable", unreadMessages)
				return
			case isAnew:
				if !page() {
					return
				}
			default:
				cursor = now
				if len(messages) > 0 {
					if recorded, err := h.recordSession(session, messages, true, false); err == nil {
						messages = changedOfStream(messages, recorded)
					}
				}
				// What the harness holds rides with the messages, so a prompt
				// leaves the one in the event that brings it to the other, and
				// the board never draws it twice.
				if (len(messages) > 0 || !slices.Equal(waiting, lastWaiting)) && !send("messages", struct {
					Conversation string                    `json:"conversation"`
					Messages     []fleettree.Message       `json:"messages"`
					Waiting      []fleettree.WaitingPrompt `json:"waiting"`
				}{cursor.Conversation(), messages, waiting}) {
					return
				}
				lastWaiting = waiting
				if !usage() || !turn() {
					return
				}
			}
		}
	}
}
