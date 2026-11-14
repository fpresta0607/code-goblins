package supervisor

import (
	"slices"
	"time"
)

// The CEO's work on the board is addressed to the home's primary CEO, not to
// one CEO process: closing the CEO and opening it again is a non-event. Every
// item the CEO put on the board carries the identity of the registration that
// put it there, which names a process, so when another CEO process registers
// in this home the items still waiting move to it, and deliveries to the CEO
// wait, queued, while no CEO runs. Only the home's registered CEO is ever
// followed: registering needs the home's session lock, and an item reaches
// the board only from the CEO its registration proves.

// followCEO addresses the home's CEO work still waiting to the CEO registered
// as identity: its open questions, its review items whose answer has not
// reached it, its run items not yet ended, and the queued deliveries to any
// of them.
func (s *Store) followCEO(identity string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.follow(identity)
}

// follow is followCEO for a caller that holds s.mu. An item whose files are
// stored under its identity keeps the one it was made under in made.
func (s *Store) follow(identity string) error {
	// An action follows only when the item it names moved, so moved names
	// items by kind and ID, not the identities they moved from.
	type item struct{ kind, id string }
	moved := map[item]bool{}
	changed := false
	follow := func(current, made *string) bool {
		if *current == identity {
			return false
		}
		if made != nil && *made == "" {
			*made = *current
		}
		*current, changed = identity, true
		return true
	}
	for i := range s.db.Questions {
		if q := &s.db.Questions[i]; q.Task == "" && (q.Status == "pending" || q.Status == "queued") && follow(&q.Identity, nil) {
			moved[item{"question", q.ID}] = true
		}
	}
	for i := range s.db.Reviews {
		if r := &s.db.Reviews[i]; r.Task == "" && (r.State == "open" || r.State == "answered" && !r.Delivered) && follow(&r.Identity, &r.Made) {
			moved[item{"review", r.ID}] = true
		}
	}
	for i := range s.db.Runs {
		if r := &s.db.Runs[i]; r.By == "ceo" && (r.State == "ready" || r.State == "running") && follow(&r.Identity, &r.Made) {
			moved[item{"run", r.ID}] = true
		}
	}
	for i := range s.db.Actions {
		a := &s.db.Actions[i]
		if a.Status != "queued" {
			continue
		}
		var named item
		switch a.Kind {
		case "ceo_answer", "question_clear", "answer_change":
			named = item{"question", a.QuestionID}
		case "review_answer", "review_clear":
			named = item{"review", a.ReviewID}
		case "run", "run_stop", "run_skip", "run_answer":
			named = item{"run", a.RunID}
		case "review":
			if a.CEOIdentity != "" {
				follow(&a.CEOIdentity, nil)
			}
		}
		if moved[named] {
			a.Generation = identity
		}
	}
	if !changed {
		return nil
	}
	return s.save()
}

// madeUnder is the identity an item's files are stored under: the one it was
// made under, which made keeps once the item has followed the CEO.
func madeUnder(made, identity string) string {
	if made != "" {
		return made
	}
	return identity
}

// liveCEO reads once whether the home's CEO is registered and running, so a
// delivery to it can be made now, and the identity it registered with.
func (s *Store) liveCEO() (string, bool) {
	ceo := readCEOState(s.Home.State)
	return ceo.identity, ceo.registered && ceo.problem == ""
}

func (s *Store) ceoLive() bool {
	_, live := s.liveCEO()
	return live
}

// waitsForCEO reports whether a queued action is a delivery to the CEO, which
// waits while no CEO runs instead of failing. The caller holds s.mu.
func (s *Store) waitsForCEO(a Action) bool {
	switch a.Kind {
	case "ceo_answer":
		return true
	case "message":
		return a.TaskID == ""
	case "review":
		return a.CEOIdentity != ""
	case "review_answer":
		i := slices.IndexFunc(s.db.Reviews, func(r Review) bool { return r.ID == a.ReviewID })
		return i >= 0 && s.db.Reviews[i].Task == ""
	case "answer_change":
		i := slices.IndexFunc(s.db.Questions, func(q Question) bool { return q.ID == a.QuestionID })
		return i >= 0 && (s.db.Questions[i].Task == "" || !isAsker(s.Home.State, s.db.Questions[i]))
	}
	return false
}

