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
// harness has not yet been seen taking: the CFO's terminal by its host, or a
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
	// Session is the CFO's harness session, whose record is searched; a
	// goblin's record is the one its harness keeps for its worktree.
	Session string `json:"session,omitempty"`
	// QuietSince is the last time the terminal's screen showed a turn under
	// way, or Since when it has shown none.
	QuietSince time.Time `json:"quiet_since"`
}

// sentToCFO and sentToGoblin are what a delivery that awaits its reader
// says.
const (
	sentToCFO    = "Sent. The CFO takes it at its next tool call, or as its current turn ends."
	sentToGoblin = "Sent. The goblin takes it at its next tool call, or as its current turn ends."
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

// recipient is who a delivery waits on, as the Overlord reads it.
func (a Awaiting) recipient() string {
	if a.Task != "" {
		return "the goblin"
	}
	return "the CFO"
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
// answer handed to the CFO for a goblin that restarted or ended never reached
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
	return Evaluation{Reason: sentToGoblin, Awaiting: &Awaiting{Task: meta.ID, Generation: meta.SpawnGen, Harness: meta.Harness, Since: since, Digest: monitor.TextDigest(goblinLine(text))}}
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
	} else if a.Digest != "" {
		// The CFO may have cleared or compacted its conversation since the
		// submit, which moves it to a new session.
		sessions := []string{a.Session}
		if primary, live := livePrimary(s.Store.Home.State); live && primary.Host == a.Host {
			sessions = append(sessions, cfoSession(s.Store.Home.State, primary))
		}
		for _, session := range slices.Compact(sessions) {
			if session != "" && conversations().Took(context.Background(), monitor.Conversation{Harness: a.Harness, Session: session}, a.Digest, a.Since) {
				took = true
			}
		}
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

// cfoSession is the session the registered CFO, primary, holds its
// conversation in now: the one it last registered, when that registration
// was this CFO process's, since a compact, a clear or a resume moves it to a
// new session while primary.json keeps the session it first registered;
// else the one primary names.
func cfoSession(stateDir string, primary primaryRegistration) string {
	if conversation, err := ReadCFOConversation(stateDir); err == nil && conversation.Harness == primary.Agent && conversation.Host == primary.Host && conversation.PID == primary.Process.PID {
		return conversation.Session
	}
	return primary.Process.Session
}

// NativeTook reports whether native goblin meta's harness took text since,
// as the record its harness keeps of the goblin's conversation shows: the
// proof cfo send and the board wait for once a goblin is in a turn.
func NativeTook(ctx context.Context, meta state.TaskMeta, text string, since time.Time) bool {
	return conversations().Took(ctx, goblinConversation(meta), monitor.TextDigest(text), since)
}

// goblinConversation names the record native goblin meta's harness keeps of
// its conversation: the one for its worktree, begun no earlier than its
// spawn.
func goblinConversation(meta state.TaskMeta) monitor.Conversation {
	return monitor.Conversation{Harness: meta.Harness, Dir: meta.Worktree, Started: spawnTime(meta.SpawnGen)}
}
