package supervisor

import "slices"

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
		case "run":
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
	case "review":
		return a.CFOIdentity != ""
	case "review_answer":
		i := slices.IndexFunc(s.db.Reviews, func(r Review) bool { return r.ID == a.ReviewID })
		return i >= 0 && s.db.Reviews[i].Task == ""
	}
	return false
}

// nextQueued is the index of the first queued action that can run now, or -1.
// With no CFO running, deliveries to the CFO wait. The caller holds s.mu.
func (s *Store) nextQueued(cfoLive bool) int {
	return slices.IndexFunc(s.db.Actions, func(a Action) bool {
		return a.Status == "queued" && (cfoLive || !s.waitsForCFO(a))
	})
}

// HasRunnable reports whether a queued action can run now.
func (s *Store) HasRunnable() bool {
	live := s.cfoLive()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextQueued(live) >= 0
}
