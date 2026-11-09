package supervisor

import (
	"errors"
	"slices"
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
// said nothing, and says so rather than leaving the board to guess. The
// report is read against the session's messages, the conversation the stream
// holds.
func turnState(node Session, isTracked bool, messages []fleettree.Message) sessionTurn {
	if !isTracked {
		return sessionTurn{Why: "The board has not seen this session report a turn, so it does not draw one as working."}
	}
	// A session's report is about the turn it last heard of. Claude Code runs
	// no hook for a turn the Overlord stops, and its record says where the turn
	// was stopped: a report older than an interrupt after the turn's prompt is
	// of the turn that stopped.
	prompt := fleettree.TurnPrompt(messages)
	if slices.ContainsFunc(messages, func(message fleettree.Message) bool {
		return message.Kind == fleettree.MessageInterrupt && message.Seq > prompt.Seq && node.UpdatedAt.Before(message.At)
	}) {
		return sessionTurn{Ended: true, Known: true}
	}
	// A prompt written after the report began another turn, whose own report
	// has not reached the board yet, and the phase before it says nothing of
	// the new turn.
	if node.UpdatedAt.Before(prompt.At) {
		return sessionTurn{Known: true}
	}
	switch node.Phase {
	case "settled", "interrupted", "ended":
		return sessionTurn{Ended: true, Known: true}
	case "active", "started":
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

// record writes the events derive makes of the stream as it holds the session
// now, its conversation and the events it is, that the stream does not hold
// yet, and answers the conversation the stream holds after. The stream an
// earlier supervisor wrote is read before derive looks at it, so a read of the
// session is weighed against all the stream holds, never only what this
// supervisor has written of it.
func (held *heldStream) record(derive func(messages []fleettree.Message, events []sessionstream.Event) []sessionstream.Event) ([]fleettree.Message, error) {
	held.mu.Lock()
	defer held.mu.Unlock()
	if !held.isRead {
		// A stream an earlier supervisor wrote is read once, so an append
		// continues its history rather than starting a second one.
		kept, err := sessionstream.Read(held.stateDir, held.id)
		switch {
		case err == nil:
			held.events = kept
		case !errors.Is(err, sessionstream.ErrNoStream):
			// A damaged stream is never overwritten with a fresh one: the
			// board says it cannot read it, and the session's own record
			// stands until somebody looks.
			held.isRead = true
			return nil, err
		}
		held.isRead = true
		held.messages = fleettree.MessagesOf(held.events)
	}
	written, isCompacted, err := held.appender.Write(derive(held.messages, held.events))
	if err != nil {
		// A stream this supervisor could not write is read again from its file
		// before the board draws it, so what the board draws is never older
		// than what the stream holds.
		held.isRead = false
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
// harness has reported nothing, with the reason it gives. A turn whose prompt
// the stream does not hold, as one that began more than a page back does, is
// the unnamed turn, read from the session's own report all the same. A session
// with no turn yet says none.
func streamTurn(messages []fleettree.Message, events []sessionstream.Event) (turn, state, why string) {
	turn = fleettree.TurnPrompt(messages).ID
	word := turnWord(events, turn)
	switch {
	case turn == "" && (len(messages) == 0 || word.Kind == ""):
		return "", "none", ""
	case word.Kind == sessionstream.KindTurnEnd:
		return turn, "ended", ""
	case word.Kind == sessionstream.KindMissing:
		return turn, "unknown", word.Missing
	}
	return turn, "running", ""
}

// turnWord is the newest word the stream holds of a turn: the session's report
// that it runs or ended, or the board's word that its harness reported nothing,
// and none while the stream holds no word of it.
func turnWord(events []sessionstream.Event, turn string) sessionstream.Event {
	for at := len(events) - 1; at >= 0; at-- {
		switch event := events[at]; event.Kind {
		case sessionstream.KindTurnEnd, sessionstream.KindTurnState, sessionstream.KindMissing:
			if event.Turn == turn {
				return event
			}
		}
	}
	return sessionstream.Event{}
}

// recordSession writes what the supervisor just read of a session into the
// fleet's stream for it, and answers the conversation the stream holds. Only a
// read of the whole newest page can show that the harness rewrote its record,
// which the stream restates; a read of messages that began or changed only
// carries the stream on, since it never holds the conversation. A read of the
// newest messages also writes what the session's own report says of its turn,
// so the end of a turn is ordered with the messages it ended; a read further
// back leaves the turn alone.
func (h *HTTP) recordSession(session messagesSession, messages []fleettree.Message, isNewest, isWhole bool) ([]fleettree.Message, error) {
	if err := session.Identity.Check(); err != nil {
		return nil, err
	}
	held, err := h.Service.sessionStreamOf(session.Identity)
	if err != nil {
		return nil, err
	}
	projection, err := held.record(func(heldMessages []fleettree.Message, _ []sessionstream.Event) []sessionstream.Event {
		// Only a read of the newest page can show that the record was
		// rewritten: a page read further back is older than what the stream
		// holds even when the harness dropped nothing, and a read of what
		// changed is not the conversation at all.
		if isWhole && streamDiverges(heldMessages, messages) {
			// The harness rewrote its own record behind the reader, so the
			// stream restates what it holds rather than showing a message it
			// dropped.
			return []sessionstream.Event{fleettree.RestatedEvent(session.Identity, messages)}
		}
		return fleettree.StreamEvents(session.Identity, messages)
	})
	if err != nil || !isNewest {
		return projection, err
	}
	node, isTracked := h.Service.Store.nativeSession(session.Identity.Harness, session.Identity.Session)
	return held.record(func(heldMessages []fleettree.Message, events []sessionstream.Event) []sessionstream.Event {
		return turnEvents(session.Identity, heldMessages, events, turnState(node, isTracked, heldMessages))
	})
}

// streamDiverges reports whether a whole read of a session's newest messages
// disagrees with the conversation its stream holds. A stream only appends, so a
// harness that dropped messages, as pi's branch reset does, needs its record
// restated, and so does a read that shares no message with the stream, whose
// gap the stream would otherwise serve as the history before it. The two are
// compared from the message where one begins inside the other, so a newest
// page that slid past messages the stream holds, as it does once the session
// wrote more than a page, or a stream that began after the page did, only
// carries the stream on.
func streamDiverges(held, read []fleettree.Message) bool {
	if len(held) == 0 || len(read) == 0 {
		return false
	}
	isMessage := func(id string) func(fleettree.Message) bool {
		return func(message fleettree.Message) bool { return message.ID == id }
	}
	if at := slices.IndexFunc(held, isMessage(read[0].ID)); at >= 0 {
		return !leads(held[at:], read)
	}
	if at := slices.IndexFunc(read, isMessage(held[0].ID)); at >= 0 {
		return !leads(held, read[at:])
	}
	return true
}

// leads says held is how read begins: the same messages in the same order,
// none of them past read's newest, which a harness that dropped them no
// longer holds.
func leads(held, read []fleettree.Message) bool {
	if len(held) > len(read) {
		return false
	}
	for at := range held {
		if held[at].ID != read[at].ID || held[at].Seq != read[at].Seq {
			return false
		}
	}
	return true
}

// turnEvents are the events a session's own report of its turn adds to its
// stream: that the turn runs or ended, and the board's word where the harness
// reports nothing, which it draws rather than inventing a turn. A report is
// written where it changes the newest word the stream holds of the turn, and
// only there, so the board draws every change however often one turn makes
// it, as a turn a notice set working again after it ended does, and a report
// read again writes nothing. A turn whose prompt the stream does not hold is
// reported under no name rather than left unreported.
func turnEvents(id sessionstream.Identity, messages []fleettree.Message, events []sessionstream.Event, turn sessionTurn) []sessionstream.Event {
	prompt := fleettree.TurnPrompt(messages).ID
	kind := sessionstream.KindMissing
	switch {
	case !turn.Known:
	case turn.Ended:
		kind = sessionstream.KindTurnEnd
	default:
		kind = sessionstream.KindTurnState
	}
	word := turnWord(events, prompt)
	if word.Kind == kind && word.Missing == turn.Why {
		return nil
	}
	// The word a report changes is part of its id, so a turn that comes back
	// to a state it left says so again rather than being taken for the word it
	// wrote then.
	return []sessionstream.Event{{
		ID: sessionstream.EventID(id, kind, prompt, "", 0, turn.Why, word.ID), At: time.Now().UTC(),
		Kind: kind, Turn: prompt, Message: prompt, Missing: turn.Why,
	}}
}
