package supervisor

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
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

// isHeldWith says delivery a waits in the queue of the harness that typed, a
// delivery the board typed, went into.
func isHeldWith(a Action, typed Awaiting) bool {
	return isHarnessHeld(a) && a.Awaiting.Host == typed.Host && a.Awaiting.Task == typed.Task && a.Awaiting.Generation == typed.Generation
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
	return s.heldBeside(*s.db.Actions[at].Awaiting)
}

// heldBeside returns every delivery the board typed into the queue that holds
// typed, in the order they were typed. The caller holds s.mu.
func (s *Store) heldBeside(typed Awaiting) []Action {
	var held []Action
	for _, a := range s.db.Actions {
		if isHeldWith(a, typed) {
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
// reports false, and changes nothing, when one of them is held no longer,
// that queue holds another the board typed there since they were read, which
// would come back onto the prompt line with them, or the worker runs a
// delivery to their session now, which may be typing onto that prompt line or
// have typed a line it has yet to record.
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
	if slices.ContainsFunc(s.db.Actions, func(a Action) bool {
		return isHeldWith(a, *held[0].Awaiting) && !slices.ContainsFunc(held, func(each Action) bool { return each.ID == a.ID })
	}) {
		return false
	}
	task := held[0].Awaiting.Task
	if slices.ContainsFunc(s.db.Actions, func(a Action) bool { return s.isDeliveryTo(a, task) && slices.Contains(s.trying, a.ID) }) {
		return false
	}
	now := time.Now().UTC()
	for _, at := range places {
		s.db.Actions[at].Awaiting, s.db.Actions[at].UpdatedAt = nil, now
	}
	return s.save() == nil
}

// isDeliveryTo says action a goes to task's session: one waitsForCEO names
// for the CEO's, and any of its task's but an evaluation for a goblin's. The
// caller holds s.mu.
func (s *Store) isDeliveryTo(a Action, task string) bool {
	if task == "" {
		return s.waitsForCEO(a)
	}
	return a.TaskID == task && a.Kind != "evaluate"
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
// harness gives its queue back only whole, so the board takes it all back
// (takeQueueBack), deletes the one he named and types the others again in
// the order he sent them. A message its turn already took is in no queue,
// and his press can land before the board's next look at its session's
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
	if err := s.takeQueueBack(held, withdraw.TaskID, record, screens); err != nil {
		return Action{}, true, err
	}
	withdrawn, err := s.Store.withdrawTakenBack(withdraw, held)
	return withdrawn, true, err
}

// takeQueueBack takes held, every delivery the board typed into the queue of
// the harness that runs in record and draws screens, back off its prompt line
// unsubmitted, and leaves them set aside for its caller to settle. A harness
// gives its queue back only whole: Claude Code's Up puts every queued line on
// the prompt line, a row each, and pi's Alt+Q every one in its editor, an
// empty row between each two. So the board presses that key where the empty
// prompt line offers it and would show every row the key puts there, which
// pi's editor, showing only its last rows, may not, waits to read there
// exactly the lines it typed into that queue and takes them off. Nothing is
// cleared that the board did not type: a queue that also held a line typed in
// the terminal is left on the prompt line for him, with the board's lines
// watched there as lines left typed are. For a queue of the CEO's, its caller
// holds the CEO's typing lock.
func (s *Service) takeQueueBack(held []Action, task string, record host.Record, screens harness.Screens) error {
	who, subject := whoHolds(task)
	read := func() ([]string, error) { return host.ReadScreen(record) }
	typed, lines, words := make([]Awaiting, len(held)), make([]string, len(held)), ""
	for i, a := range held {
		typed[i], lines[i] = *a.Awaiting, a.Awaiting.Typed
		words += a.Awaiting.Typed
	}
	rows, err := read()
	switch {
	case err != nil || !screens.OffersQueue(rows):
		return errors.New(subject + "'s prompt line does not offer its queue now, so nothing was changed. Press again in a moment.")
	case !screens.QueueFits(rows, lines):
		return errors.New(subject + "'s editor is too short to show its queue whole, so it cannot be taken back. It takes it at its next tool call, or as its turn ends.")
	}
	client, err := host.Dial(record)
	if err != nil {
		return errors.New(subject + "'s terminal does not answer, so its queue was left as it is.")
	}
	defer client.Close()
	if !s.Store.setAside(held) {
		return errors.New("The board is typing for " + who + " right now. Press again in a moment.")
	}
	restore := func(a *Action, was Action) { a.Awaiting = was.Awaiting }
	if err := client.Input([]byte(screens.EditQueue)); err != nil {
		return errors.Join(errors.New(subject+"'s terminal took no key, so its queue was left as it is."), s.Store.settleSetAside(held, restore))
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
		return errors.Join(errors.New(subject+"'s harness did not give its queue back, so nothing was changed."), s.Store.settleSetAside(held, restore))
	}
	if !isBack || !spawn.TakeBack(read, client.Input, screens, words) {
		why := "It left " + who + "'s queue for its prompt line, which holds words the board cannot take back alone, so it waits there unsent. Send or clear that line in its terminal."
		return errors.Join(errors.New(why), s.Store.settleSetAside(held, func(a *Action, was Action) {
			a.Status, a.Awaiting, a.LeftTyped, a.Message = "uncertain", was.Awaiting, was.Awaiting, why
		}))
	}
	return nil
}

// heldIn returns every delivery the board typed into the queue of the
// harness of task's session that it still holds there, in the order they
// were typed; none when it holds none: an answer held there alone is in it
// as his words are (#2759).
func (s *Store) heldIn(task string) []Action {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := slices.IndexFunc(s.db.Actions, func(a Action) bool { return s.isDeliveryTo(a, task) && isHarnessHeld(a) })
	if at < 0 {
		return nil
	}
	return s.heldBeside(*s.db.Actions[at].Awaiting)
}

// holdsUnrecorded says the harness of task's session may hold in its queue a
// line typed there that no action records, which it would give back with the
// board's: a notice told to the CEO in a turn under way, or a message goblins
// send typed into a goblin's.
func (s *Store) holdsUnrecorded(task string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if task == "" {
		return len(s.db.HeldNotices) > 0
	}
	return slices.ContainsFunc(s.db.QueuedSends, func(a BoardActivity) bool { return a.TaskID == task })
}

// queueAhead returns what the harness of task's session holds in its queue
// of what the board typed there, the terminal it runs in and the screens it
// draws, and whether a command he sends now can go ahead of it: its send-now
// key takes that queue from the head, so a command behind his words there
// waits out the whole turn they start, as /login did on Claude Code 2.1.328,
// live on 2026-11-19 (#2759). It can only where that harness gives its queue
// back to a key and holds there only his messages, which then wait on the
// board behind the command, and no line that no action records, which that
// key would give back with them. Nor can it where the prompt line that offers
// that queue would not show every row the key puts there, as pi's editor
// shows only its last rows, since the board could not take them back off it
// (takeQueueBack). A queue in a terminal that has ended holds nothing.
func (s *Service) queueAhead(task string) ([]Action, host.Record, harness.Screens, bool) {
	if s.Store.holdsUnrecorded(task) {
		return nil, host.Record{}, harness.Screens{}, false
	}
	held := s.Store.heldIn(task)
	if len(held) == 0 {
		return nil, host.Record{}, harness.Screens{}, true
	}
	record, screens, isLive := s.deliveryTerminal(*held[0].Awaiting)
	if !isLive {
		return nil, host.Record{}, harness.Screens{}, true
	}
	canGoAhead := !record.Stream && screens.EditQueue != "" && !slices.ContainsFunc(held, func(a Action) bool { return a.Kind != "message" })
	if canGoAhead && screens.ComposerScrolls {
		lines := make([]string, len(held))
		for i, a := range held {
			lines[i] = a.Awaiting.Typed
		}
		if rows, err := host.ReadScreen(record); err == nil && screens.OffersQueue(rows) && !screens.QueueFits(rows, lines) {
			canGoAhead = false
		}
	}
	return held, record, screens, canGoAhead
}

// sendCommandFirst readies command id to task, which he sends now, to go
// ahead of what its session's harness holds in its queue of what the board
// typed there, and returns why it cannot where it cannot (queueAhead). It
// takes that queue back (takeQueueBack), sends the command now, and reports
// that it did, and his words wait on the board behind the command
// (waitsForCommandSentNow), to be typed into the turn again once it has gone.
// No key goes for a command that cannot go ahead of them. A delivery to that
// session the worker tries meanwhile can be typing into that queue a line the
// board records there only once its try ends, as his words to the CEO are, so
// the press reads the queue once no such try runs, waiting up to tryWait for
// one under way, and otherwise says to press again in a moment. As
// takeBackHeld does, the board then looks first, since the turn may have
// taken them, or a notice told to the CEO, since its last look.
func (s *Service) sendCommandFirst(ctx context.Context, id, task string) (bool, error) {
	if !s.Store.isCommandToSendNow(id, task) {
		return false, nil
	}
	if !s.Store.awaitTries(ctx, func(a Action) bool { return s.Store.isDeliveryTo(a, task) }) {
		who, _ := whoHolds(task)
		return false, errors.New("The board is typing for " + who + " right now. Press again in a moment.")
	}
	if len(s.Store.heldIn(task)) > 0 || s.Store.holdsUnrecorded(task) {
		if err := s.Store.settleDeliveries(time.Now().UTC(), s.lookAtTerminal); err != nil {
			return false, err
		}
		s.notify()
	}
	held, record, screens, canGoAhead := s.queueAhead(task)
	if !canGoAhead {
		// One that reads Not sent goes back in the queue, as Send now puts back
		// any message that reads Not sent, but to run there when the turn ends
		// and not sent now, since the harness's send-now key would end that
		// turn over the words ahead of it, as Codex's Escape hands them back
		// onto its prompt line.
		var saveErr error
		s.Store.mu.Lock()
		if at := slices.IndexFunc(s.Store.db.Actions, func(a Action) bool { return a.ID == id && a.TaskID == task && isNotSent(a) }); at >= 0 {
			a := &s.Store.db.Actions[at]
			a.Status, a.Message, a.IsSentNow, a.CanSendNow, a.SendNowWhy, a.Awaiting, a.LeftTyped, a.UpdatedAt = "queued", "Queued. "+commandWaits, false, false, commandWaits, nil, nil, time.Now().UTC()
			saveErr = s.Store.save()
		}
		s.Store.mu.Unlock()
		return false, errors.Join(errors.New(commandWaits), saveErr)
	}
	if len(held) == 0 {
		return false, nil
	}
	if first := *held[0].Awaiting; first.Host != "" && s.Options.CEO != nil {
		s.Options.CEO.typing.Lock()
		defer s.Options.CEO.typing.Unlock()
	}
	if err := s.takeQueueBack(held, task, record, screens); err != nil {
		return false, err
	}
	// The command is sent now before his words wait on the board again, so
	// the worker never types them back into that queue ahead of it.
	_, err := s.Store.SendQueuedMessageNow(id, task)
	return true, errors.Join(err, s.Store.settleSetAside(held, func(a *Action, _ Action) {
		a.Status, a.CanSendNow, a.SendNowWhy = "queued", true, ""
		a.Message = "Queued. It left its session's queue so the command he sent now runs first, and is typed there again after it."
	}))
}

// isCommandToSendNow reports whether message id to task is a command he may
// send now: one queued on the board with Send now, or one that reads Not
// sent, which Send now puts back in the queue to go at once.
func (s *Store) isCommandToSendNow(id, task string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.db.Actions, func(a Action) bool {
		return a.ID == id && a.TaskID == task && isHarnessCommand(a.Text) && (a.Kind == "message" && a.Status == "queued" && a.CanSendNow || isNotSent(a))
	})
}
