package supervisor

import (
	"errors"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/spawn"
)

// A message submitted into a turn under way waits in its harness's own queue
// until that turn takes it. The Overlord, 2026-11-10, of three such messages
// that read Sent for minutes: it "must read Queued, be editable and deletable,
// and Send now must interrupt and deliver it" (#2332). The board acts on that
// queue with the harness's own keys, and only after its screen shows the
// state each key needs.

// queueKeyWait bounds how long the board waits to see a harness's queue on
// its prompt line once the key that takes it back went.
var queueKeyWait = 5 * time.Second

// queueKeyPoll spaces the board's looks at the screen during that wait.
const queueKeyPoll = 50 * time.Millisecond

// isHarnessHeld says delivery a was submitted into a turn under way that has
// not been seen taking it, so its harness holds it in its own queue.
func isHarnessHeld(a Action) bool {
	return a.Status == "running" && a.Awaiting != nil && a.Awaiting.IsHeld
}

// whoHolds names the session message a waits in, within a sentence and at
// the start of one.
func whoHolds(task string) (string, string) {
	if task == "" {
		return "the CEO", "The CEO"
	}
	return task, task
}

// heldWith returns message id to task while its harness holds it in its
// queue, among every delivery the board typed into that queue, in the order
// they were typed; none when no harness holds that message.
func (s *Store) heldWith(id, task string) []Action {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := slices.IndexFunc(s.db.Actions, func(a Action) bool {
		return a.ID == id && a.TaskID == task && a.Kind == "message" && isHarnessHeld(a)
	})
	if at < 0 {
		return nil
	}
	target := *s.db.Actions[at].Awaiting
	var held []Action
	for _, a := range s.db.Actions {
		if isHarnessHeld(a) && a.Awaiting.Host == target.Host && a.Awaiting.Task == target.Task && a.Awaiting.Generation == target.Generation {
			held = append(held, a)
		}
	}
	slices.SortStableFunc(held, func(a, b Action) int { return a.Awaiting.Since.Compare(b.Awaiting.Since) })
	return held
}

// setAside marks each of held, deliveries their harness holds in its queue,
// as one the board is trying, awaited no longer, while the board takes that
// queue back onto its prompt line: nothing else then submits, takes back or
// settles them, and a later message to their session waits behind them. It
// reports false, and changes nothing, when one of them is held no longer or
// the worker runs a delivery to their session now, which may be typing onto
// that prompt line or have typed a line it has yet to record.
func (s *Store) setAside(held []Action) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	places := make([]int, len(held))
	for i, each := range held {
		places[i] = slices.IndexFunc(s.db.Actions, func(a Action) bool { return a.ID == each.ID && a.TaskID == each.TaskID && isHarnessHeld(a) })
		if places[i] < 0 {
			return false
		}
	}
	task := held[0].Awaiting.Task
	if slices.ContainsFunc(s.db.Actions, func(a Action) bool {
		isForSession := a.TaskID == task && a.Kind != "evaluate"
		if task == "" {
			isForSession = s.waitsForCEO(a)
		}
		return isForSession && slices.Contains(s.trying, a.ID)
	}) {
		return false
	}
	now := time.Now().UTC()
	for _, at := range places {
		s.db.Actions[at].Awaiting, s.db.Actions[at].UpdatedAt = nil, now
	}
	return s.save() == nil
}

// settleSetAside ends what setAside began for each of held still set aside,
// as settle says of the delivery it is given as setAside found it.
func (s *Store) settleSetAside(held []Action, settle func(a *Action, was Action)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for _, each := range held {
		at := slices.IndexFunc(s.db.Actions, func(a Action) bool {
			return a.ID == each.ID && a.TaskID == each.TaskID && a.Status == "running" && a.Awaiting == nil
		})
		if at < 0 {
			continue
		}
		settle(&s.db.Actions[at], each)
		s.db.Actions[at].UpdatedAt = now
	}
	s.updateQuestionOutcomes()
	return s.save()
}