// answerBatchWindow is how long an answer to one of the CEO's questions waits
// for the Overlord's next one while another of them still waits on him, so
// the answers he gives in one go reach the CEO as one message. He answers a
// stack of them 2 to 4 s apart: the eleven gaps in the seven such sittings the
// live home still held from 2026-10-01 to 2026-10-06 ran from 2.0 s to 3.9 s.
// On 2026-10-09 four answers he gave within 7 s were typed into the CEO as
// four messages in the middle of a turn.
const answerBatchWindow = 5 * time.Second

// answersTogether is the queued answers to the CEO's questions that go to it
// as one message, by index and in the order he gave them: every one from the
// first up to whatever else he queued for the CEO after it, such as a message
// of his own, which keeps its place among his answers. isClosed says such a
// delivery follows them, so no later answer can join them. The caller holds
// s.mu.
func (s *Store) answersTogether() (together []int, isClosed bool) {
	for i, a := range s.db.Actions {
		switch {
		case a.Status != "queued":
		case a.Kind == "ceo_answer":
			if len(together) == 0 || a.Generation == s.db.Actions[together[0]].Generation {
				together = append(together, i)
			}
		case len(together) > 0 && s.waitsForCEO(a):
			return together, true
		}
	}
	return together, false
}

// answersWait is how long, at now, the queued answers to the CEO's questions
// still wait for the Overlord's next one: until answerBatchWindow after the
// newest of them, and only while his next answer could still join them, so
// while another question of the CEO's waits on him and he has queued nothing
// else for the CEO behind them. Otherwise there is nothing to wait for, so an
// answer to the only question goes at once, and so does the last answer of a
// stack. A clock that stepped back never lengthens the wait. The caller holds
// s.mu.
func (s *Store) answersWait(now time.Time) time.Duration {
	together, isClosed := s.answersTogether()
	isAnotherWaiting := slices.ContainsFunc(s.db.Questions, func(q Question) bool {
		return q.Task == "" && q.Status == "pending" && q.AnswerID == ""
	})
	if len(together) == 0 || isClosed || !isAnotherWaiting {
		return 0
	}
	given := now.Sub(s.db.Actions[together[len(together)-1]].CreatedAt)
	if given < 0 || given >= answerBatchWindow {
		return 0
	}
	return answerBatchWindow - given
}

// ceoMessageUndo is how long a message of his to the CEO waits on the board
// before it is typed into the CEO's terminal, so Edit and Cancel can still take
// it back: once typed into a turn it is the harness's to take. He sees it
// held, then typed, then taken (the CEO, 2026-11-02). It is short because the
// CEO's turns are long and his word reaches the CEO promptly (2026-10-28).
// Send now skips it, and a command never waits in it.
const ceoMessageUndo = 10 * time.Second

// undoWait is how long, at now, queued message a still waits for its undo
// window to end. A clock that stepped back never lengthens the wait.
func undoWait(a Action, now time.Time) time.Duration {
	if a.Kind != "message" || a.TaskID != "" || a.Status != "queued" || a.IsSentNow || isHarnessCommand(a.Text) {
		return 0
	}
	given := now.Sub(a.CreatedAt)
	if given < 0 || given >= ceoMessageUndo {
		return 0
	}
	return ceoMessageUndo - given
}

// heldDueIn is how long until the first action held for a moment can go: the
// answers held for the Overlord's next one, or a message to the CEO in its
// undo window. It is zero when none is held.
func (s *Store) heldDueIn() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	soonest := s.answersWait(now)
	for _, a := range s.db.Actions {
		if wait := undoWait(a, now); wait > 0 && (soonest == 0 || wait < soonest) {
			soonest = wait
		}
	}
	return soonest
}

