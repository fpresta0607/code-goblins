package supervisor

import (
	"slices"
	"time"
)

// The CFO's work on the board is addressed to the home's primary CFO, not to
// one CFO process: closing the CFO and opening it again is a non-event. Every
// item the CFO put on the board carries the identity of the registration that
// put it there, which names a process, so when another CFO process registers
// in this home the items still waiting move to it, and deliveries to the CFO
// wait, queued, while no CFO runs. Only the home's registered CFO is ever
// followed: registering needs the home's session lock, and an item reaches
// the board only from the CFO its registration proves.

// followCFO addresses the home's CFO work still waiting to the CFO registered
// as identity: its open questions, its review items whose answer has not
// reached it, its run items not yet ended, and the queued deliveries to any
// of them.
func (s *Store) followCFO(identity string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.follow(identity)
}

// follow is followCFO for a caller that holds s.mu. An item whose files are
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
		if r := &s.db.Runs[i]; r.By == "cfo" && (r.State == "ready" || r.State == "running") && follow(&r.Identity, &r.Made) {
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
		case "cfo_answer", "question_clear":
			named = item{"question", a.QuestionID}
		case "review_answer", "review_clear":
			named = item{"review", a.ReviewID}
		case "run", "run_stop":
			named = item{"run", a.RunID}
		case "review":
			if a.CFOIdentity != "" {
				follow(&a.CFOIdentity, nil)
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
// made under, which made keeps once the item has followed the CFO.
func madeUnder(made, identity string) string {
	if made != "" {
		return made
	}
	return identity
}

// liveCFO reads once whether the home's CFO is registered and running, so a
// delivery to it can be made now, and the identity it registered with.
func (s *Store) liveCFO() (string, bool) {
	cfo := readCFOState(s.Home.State)
	return cfo.identity, cfo.registered && cfo.problem == ""
}

func (s *Store) cfoLive() bool {
	_, live := s.liveCFO()
	return live
}

// waitsForCFO reports whether a queued action is a delivery to the CFO, which
// waits while no CFO runs instead of failing. The caller holds s.mu.
func (s *Store) waitsForCFO(a Action) bool {
	switch a.Kind {
	case "cfo_answer":
		return true
	case "message":
		return a.TaskID == ""
	case "review":
		return a.CFOIdentity != ""
	case "review_answer":
		i := slices.IndexFunc(s.db.Reviews, func(r Review) bool { return r.ID == a.ReviewID })
		return i >= 0 && s.db.Reviews[i].Task == ""
	}
	return false
}

// answerBatchWindow is how long an answer to one of the CFO's questions waits
// for the Overlord's next one while another of them still waits on him, so
// the answers he gives in one go reach the CFO as one message. He answers a
// stack of them 2 to 4 s apart: the eleven gaps in the seven such sittings the
// live home still held from 2026-10-01 to 2026-10-06 ran from 2.0 s to 3.9 s.
// On 2026-10-09 four answers he gave within 7 s were typed into the CFO as
// four messages in the middle of a turn.
const answerBatchWindow = 5 * time.Second

// answersTogether is the queued answers to the CFO's questions that go to it
// as one message, by index and in the order he gave them: every one from the
// first up to whatever else he queued for the CFO after it, such as a message
// of his own, which keeps its place among his answers. isClosed says such a
// delivery follows them, so no later answer can join them. The caller holds
// s.mu.
func (s *Store) answersTogether() (together []int, isClosed bool) {
	for i, a := range s.db.Actions {
		switch {
		case a.Status != "queued":
		case a.Kind == "cfo_answer":
			if len(together) == 0 || a.Generation == s.db.Actions[together[0]].Generation {
				together = append(together, i)
			}
		case len(together) > 0 && s.waitsForCFO(a):
			return together, true
		}
	}
	return together, false
}

// answersWait is how long, at now, the queued answers to the CFO's questions
// still wait for the Overlord's next one: until answerBatchWindow after the
// newest of them, and only while his next answer could still join them, so
// while another question of the CFO's waits on him and he has queued nothing
// else for the CFO behind them. Otherwise there is nothing to wait for, so an
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

// answersDueIn is how long until the answers held for the Overlord's next one
// can go, and zero when none is held.
func (s *Store) answersDueIn() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.answersWait(time.Now())
}

// nextQueued is the index of the first queued action that can run now, or -1.
// With no CFO running, deliveries to the CFO wait, and an answer to one of its
// questions waits while the Overlord's next one may follow it. The caller
// holds s.mu.
func (s *Store) nextQueued(cfoLive bool) int {
	now := time.Now()
	isHeld := s.answersWait(now) > 0
	return slices.IndexFunc(s.db.Actions, func(a Action) bool {
		return a.Status == "queued" && !s.deferredUntil[a.ID].After(now) && (cfoLive || !s.waitsForCFO(a)) && !(isHeld && a.Kind == "cfo_answer")
	})
}

// HasRunnable reports whether a queued action can run now.
func (s *Store) HasRunnable() bool {
	live := s.cfoLive()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextQueued(live) >= 0
}