// withdrawTakenBack records that the queue held waited in was taken back off
// its harness's prompt line unsubmitted: the message withdraw names is
// deleted, as one the board still held is, and every other waits on the
// board again, to be typed into its session in the order he sent them.
func (s *Store) withdrawTakenBack(withdraw Action, held []Action) (Action, error) {
	if err := s.settleSetAside(held, func(a *Action, _ Action) {
		a.Status, a.CanSendNow, a.SendNowWhy = "queued", true, ""
		a.Message = "Queued. It left its session's queue with the message he took back, and is typed there again."
	}); err != nil {
		return Action{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	withdrawn, err := s.queue(withdraw)
	if err != nil {
		return Action{}, err
	}
	if at := slices.IndexFunc(s.db.Actions, func(a Action) bool { return a.ID == withdrawn.ID && a.Kind == "message_withdraw" }); at >= 0 {
		s.db.Actions[at].Message = "Taken back out of its session's queue unsent."
		withdrawn = s.db.Actions[at]
	}
	return withdrawn, s.save()
}

// sentHeldNow records that the send-now key of the harness that holds
// message id to task went, as of now, so Send now may be asked of it again
// should its harness still hold it.
func (s *Store) sentHeldNow(id, task string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at := slices.IndexFunc(s.db.Actions, func(a Action) bool { return a.ID == id && a.TaskID == task && isHarnessHeld(a) }); at >= 0 {
		s.db.Actions[at].Message, s.db.Actions[at].UpdatedAt = sentNowInTurn, time.Now().UTC()
	}
	return s.save()
}

// sendHeldNow presses the send-now key of the harness that holds message id
// to task in its queue, which interrupts its turn and has it take what that
// queue holds at once. It reports false when no harness holds the message,
// which Send now then asks for where the board holds it. The key goes only
// to a turn under way over an empty prompt line that, where its harness says
// so, still offers its queue: a harness that is taking the message, or has,
// is left alone, since the key would then interrupt the turn that reads it.
func (s *Service) sendHeldNow(id, task string) (bool, error) {
	held := s.Store.heldWith(id, task)
	at := slices.IndexFunc(held, func(a Action) bool { return a.ID == id })
	if at < 0 {
		return false, nil
	}
	waiting := *held[at].Awaiting
	_, subject := whoHolds(task)
	record, screens, isLive := s.deliveryTerminal(waiting)
	switch {
	case !isLive:
		return true, errors.New(subject + "'s terminal it was typed into has ended or started again, so its send-now key was not pressed.")
	case record.Stream:
		return true, errors.New(subject + " runs on stream-json pipes, where Claude Code holds the message itself. It takes it at its next tool call, or as its turn ends.")
	case screens.SendNow == "":
		return true, errors.New(subject + "'s harness has no send-now key. It takes the message at its next tool call, or as its turn ends.")
	}
	if waiting.Host != "" && s.Options.CEO != nil {
		s.Options.CEO.typing.Lock()
		defer s.Options.CEO.typing.Unlock()
	}
	if s.lookAtTerminal(waiting).Took {
		return true, nil
	}
	rows, err := host.ReadScreen(record)
	switch {
	case err != nil:
		return true, errors.New(subject + "'s screen cannot be read, so its send-now key was not pressed.")
	case !screens.IsWorking(rows):
		// No turn is left to interrupt: its harness takes the message as
		// the next prompt, or handed it back for the board to submit.
		return true, nil
	case screens.IsOpen(rows) || !screens.ComposerEmpty(rows):
		return true, errors.New(subject + "'s prompt line is not empty and ready, so its send-now key was not pressed. Press again in a moment.")
	case screens.Queued != nil && !screens.OffersQueue(rows):
		return true, nil
	}
	client, err := host.Dial(record)
	if err != nil {
		return true, errors.New(subject + "'s terminal does not answer, so its send-now key was not pressed.")
	}
	defer client.Close()
	if err := spawn.PressKeys(client.Input, screens.SendNow); err != nil {
		return true, errors.New(subject + "'s send-now key could not be pressed.")
	}
	return true, s.Store.sentHeldNow(id, task)
}

// takeBackHeld takes the message withdraw names back out of the queue of the
// harness that holds it, for Edit and Cancel, and reports false when no
// harness holds it, which the board then withdraws where it holds it. A
// harness gives its queue back only whole: Claude Code's Up puts every queued
// line on the prompt line, a row each. So the board presses that key where
// the empty prompt line offers it, waits to read there exactly the lines it
// typed into that queue, takes them off unsubmitted, deletes the one he named
// and types the others again in the order he sent them. Nothing is cleared
// that the board did not type: a queue that also held a line typed in the
// terminal is left on the prompt line for him, with the board's lines watched
// there as lines left typed are. A message its turn already took is in no
// queue, and his press can land before the board's next look at its session's
// record, so the board looks first: that message then reads delivered on
// every board, the press says its session took it, and no key goes to a
// session for a queue it no longer holds (#1915).
func (s *Service) takeBackHeld(withdraw Action) (Action, bool, error) {
	if len(s.Store.heldWith(withdraw.Target, withdraw.TaskID)) > 0 {
		if err := s.Store.settleDeliveries(time.Now().UTC(), s.lookAtTerminal); err != nil {
			return Action{}, true, err
		}
		s.notify()
	}
	held := s.Store.heldWith(withdraw.Target, withdraw.TaskID)
	if len(held) == 0 {
		return Action{}, false, nil
	}
	first := *held[0].Awaiting
	who, subject := whoHolds(withdraw.TaskID)
	record, screens, isLive := s.deliveryTerminal(first)
	switch {
	case !isLive:
		return Action{}, true, errors.New(subject + "'s terminal does not answer, so its queue was left as it is.")
	case record.Stream:
		return Action{}, true, errors.New("It waits in " + who + "'s own queue on stream-json pipes, which Claude Code gives back to nothing. It takes it at its next tool call, or as its turn ends.")
	case screens.EditQueue == "":
		return Action{}, true, errors.New("It waits in " + who + "'s own queue, which its harness gives back to no key. Send now has it taken at once.")
	case slices.ContainsFunc(held, func(a Action) bool { return a.Kind != "message" }):
		return Action{}, true, errors.New(subject + "'s queue also holds an answer the board typed, so this message cannot be taken back alone. Send now has it taken at once.")
	}
	if first.Host != "" && s.Options.CEO != nil {
		s.Options.CEO.typing.Lock()
		defer s.Options.CEO.typing.Unlock()
	}
	read := func() ([]string, error) { return host.ReadScreen(record) }
	rows, err := read()
	if err != nil || !screens.OffersQueue(rows) {
		return Action{}, true, errors.New(subject + "'s prompt line does not offer its queue now, so nothing was changed. Press again in a moment.")
	}
	client, err := host.Dial(record)
	if err != nil {
		return Action{}, true, errors.New(subject + "'s terminal does not answer, so its queue was left as it is.")
	}
	defer client.Close()
	if !s.Store.setAside(held) {
		return Action{}, true, errors.New("The board is typing for " + who + " right now. Press again in a moment.")
	}
	restore := func(a *Action, was Action) { a.Awaiting = was.Awaiting }
	if err := client.Input([]byte(screens.EditQueue)); err != nil {
		return Action{}, true, errors.Join(errors.New(subject+"'s terminal took no key, so its queue was left as it is."), s.Store.settleSetAside(held, restore))
	}
	typed, words := make([]Awaiting, len(held)), ""
	for i, a := range held {
		typed[i] = *a.Awaiting
		words += a.Awaiting.Typed
	}
	isBack := false
	for deadline := time.Now().Add(queueKeyWait); !isBack && time.Now().Before(deadline); time.Sleep(queueKeyPoll) {
		if rows, err = read(); err == nil {
			at, count := promptLineHolds(screens, rows, typed)
			isBack = at == 0 && count == len(typed)
		}
	}
	// A prompt line that shows no words took nothing off the queue, which its
	// turn then has, or took meanwhile.
	if !isBack && (err != nil || !screens.ComposerHoldsText(rows)) {
		return Action{}, true, errors.Join(errors.New(subject+"'s harness did not give its queue back, so nothing was changed."), s.Store.settleSetAside(held, restore))
	}
	if !isBack || !spawn.TakeBack(read, client.Input, screens, words) {
		why := "It left " + who + "'s queue for its prompt line, which holds words the board cannot take back alone, so it waits there unsent. Send or clear that line in its terminal."
		return Action{}, true, errors.Join(errors.New(why), s.Store.settleSetAside(held, func(a *Action, was Action) {
			a.Status, a.Awaiting, a.LeftTyped, a.Message = "uncertain", was.Awaiting, was.Awaiting, why
		}))
	}
	withdrawn, err := s.Store.withdrawTakenBack(withdraw, held)
	return withdrawn, true, err
}