// nextQueued is the index of the queued action that can run first now, or
// -1. With no CEO running, deliveries to the CEO wait, but for his message,
// whose delivery starts the CEO at once (message_starts.go), so it reads
// Starting from the first; an answer to one of its questions waits while the
// Overlord's next one may follow it, and a message to a CEO that runs waits
// out its undo window. Every delivery goes ahead of the board's
// own evaluations, which batches of reconciles queue fifteen at a time: on
// 2026-11-02 his message waited 93 s behind one such batch. The caller holds
// s.mu.
func (s *Store) nextQueued(ceoLive bool) int {
	now := time.Now()
	isHeld := s.answersWait(now) > 0
	first := -1
	for i, a := range s.db.Actions {
		if a.Status == "queued" && !s.deferredUntil[a.ID].After(now) && (ceoLive || !s.waitsForCEO(a) || a.Kind == "message") && !(isHeld && a.Kind == "ceo_answer") && (!ceoLive || undoWait(a, now) == 0) && !s.followsHeldMessage(i, now) && !s.followsEarlierAnswer(i) && !s.followsEarlierMessage(i) {
			if a.Kind != "evaluate" {
				return i
			}
			if first < 0 {
				first = i
			}
		}
	}
	return first
}

// followsHeldMessage reports whether action i is a delivery to the CEO that
// waits behind an earlier message of his to it still in its undo window, so
// the CEO gets what he sends in the order he sent it, unless he asked for this
// one now. The caller holds s.mu.
func (s *Store) followsHeldMessage(i int, now time.Time) bool {
	a := s.db.Actions[i]
	return !a.IsSentNow && s.waitsForCEO(a) && slices.ContainsFunc(s.db.Actions[:i], func(earlier Action) bool { return undoWait(earlier, now) > 0 })
}

// followsEarlierAnswer reports whether action i is his change of an answer
// that waits for an earlier answer or change to the same question, one still
// queued or running and not yet handed over, so whoever acts on them gets
// them in the order he gave them. The caller holds s.mu.
func (s *Store) followsEarlierAnswer(i int) bool {
	a := s.db.Actions[i]
	return a.Kind == "answer_change" && slices.ContainsFunc(s.db.Actions[:i], func(earlier Action) bool {
		return earlier.QuestionID == a.QuestionID && (earlier.Kind == "ceo_answer" || earlier.Kind == "goblin_answer" || earlier.Kind == "answer_change") &&
			(earlier.Status == "queued" || earlier.Status == "running" && earlier.Awaiting == nil)
	})
}

// followsEarlierMessage reports whether action i is a message that waits for
// an earlier message to the same session, one still queued or running and
// not yet handed over, so its harness takes each alone and in the order he
// sent them, unless he asked for this one now. The caller holds s.mu.
func (s *Store) followsEarlierMessage(i int) bool {
	a := s.db.Actions[i]
	return a.Kind == "message" && !a.IsSentNow && slices.ContainsFunc(s.db.Actions[:i], func(earlier Action) bool {
		return earlier.Kind == "message" && earlier.TaskID == a.TaskID && (earlier.Status == "queued" || earlier.Status == "running" && earlier.Awaiting == nil)
	})
}

// waitBehind gives each queued message to action i's session after it, which
// waits behind it, what it waits on and whether he may send it now, unless he
// asked for one now already; a command takes words of its own, with Send now
// where commandSendsNow says that session's harness can run it at once, and
// keeps those its own spent Send now gives it. The caller holds s.mu.
func (s *Store) waitBehind(i int, commandSendsNow bool) {
	held := s.db.Actions[i]
	for j := i + 1; j < len(s.db.Actions); j++ {
		if later := &s.db.Actions[j]; later.Kind == "message" && later.TaskID == held.TaskID && later.Status == "queued" && !later.IsSentNow {
			later.Message, later.CanSendNow, later.SendNowWhy = held.Message, held.CanSendNow, held.SendNowWhy
			later.holdCommand(commandSendsNow)
		}
	}
}

// HasRunnable reports whether a queued action can run now.
func (s *Store) HasRunnable() bool {
	live := s.ceoLive()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextQueued(live) >= 0
}
