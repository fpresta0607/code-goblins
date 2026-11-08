package supervisor

import (
	"errors"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/sessionstream"
)

// A session stream is the fleet's one conversation record for a session: the
// supervisor writes the events its own read of the harness already produced,
// and the board renders that projection rather than guessing at the session
// from three places at once.
//
// The supervisor is its only writer, so the stream it holds in memory is what
// the file holds; it reads the file again only after a compaction, which the
// appender makes when a stream reaches its bound.

// sessionTurn is what a session's own report says of its newest turn: whether
// it ended, and what ended it. Known is false while the harness has said
// nothing of a turn, which the board shows instead of inventing one.
type sessionTurn struct {
	Ended bool
	Known bool
	Why   string
}

// turnState reads what a session's own record says of its turn. A harness
// reports the end of a turn itself: Claude Code's Stop, Codex's turn
// completion and pi's settle each arrive as a session's own event, which the
// supervisor keeps as the session's phase, and a prompt that begins another
// turn makes it active again. A session whose hooks were never installed has
// said nothing, and says so rather than leaving the board to guess.
func turnState(node Session, isTracked bool, promptAt time.Time) sessionTurn {
	if !isTracked {
		return sessionTurn{Why: "The board has not seen this session report a turn, so it does not draw one as working."}
	}
	// A session's report is about the turn it last heard of. A prompt written
	// after it began another turn, whose own report has not reached the board
	// yet, and the phase before it says nothing of the new turn.
	if !promptAt.IsZero() && node.UpdatedAt.Before(promptAt) {
		return sessionTurn{Known: true}
	}
	switch node.Phase {
	case "settled", "interrupted", "ended":
		return sessionTurn{Ended: true, Known: true}
	case "active", "started", "compacting", "compacted":
		// A session that compacts is still in its turn: a compaction neither
		// starts nor ends one.
		return sessionTurn{Known: true}
	}
	return sessionTurn{Why: "This session's harness has not reported a turn yet, so the board does not draw one as working."}
}

// heldStream is one session's stream as the supervisor holds it: the appender
// that writes it, the events it holds in order, and the conversation they are.
type heldStream struct {
	mu       sync.Mutex
	id       sessionstream.Identity
	stateDir string
	asked    time.Time
	appender *sessionstream.Appender
	isRead   bool
	events   []sessionstream.Event
	messages []fleettree.Message
}

// maxHeldStreams bounds the streams the supervisor keeps in memory, so a
// board that has shown every session of a long run costs a few of them.
const maxHeldStreams = 16

// sessionStreamOf is the stream kept for id, opened on first use.
func (s *Service) sessionStreamOf(id sessionstream.Identity) (*heldStream, error) {
	key := id.Owner() + "\x00" + id.Generation + "\x00" + id.Harness + "\x00" + id.Session
	s.sessionStreamsMu.Lock()
	defer s.sessionStreamsMu.Unlock()
	if held, ok := s.sessionStreams[key]; ok {
		held.asked = time.Now()
		return held, nil
	}
	appender, err := sessionstream.Open(s.Store.Home.State, id)
	if err != nil {
		return nil, err
	}
	held := &heldStream{id: id, stateDir: s.Store.Home.State, appender: appender, asked: time.Now()}
	if s.sessionStreams == nil {
		s.sessionStreams = map[string]*heldStream{}
	}
	s.sessionStreams[key] = held
	// A stream a view no longer asks for is read again from its file when one
	// does, so dropping it loses no evidence.
	for len(s.sessionStreams) > maxHeldStreams {
		oldest, oldestKey := time.Now(), ""
		for each, stream := range s.sessionStreams {
			if each != key && stream.asked.Before(oldest) {
				oldest, oldestKey = stream.asked, each
			}
		}
		if oldestKey == "" {
			break
		}
		delete(s.sessionStreams, oldestKey)
	}
	return held, nil
}

// record writes the events of a read of the session that the stream does not
// hold yet, and answers the conversation the stream holds now.
func (held *heldStream) record(events []sessionstream.Event) ([]fleettree.Message, error) {
	held.mu.Lock()
	defer held.mu.Unlock()
	if !held.isRead {
		// A stream an earlier supervisor wrote is read once, so an append
		// continues its history rather than starting a second one.
		kept, err := sessionstream.Read(held.stateDir, held.id)
		switch {
		case err == nil:
			held.events = kept
		case errors.Is(err, sessionstream.ErrNoStream):
		case errors.Is(err, sessionstream.ErrUnreadable):
			// A damaged stream is never overwritten with a fresh one: the
			// board says it cannot read it, and the session's own record
			// stands until somebody looks.
			held.isRead = true
			return nil, err
		default:
			held.isRead = true
			return nil, err
		}
		held.isRead = true
		held.messages = fleettree.MessagesOf(held.events)
	}
	written, isCompacted, err := held.appender.Write(events)
	if err != nil {
		return nil, err
	}
	switch {
	case isCompacted:
		kept, err := sessionstream.Read(held.stateDir, held.id)
		if err != nil && !errors.Is(err, sessionstream.ErrNoStream) {
			return nil, err
		}
		held.events = kept
	case len(written) > 0:
		held.events = append(held.events, written...)
	}
	if len(written) > 0 || isCompacted {
		held.messages = fleettree.MessagesOf(held.events)
	}
	return held.messages, nil
}

