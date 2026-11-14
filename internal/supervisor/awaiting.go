package supervisor

import (
	"cmp"
	"context"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Awaiting is a delivery typed into a native terminal and submitted that its
// harness has not yet been seen taking: the CEO's terminal by its host, or a
// goblin's by its task and spawn generation. A harness inside a turn queues
// the text and hands it to its model at its next tool call, and one slow to
// start its next turn takes it later, so until its own record of the
// conversation shows the text the delivery is sent, not failed.
type Awaiting struct {
	Host       string    `json:"host,omitempty"`
	Task       string    `json:"task,omitempty"`
	Generation string    `json:"generation,omitempty"`
	Harness    string    `json:"harness,omitempty"`
	Since      time.Time `json:"since"`
	// Digest is the monitor.TextDigest of the text typed, which the
	// harness's record of the conversation is searched for.
	Digest string `json:"digest,omitempty"`
	// Session is the CEO's harness session, whose record is searched; a
	// goblin's record is the one its harness keeps for its worktree.
	Session string `json:"session,omitempty"`
	// Typed is the line as typed, which its terminal's prompt line shows
	// again when its harness hands it back unsent.
	Typed string `json:"typed,omitempty"`
	// Pasted says Typed went as a bracketed paste, which its harness may
	// hand back as a paste's placeholder rather than as its words.
	Pasted bool `json:"pasted,omitempty"`
	// QuietSince is the last time the terminal's screen showed a turn under
	// way, or Since when it has shown none.
	QuietSince time.Time `json:"quiet_since"`
	// IsHeld says the line was submitted into a turn under way, so its
	// harness holds it in its own queue until that turn takes it: his
	// message reads Queued until then, never Sent (#2332).
	IsHeld bool `json:"is_held,omitempty"`
}

// sentToCEO and sentToGoblin are what a delivery that awaits its reader says,
// heldByCEO and heldByGoblin what one submitted into a turn under way says
// while its harness holds it in its own queue, and sentNowInTurn what one
// says once its harness's send-now key went.
const (
	sentToCEO     = "Sent. The CEO takes it at its next tool call, or as its current turn ends."
	sentToGoblin  = "Sent. The goblin takes it at its next tool call, or as its current turn ends."
	heldByCEO     = "Queued in the CEO's session. The CEO takes it at its next tool call, or as its current turn ends."
	heldByGoblin  = "Queued in the goblin's session. The goblin takes it at its next tool call, or as its current turn ends."
	sentNowInTurn = "Typed while it was working and sent now."
)

// commandWaits is why a slash or bang command waits for its turn to end, and
// why one offers no Send now where nothing can run it sooner; commandSendsNow
// is what Send now does for one held for a turn its harness's send-now key
// reaches, and commandNotSentNow what one says once a Send now could not run
// it at once (#1915).
const (
	commandWaits      = "A command runs when its turn ends."
	commandSendsNow   = "Send now interrupts the turn to run it."
	commandNotSentNow = "Send now could not run it at once, so it runs when its turn ends."
)

const (
	// deliveryQuiet is how long a terminal may show no turn, with no report
	// that its harness took a delivery, before the delivery counts as not
	// arrived. A harness has taken tens of seconds to start the turn that
	// reads it, which the five second confirmation called a failure.
	deliveryQuiet = 3 * time.Minute
	// deliveryForget is how long a delivery that never arrived is still
	// watched for a late report.
	deliveryForget = 24 * time.Hour
	// quietStep is how far the last look that showed a turn must move before
	// it is saved again, so a long turn does not write the state every cycle.
	quietStep = 30 * time.Second
)

// terminalLook is what the supervisor sees of the terminal a delivery waits
// in: gone, or its screen showing a turn under way, and whether its harness's
// record of the conversation shows the text taken.
type terminalLook struct {
	Gone    bool
	Working bool
	Took    bool
}

// digests are the names the text of a can carry in its harness's record: the
// one its words as typed have now, then the one stored with it where that is
// another. A build before #1915's fix named text character for character, so
// a delivery it stored whose text held a character that draws nothing carries
// a name no record of the text has, and is found by its words as typed
// instead. Lines a stopped turn handed back went as one prompt and carry that
// prompt's name (markHandedBack). A delivery stored with no name has none.
func (a Awaiting) digests() []string {
	if a.Digest == "" {
		return nil
	}
	if a.Typed == "" {
		return []string{a.Digest}
	}
	return slices.Compact([]string{monitor.TextDigest(a.Typed), a.Digest})
}

// tookIn reports whether the record conversation names shows the text of a
// taken since its submit.
func (a Awaiting) tookIn(ctx context.Context, conversation monitor.Conversation) bool {
	return slices.ContainsFunc(a.digests(), func(digest string) bool {
		return conversations().Took(ctx, conversation, digest, a.Since)
	})
}

// recipient is who a delivery waits on, as the Overlord reads it.
func (a Awaiting) recipient() string {
	if a.Task != "" {
		return "the goblin"
	}
	return "the CEO"
}

// settleDeliveries settles each delivery that awaits its reader: delivered
// once the harness's own record of the conversation shows the text taken
// since the submit, and not arrived, in words that say what to do, only when
// its terminal is gone or has shown no turn for deliveryQuiet with the text
// still not taken. The harness's prompt hook proves nothing here: it runs
// when the harness queues text typed during a turn, before the agent has it.
// A delivery that did not arrive is still watched, so a late take delivers
// it after all. look reads each terminal and record away from the store's
// lock.
func (s *Store) settleDeliveries(now time.Time, look func(Awaiting) terminalLook) error {
	s.mu.Lock()
	looks := map[string]terminalLook{}
	var sent []Action
	for _, a := range s.db.Actions {
		if a.Awaiting != nil {
			sent = append(sent, a)
		}
	}
	s.mu.Unlock()
	for _, a := range sent {
		looks[a.ID] = look(*a.Awaiting)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for i := range s.db.Actions {
		a := &s.db.Actions[i]
		if a.Awaiting == nil {
			continue
		}
		waiting, who := *a.Awaiting, a.Awaiting.recipient()
		seen, looked := looks[a.ID]
		switch {
		case !looked:
			continue
		case seen.Took:
			a.Status, a.Awaiting, a.Advice = "succeeded", nil, ""
			a.Message = "Taken by " + who + ", as its own record of the conversation shows."
			s.deliveredReview(a.ID, waiting.Task)
		case a.Status != "running":
			// It did not arrive; only a late take changes that.
			if now.Sub(waiting.Since) < deliveryForget {
				continue
			}
			a.Awaiting = nil
		case seen.Gone:
			a.Status, a.Awaiting = "uncertain", nil
			a.Advice = "The terminal of " + who + " closed before it took your answer. Give " + who + " your answer once it is running again."
			a.Message = a.Advice
		case seen.Working:
			if now.Sub(waiting.QuietSince) < quietStep {
				continue
			}
			waiting.QuietSince = now
			a.Awaiting = &waiting
		case now.Sub(waiting.QuietSince) >= deliveryQuiet:
			a.Status = "uncertain"
			a.Advice = "Your answer was typed for " + who + ", which has not picked it up. Open its terminal and press Enter if your answer is waiting in its box; if it is not there, type it to " + who + "."
			a.Message = a.Advice
		default:
			continue
		}
		a.UpdatedAt, changed = now, true
	}
	if !changed {
		return nil
	}
	s.updateQuestionOutcomes()
	return s.save()
}

// deliveredReview marks the review item whose answer action id carried as
// delivered, when task, whose terminal took it, is the item's reporter: an
// answer handed to the CEO for a goblin that restarted or ended never reached
// the goblin. The caller holds the store lock.
func (s *Store) deliveredReview(action, task string) {
	for i := range s.db.Reviews {
		if s.db.Reviews[i].AnswerID == action && s.db.Reviews[i].Task == task {
			s.db.Reviews[i].Delivered = true
		}
	}
}

// behindGoblinsTurn is the outcome of a delivery of text typed for goblin
// task while it was in a turn. The goblin's harness takes it at its next tool
// call, so the delivery is sent and awaits the goblin's record showing it.
// A task whose record can no longer be read names nothing the board can wait
// on, so its delivery reads as unwatched says.
func (s *Service) behindGoblinsTurn(task string, since time.Time, text, unwatched string) Evaluation {
	meta, err := state.ReadTaskMeta(s.Store.Home.State, task)
	if err != nil {
		return Evaluation{Reason: unwatched}
	}
	held := awaitingGoblinDelivery(meta, since, text)
	held.IsHeld = true
	return Evaluation{Reason: heldByGoblin, Awaiting: held}
}

// awaitingGoblinDelivery is the delivery of text, typed into meta's native
// terminal at since, that its harness has not yet been seen taking. It takes
// the recipient the delivery was verified against, never a fresh read: a
// record that disappeared or was replaced meanwhile must not turn the delivery
// into a different task's, nor bind it to a generation that never took it.
func awaitingGoblinDelivery(meta state.TaskMeta, since time.Time, text string) *Awaiting {
	return &Awaiting{Task: meta.ID, Generation: meta.SpawnGen, Harness: meta.Harness, Since: since, Digest: monitor.TextDigest(goblinLine(text)), Typed: goblinLine(text)}
}

// submitHandedBack submits again each delivery its harness handed back to its
// prompt line unsent. Claude Code, Codex and pi each put the lines typed into
// a turn back on the prompt line when that turn is interrupted (Claude Code
// 2.1.316 does so in its cancelAndDrainPending), so a message of his typed
// into a turn he then stopped with the box's Stop sat there unsent while its
// bubble said Sent (the Overlord, 2026-11-02: "nor do they work with
// interrupts"). The turn it waited for is over, so it goes as the next
// prompt, once the terminal waits idle with nothing else on its prompt line,
// whitespace aside: one such line, several joined in the order they were
// typed, which go as one prompt, or the placeholder of one pasted line alone.
// Words of his before or after them leave the prompt line his to send.
func (s *Service) submitHandedBack(ctx context.Context) error {
	waiting := map[string][]Action{}
	for _, a := range s.Store.Snapshot().Actions {
		if a.Status == "running" && a.Awaiting != nil && a.Awaiting.Typed != "" {
			terminal := a.Awaiting.Host
			if terminal == "" {
				terminal = a.Awaiting.Task
			}
			waiting[terminal] = append(waiting[terminal], a)
		}
	}
	var prompts [][]Action
	for _, deliveries := range waiting {
		slices.SortFunc(deliveries, func(a, b Action) int { return a.Awaiting.Since.Compare(b.Awaiting.Since) })
		if prompt := s.submitOnto(ctx, deliveries); len(prompt) > 0 {
			prompts = append(prompts, prompt)
		}
	}
	return s.Store.markHandedBack(prompts, time.Now().UTC())
}

// deliveryTerminal returns the running terminal delivery a was typed into
// and its harness's screens, and false once that terminal is gone, its goblin
// restarted or ended, or its screens cannot be read.
func (s *Service) deliveryTerminal(a Awaiting) (host.Record, harness.Screens, bool) {
	terminal := a.Host
	if a.Task != "" {
		terminal = a.Task
		if meta, err := state.ReadTaskMeta(s.Store.Home.State, a.Task); err != nil || meta.SpawnGen != a.Generation {
			return host.Record{}, harness.Screens{}, false
		}
	}
	screens, readable := harness.NativeScreens(harness.Kind(a.Harness))
	record, err := host.ReadRecord(s.Store.Home.State, terminal)
	return record, screens, readable && err == nil && host.Running(record)
}

// promptLineHolds finds the run of typed, deliveries typed into one terminal
// in this order, that is all rows' prompt line holds, whitespace aside: one
// of them, several joined in the order they were typed, or the placeholder
// of one pasted line alone. It returns where the run starts and how many it
// is, none when the prompt line holds anything else.
func promptLineHolds(screens harness.Screens, rows []string, typed []Awaiting) (int, int) {
	if !screens.ComposerHoldsText(rows) {
		return 0, 0
	}
	if pasted := slices.IndexFunc(typed, func(a Awaiting) bool { return a.Pasted }); pasted >= 0 && screens.ComposerHoldsOnePaste(rows) {
		return pasted, 1
	}
	for first := range typed {
		joined := ""
		for last, a := range typed[first:] {
			joined += a.Typed + "\n"
			if screens.ComposerMatches(rows, joined) {
				return first, last + 1
			}
		}
	}
	return 0, 0
}

// takeBackLeftTyped takes back off its prompt line what each delivery typed
// into a terminal and never submitted, once the terminal shows those words
// there with nothing else: a harness that had not read them when their
// delivery gave up shows them once it reads again, where they held back
// every message after them (the Overlord, 2026-11-10: "queued messages in
// modern chat get stuck in input box in terminal chat and never send",
// #2306). A message taken back reads Not sent. Words of his beside them
// leave the prompt line his, a goblin the board is typing for is left to that
// delivery, and words whose terminal is gone or was started again since,
// which can never show them, or that never showed within deliveryForget, are
// watched no longer.
func (s *Service) takeBackLeftTyped(ctx context.Context) error {
	now := time.Now().UTC()
	left := map[string][]Action{}
	typing := map[string]bool{}
	for _, a := range s.Store.Snapshot().Actions {
		if a.LeftTyped != nil {
			terminal := cmp.Or(a.LeftTyped.Host, a.LeftTyped.Task)
			left[terminal] = append(left[terminal], a)
		}
		if a.TaskID != "" && a.Status == "running" && a.Awaiting == nil {
			typing[a.TaskID] = true
		}
	}
	var taken, dropped []Action
	for terminal, deliveries := range left {
		slices.SortFunc(deliveries, func(a, b Action) int { return a.LeftTyped.Since.Compare(b.LeftTyped.Since) })
		var watched []Action
		var record host.Record
		var screens harness.Screens
		for _, a := range deliveries {
			live, read, isLive := s.deliveryTerminal(*a.LeftTyped)
			if !isLive || a.LeftTyped.Since.Before(live.Started) || now.Sub(a.LeftTyped.Since) >= deliveryForget {
				dropped = append(dropped, a)
				continue
			}
			record, screens = live, read
			watched = append(watched, a)
		}
		if len(watched) == 0 || watched[0].LeftTyped.Task != "" && typing[terminal] {
			continue
		}
		taken = append(taken, s.takeBackFrom(ctx, record, screens, watched)...)
	}
	return s.Store.settleLeftTyped(taken, dropped, now)
}

// takeBackFrom takes back off the prompt line of the terminal record names
// those of deliveries, all left typed in it in the order given, whose words
// are all the line holds, and returns them once the line read empty. The
// screen is read without the CEO's typing lock and again under it before any
// key, as submitOnto reads it.
func (s *Service) takeBackFrom(ctx context.Context, record host.Record, screens harness.Screens, deliveries []Action) []Action {
	typed := make([]Awaiting, len(deliveries))
	for i, a := range deliveries {
		typed[i] = *a.LeftTyped
	}
	shown := func() []Action {
		rows, err := host.ReadScreen(record)
		if err != nil {
			return nil
		}
		at, count := promptLineHolds(screens, rows, typed)
		return deliveries[at : at+count]
	}
	held := shown()
	if len(held) == 0 {
		return nil
	}
	if typed[0].Host != "" && s.Options.CEO != nil {
		s.Options.CEO.typing.Lock()
		defer s.Options.CEO.typing.Unlock()
		if held = shown(); len(held) == 0 {
			return nil
		}
	}
	client, err := host.Dial(record)
	if err != nil {
		return nil
	}
	defer client.Close()
	words := ""
	for _, a := range held {
		words += a.LeftTyped.Typed
	}
	if ctx.Err() != nil || !spawn.TakeBack(func() ([]string, error) { return host.ReadScreen(record) }, client.Input, screens, words) {
		return nil
	}
	return held
}

// settleLeftTyped records that the words each of taken left typed were taken
// back off their prompt line unsubmitted, so a message among them reads Not
// sent, and that the words of each of dropped are watched no longer.
func (s *Store) settleLeftTyped(taken, dropped []Action, now time.Time) error {
	if len(taken) == 0 && len(dropped) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	settle := func(left Action, isTaken bool) {
		at := slices.IndexFunc(s.db.Actions, func(a Action) bool { return a.ID == left.ID && a.TaskID == left.TaskID })
		if at < 0 || s.db.Actions[at].LeftTyped == nil {
			return
		}
		a := &s.db.Actions[at]
		who := a.LeftTyped.recipient()
		a.LeftTyped, a.UpdatedAt = nil, now
		if !isTaken {
			return
		}
		a.Message = ErrNotSent.Error() + ". It was typed for " + who + ", which read it only after its delivery had ended, so it was taken back off its prompt line unsubmitted."
		if a.Kind == "message" {
			a.Status = "failed"
		}
	}
	for _, left := range dropped {
		settle(left, false)
	}
	for _, left := range taken {
		settle(left, true)
	}
	s.updateQuestionOutcomes()
	return s.save()
}

// submitOnto presses the submit key in terminal when its harness waits idle
// with nothing on its prompt line but some of deliveries, all typed into it
// in the order given, as submitHandedBack says, and returns those it
// submitted as one prompt, or none. The screen is read without the CEO's
// typing lock, which a delivery to the CEO holds for seconds, and read again
// under it before the key, so nothing else types into the CEO's terminal
// meanwhile.
func (s *Service) submitOnto(ctx context.Context, deliveries []Action) []Action {
	first := *deliveries[0].Awaiting
	record, screens, isLive := s.deliveryTerminal(first)
	if !isLive {
		return nil
	}
	typed := make([]Awaiting, len(deliveries))
	for i, a := range deliveries {
		typed[i] = *a.Awaiting
	}
	handedBack := func() []Action {
		rows, err := host.ReadScreen(record)
		if err != nil || !screens.IsReady(rows) {
			return nil
		}
		at, count := promptLineHolds(screens, rows, typed)
		return deliveries[at : at+count]
	}
	prompt := handedBack()
	if len(prompt) == 0 {
		return nil
	}
	if first.Host != "" && s.Options.CEO != nil {
		s.Options.CEO.typing.Lock()
		defer s.Options.CEO.typing.Unlock()
		if prompt = handedBack(); len(prompt) == 0 {
			return nil
		}
	}
	client, err := host.Dial(record)
	if err != nil {
		return nil
	}
	defer client.Close()
	submit := "\r"
	if screens.PasteTakesEnter {
		submit = "\x1b[F\r"
	}
	if ctx.Err() != nil || client.Input([]byte(submit)) != nil {
		return nil
	}
	return prompt
}

// markHandedBack has each delivery of prompts, each the deliveries submitted
// together as one prompt, submitted again at now, so it waits anew for its
// harness to take it and is not called unarrived for the quiet of the turn
// that was stopped. Each then awaits its prompt's text, which its harness's
// record shows as one prompt, rather than its own line.
func (s *Store) markHandedBack(prompts [][]Action, now time.Time) error {
	if len(prompts) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, prompt := range prompts {
		joined := ""
		for _, delivery := range prompt {
			joined += delivery.Awaiting.Typed + "\n"
		}
		for _, delivery := range prompt {
			at := slices.IndexFunc(s.db.Actions, func(a Action) bool { return a.ID == delivery.ID && a.TaskID == delivery.TaskID })
			if at < 0 || s.db.Actions[at].Status != "running" || s.db.Actions[at].Awaiting == nil {
				continue
			}
			a := &s.db.Actions[at]
			waiting := *a.Awaiting
			waiting.QuietSince, waiting.Digest = now, monitor.TextDigest(joined)
			a.Awaiting, a.Message, a.UpdatedAt = &waiting, "Its turn was stopped, so it was submitted as the next prompt.", now
		}
	}
	return s.save()
}

// lookAtTerminal reads the terminal a delivery waits in: gone when its host
// no longer answers for it or its goblin restarted or ended, working when its
// screen, or a stream session's state, shows a turn under way; and the record
// its harness keeps of the conversation, for the text taken.
func (s *Service) lookAtTerminal(a Awaiting) terminalLook {
	id := a.Host
	took := false
	if a.Task != "" {
		meta, err := state.ReadTaskMeta(s.Store.Home.State, a.Task)
		if err != nil || meta.SpawnGen != a.Generation {
			return terminalLook{Gone: true}
		}
		id = meta.ID
		took = a.tookIn(context.Background(), goblinConversation(meta))
	} else {
		took = ceoConversationTook(context.Background(), s.Store.Home.State, a)
	}
	record, err := host.ReadRecord(s.Store.Home.State, id)
	if err != nil || !host.Running(record) {
		return terminalLook{Gone: true, Took: took}
	}
	if record.Stream {
		session, err := host.ReadStreamState(record)
		return terminalLook{Working: err == nil && session.IsWorking, Took: took}
	}
	screens, readable := harness.NativeScreens(harness.Kind(a.Harness))
	if !readable {
		return terminalLook{Took: took}
	}
	rows, err := host.ReadScreen(record)
	return terminalLook{Working: err == nil && screens.IsWorking(rows), Took: took}
}

// ceoConversationTook reports whether the CEO's own record of conversation a
// shows it took a's text since: the session a was submitted in, and the one a
// live primary holds now, since a clear, compact or resume moves the
// conversation to a new session after the submit.
func ceoConversationTook(ctx context.Context, stateDir string, a Awaiting) bool {
	sessions := []string{a.Session}
	if primary, live := livePrimary(stateDir); live && primary.Host == a.Host {
		sessions = append(sessions, ceoSession(stateDir, primary))
	}
	for _, session := range slices.Compact(sessions) {
		if session != "" && a.tookIn(ctx, monitor.Conversation{Harness: a.Harness, Session: session}) {
			return true
		}
	}
	return false
}

// conversationReaders keeps one reader of the harnesses' records for each
// user home, so what a reader learns of each Codex rollout is read once.
var conversationReaders sync.Map

// conversations reads, in this user's home, the record each harness keeps of
// its conversations: the proof an agent took text typed into its terminal.
func conversations() *monitor.HostProgress {
	home, _ := os.UserHomeDir()
	reader, _ := conversationReaders.LoadOrStore(home, &monitor.HostProgress{Home: home})
	return reader.(*monitor.HostProgress)
}

// ceoSession is the session the registered CEO, primary, holds its
// conversation in now: the one it last registered, when that registration
// was this CEO process's, since a compact, a clear or a resume moves it to a
// new session while primary.json keeps the session it first registered;
// else the one primary names.
func ceoSession(stateDir string, primary primaryRegistration) string {
	if conversation, err := ReadCEOConversation(stateDir); err == nil && conversation.Harness == primary.Agent && conversation.Host == primary.Host && conversation.PID == primary.Process.PID {
		return conversation.Session
	}
	return primary.Process.Session
}

// NativeTook reports whether native goblin meta's harness took text since,
// as the record its harness keeps of the goblin's conversation shows: the
// proof goblins send and the board wait for once a goblin is in a turn.
func NativeTook(ctx context.Context, meta state.TaskMeta, session, text string, since time.Time) bool {
	conversation := goblinConversation(meta)
	conversation.Session = session
	return conversations().Took(ctx, conversation, monitor.TextDigest(text), since)
}

// goblinConversation names the record native goblin meta's harness keeps of
// its conversation: the one for its worktree, begun no earlier than its
// spawn.
func goblinConversation(meta state.TaskMeta) monitor.Conversation {
	return monitor.Conversation{Harness: meta.Harness, Dir: meta.Worktree, Started: spawnTime(meta.SpawnGen)}
}
