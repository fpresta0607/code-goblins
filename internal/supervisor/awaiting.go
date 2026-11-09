package supervisor

import (
	"context"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/monitor"
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
}

// sentToCEO and sentToGoblin are what a delivery that awaits its reader says,
// and sentNowInTurn what one says once its harness's send-now key went.
const (
	sentToCEO     = "Sent. The CEO takes it at its next tool call, or as its current turn ends."
	sentToGoblin  = "Sent. The goblin takes it at its next tool call, or as its current turn ends."
	sentNowInTurn = "Typed while it was working and sent now."
)

// commandWaits is why a slash or bang command offers no Send now.
const commandWaits = "A command runs when its turn ends."

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
	return Evaluation{Reason: sentToGoblin, Awaiting: awaitingGoblinDelivery(meta, since, text)}
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
	for terminal, deliveries := range waiting {
		slices.SortFunc(deliveries, func(a, b Action) int { return a.Awaiting.Since.Compare(b.Awaiting.Since) })
		if prompt := s.submitOnto(ctx, terminal, deliveries); len(prompt) > 0 {
			prompts = append(prompts, prompt)
		}
	}
	return s.Store.markHandedBack(prompts, time.Now().UTC())
}

// submitOnto presses the submit key in terminal when its harness waits idle
// with nothing on its prompt line but some of deliveries, all typed into it
// in the order given, as submitHandedBack says, and returns those it
// submitted as one prompt, or none. The screen is read without the CEO's
// typing lock, which a delivery to the CEO holds for seconds, and read again
// under it before the key, so nothing else types into the CEO's terminal
// meanwhile.
func (s *Service) submitOnto(ctx context.Context, terminal string, deliveries []Action) []Action {
	first := *deliveries[0].Awaiting
	if first.Task != "" {
		if meta, err := state.ReadTaskMeta(s.Store.Home.State, first.Task); err != nil || meta.SpawnGen != first.Generation {
			return nil
		}
	}
	screens, readable := harness.NativeScreens(harness.Kind(first.Harness))
	record, err := host.ReadRecord(s.Store.Home.State, terminal)
	if !readable || err != nil || !host.Running(record) {
		return nil
	}
	handedBack := func() []Action {
		rows, err := host.ReadScreen(record)
		if err != nil || !screens.IsReady(rows) || !screens.ComposerHoldsText(rows) {
			return nil
		}
		if pasted := slices.IndexFunc(deliveries, func(a Action) bool { return a.Awaiting.Pasted }); pasted >= 0 && screens.ComposerHoldsOnePaste(rows) {
			return deliveries[pasted : pasted+1]
		}
		for first := range deliveries {
			joined := ""
			for last, a := range deliveries[first:] {
				joined += a.Awaiting.Typed + "\n"
				if screens.ComposerMatches(rows, joined) {
					return deliveries[first : first+last+1]
				}
			}
		}
		return nil
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
// screen shows a turn under way; and the record its harness keeps of the
// conversation, for the text taken.
func (s *Service) lookAtTerminal(a Awaiting) terminalLook {
	id := a.Host
	took := false
	if a.Task != "" {
		meta, err := state.ReadTaskMeta(s.Store.Home.State, a.Task)
		if err != nil || meta.SpawnGen != a.Generation {
			return terminalLook{Gone: true}
		}
		id = meta.ID
		took = a.Digest != "" && conversations().Took(context.Background(), goblinConversation(meta), a.Digest, a.Since)
	} else {
		took = ceoConversationTook(context.Background(), s.Store.Home.State, a)
	}
	record, err := host.ReadRecord(s.Store.Home.State, id)
	if err != nil || !host.Running(record) {
		return terminalLook{Gone: true, Took: took}
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
	if a.Digest == "" {
		return false
	}
	sessions := []string{a.Session}
	if primary, live := livePrimary(stateDir); live && primary.Host == a.Host {
		sessions = append(sessions, ceoSession(stateDir, primary))
	}
	for _, session := range slices.Compact(sessions) {
		if session != "" && conversations().Took(ctx, monitor.Conversation{Harness: a.Harness, Session: session}, a.Digest, a.Since) {
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
// proof ceo send and the board wait for once a goblin is in a turn.
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