// conversation is the conversation the stream holds and the events they are.
func (held *heldStream) conversation() ([]fleettree.Message, []sessionstream.Event) {
	held.mu.Lock()
	defer held.mu.Unlock()
	return held.messages, held.events
}

// streamTurn is what the stream says of a session's newest turn, in the words
// the board draws: running while the turn the newest prompt began has not
// ended, ended once the session's own report ended it, and unknown where the
// harness has reported nothing, with the reason it gives. A session with no
// turn yet says none.
func streamTurn(messages []fleettree.Message, events []sessionstream.Event) (turn, state, why string) {
	_, turn = fleettree.TurnOf(messages)
	if turn == "" {
		return "", "none", ""
	}
	for at := len(events) - 1; at >= 0; at-- {
		event := events[at]
		if event.Turn != turn {
			continue
		}
		switch event.Kind {
		case sessionstream.KindTurnEnd:
			return turn, "ended", ""
		case sessionstream.KindTurnState:
			return turn, "running", ""
		case sessionstream.KindMissing:
			return turn, "unknown", event.Missing
		}
	}
	return turn, "running", ""
}

// recordSession writes what the supervisor just read of a session into the
// fleet's stream for it, and answers the conversation the stream holds. A
// read of the newest messages also writes what the session's own report says
// of its turn, so the end of a turn is ordered with the messages it ended; a
// read further back leaves the turn alone.
func (h *HTTP) recordSession(session messagesSession, messages []fleettree.Message, isNewest bool) ([]fleettree.Message, error) {
	if err := session.Identity.Check(); err != nil {
		return nil, err
	}
	held, err := h.Service.sessionStreamOf(session.Identity)
	if err != nil {
		return nil, err
	}
	heldMessages, _ := held.conversation()
	events := fleettree.StreamEvents(session.Identity, messages)
	// Only a read of the newest messages can show that the record was
	// rewritten: a page read further back is older than what the stream holds
	// even when the harness dropped nothing.
	if isNewest && streamDiverges(heldMessages, messages) {
		// The harness rewrote its own record behind the reader, so the stream
		// restates what it holds rather than showing a message it dropped.
		events = []sessionstream.Event{fleettree.RestatedEvent(session.Identity, messages)}
	}
	projection, err := held.record(events)
	if err != nil {
		return projection, err
	}
	if !isNewest {
		return projection, nil
	}
	node, isTracked := h.Service.Store.nativeSession(session.Identity.Harness, session.Identity.Session)
	reported := turnEvents(session.Identity, projection, turnState(node, isTracked, newestPromptAt(projection)))
	if len(reported) == 0 {
		return projection, nil
	}
	return held.record(reported)
}

// streamDiverges reports whether a read of a session disagrees with the
// conversation its stream holds. A stream only appends, so a harness that
// dropped messages, as pi's branch reset does, needs its record restated; a
// read that merely carries on past the stream, or repeats what it holds, does
// not.
func streamDiverges(held, read []fleettree.Message) bool {
	if len(held) == 0 || len(read) == 0 {
		return false
	}
	if len(read) <= len(held) {
		return !sameMessageIDs(held[len(held)-len(read):], read)
	}
	return !sameMessageIDs(read[:len(held)], held)
}

// sameMessageIDs says two runs of messages are the same messages in the same
// order.
func sameMessageIDs(one, other []fleettree.Message) bool {
	if len(one) != len(other) {
		return false
	}
	for at := range one {
		if one[at].ID != other[at].ID || one[at].Seq != other[at].Seq {
			return false
		}
	}
	return true
}

// newestPromptAt is when the newest turn of a session began, which is what a
// session's own report of its turn is read against.
func newestPromptAt(messages []fleettree.Message) time.Time {
	for at := len(messages) - 1; at >= 0; at-- {
		if messages[at].Kind == fleettree.MessagePrompt && messages[at].Source != fleettree.FromNotice {
			return messages[at].At
		}
	}
	return time.Time{}
}

// turnEvents are the events a session's own report of its turn adds to its
// stream: the end of a turn, the end of a session, and the board's word where
// the harness reports nothing, which it draws rather than inventing a turn.
func turnEvents(id sessionstream.Identity, messages []fleettree.Message, turn sessionTurn) []sessionstream.Event {
	at := time.Now().UTC()
	_, prompt := fleettree.TurnOf(messages)
	if !turn.Known {
		return []sessionstream.Event{{
			ID: sessionstream.EventID(id, sessionstream.KindMissing, prompt, "", 0, turn.Why), At: at,
			Kind: sessionstream.KindMissing, Turn: prompt, Message: prompt, Missing: turn.Why,
		}}
	}
	if prompt == "" {
		return nil
	}
	// Every state the report gives is written, so one that supersedes an
	// earlier word of the same turn, as a session the board had not seen
	// report anything beginning to, is the one the board draws.
	kind := sessionstream.KindTurnState
	if turn.Ended {
		kind = sessionstream.KindTurnEnd
	}
	return []sessionstream.Event{{
		ID: sessionstream.EventID(id, kind, prompt, "", 0, "reported"), At: at,
		Kind: kind, Turn: prompt, Message: prompt,
	}}
}
