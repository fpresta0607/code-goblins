package supervisor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/update"
)

// A message he sends a session whose terminal is not running starts that
// session, and is typed into it once it runs (the Overlord, 2026-11-04:
// "Modern chat terminal. Won't take new messages on a restart if the original
// terminal is not running. The modern chat should. be its own trigger to
// start any code session. Especially on a message sent."). A goblin comes
// back as the comeback brings one back, on its own conversation, and the CFO
// as it comes back after its terminal ended unasked, each told that his
// message follows. Each message starts its session once, one start at a time
// as every start goes, and a start of that session already under way, by
// anything, is waited on rather than made again. A goblin he stopped stays
// stopped, and its message goes to the CFO. A paused goblin keeps the message
// in its resume note and is resumed as his click resumes one, so its resume
// prompt gives the session his words as its first turn. While the session
// starts, the message waits saying so.

// messageStartRetry is how long a message waits before it tries again to
// start a goblin the machine had no room for.
const messageStartRetry = 30 * time.Second

// cfoMessageWhy is why a CFO started for his message is told it started.
const cfoMessageWhy = "the Overlord sent the CFO a message on the board while no CFO ran, which is typed in after this instruction"

// messageStart is the start one message made of its session: the message's
// action, and once the start ended, how it ended, what it said and when.
type messageStart struct {
	message string
	isDone  bool
	outcome ComebackOutcome
	said    string
	at      time.Time
}

// startingFor is how a message waits while session starts for it.
func startingFor(session string) Evaluation {
	return Evaluation{Reason: "Starting " + session + ". The message goes in once it runs.", SendNowWhy: "It is starting."}
}

// isStartUnderWay says a start of task's session is under way, by anything: a
// Start or Resume running or waiting its turn, a switch, the comeback, or a
// message's own. A terminal comes up before its start has told its harness
// anything, so a session reads as starting until that start returns, however
// its terminal reads. starts guards what it reads.
func (s *Service) isStartUnderWay(task string) bool {
	started, isTried := s.messageStarts[task]
	return isTried && !started.isDone || s.starting == task || s.changing[task] == "resume" || s.changing[task] == "switch" || s.isAsked(task)
}

// resumeForMessage asks the Resume that carries message a, which its paused
// goblin's note now keeps, as his click asks one: in its turn, with memory and
// disk to spare, and not again while one runs or waits. A board that runs no
// task processes leaves the note for a later Resume, and so does one the
// click's own checks refuse, which the board reports.
func (s *Service) resumeForMessage(a Action, meta state.TaskMeta) {
	if s.Options.Dispatch == nil || s.Options.Example {
		return
	}
	if err := s.askResume(lifecycleRequest{Task: a.TaskID, Generation: meta.SpawnGen, Operation: a.ID, Action: "resume"}); err != nil {
		s.reportComeback(fmt.Errorf("his message did not resume %s: %w", a.TaskID, err))
	}
}

// startGoblinForMessage starts the goblin of message a, whose terminal is not
// running, unless a start of it is under way or the one a made ended, and
// says how a waits. A goblin the start could not bring back has the CFO given
// a; one that came back and ended again, or that the start left as it was,
// has a take the ordinary wait; and one the machine had no room for is tried
// again after messageStartRetry, until messageWait has passed.
func (s *Service) startGoblinForMessage(a Action) (Evaluation, error) {
	comeback := s.Options.Comeback
	if comeback == nil || comeback.GoblinForMessage == nil || s.Options.Example {
		result, err := s.waitForGoblin(a, false)
		result.SendNowWhy = "Its terminal is not running."
		return result, err
	}
	task := a.TaskID
	s.starts.Lock()
	tried, isTried := s.messageStarts[task]
	isOwn := isTried && tried.message == a.ID
	isRoomWait := isOwn && tried.isDone && tried.outcome == WaitsForRoom
	switch {
	case s.isStartUnderWay(task):
		s.starts.Unlock()
		return startingFor(task), ErrDeferred
	case isOwn && tried.outcome == DidNotComeBack:
		s.starts.Unlock()
		return Evaluation{}, s.messageToCFO(a, task+" could not start for it: "+tried.said)
	case isOwn && !isRoomWait, s.changing[task] != "":
		s.starts.Unlock()
		result, err := s.waitForGoblin(a, false)
		result.SendNowWhy = "Its terminal is not running."
		return result, err
	case isRoomWait && time.Since(a.CreatedAt) > messageWait:
		s.starts.Unlock()
		return Evaluation{}, s.messageToCFO(a, task+" had no room to start for "+messageWait.String()+": "+tried.said)
	case isRoomWait && time.Since(tried.at) < messageStartRetry, s.launching() != "", s.isCFOComingBack, s.isRestartingPC():
		s.starts.Unlock()
		return startingFor(task), ErrDeferred
	}
	if s.messageStarts == nil {
		s.messageStarts = map[string]messageStart{}
	}
	s.markChanging(task, "resume")
	delete(s.changeErrors, task)
	s.messageStarts[task] = messageStart{message: a.ID, at: time.Now()}
	s.starts.Unlock()
	go s.bringGoblinBackForMessage(comeback, task)
	s.notify()
	return startingFor(task), ErrDeferred
}

// bringGoblinBackForMessage brings task's goblin back for the message that
// asked, once the machine has room for it, and records how that went.
func (s *Service) bringGoblinBackForMessage(comeback *Comeback, task string) {
	result := GoblinComeback{Outcome: AlreadyRuns}
	if !NativeTerminalRuns(s.Store.Home.State, task) {
		room := errors.New("free memory cannot be read")
		if s.Options.Dispatch != nil && s.Options.Dispatch.Memory != nil {
			if memory, err := s.Options.Dispatch.Memory(); err != nil {
				room = fmt.Errorf("free memory cannot be read: %w", err)
			} else {
				room = s.launchRoom(memory)
			}
		}
		if room != nil {
			result = GoblinComeback{Outcome: WaitsForRoom, Said: room.Error()}
		} else {
			result = comeback.GoblinForMessage(context.Background(), task)
		}
	}
	s.starts.Lock()
	delete(s.changing, task)
	started := s.messageStarts[task]
	started.isDone, started.outcome, started.said, started.at = true, result.Outcome, result.Said, time.Now()
	s.messageStarts[task] = started
	s.starts.Unlock()
	s.notify()
	s.wakeActions()
	s.runAsked()
}

// startCFOForMessage starts the CFO for message a while no CFO runs, unless
// a start of it is under way or the one a made ended, and says how a waits.
// A CFO that registered and runs but cannot be reached is not started again,
// and nothing starts one while an update installs, which starts the CFO
// after it.
func (s *Service) startCFOForMessage(a Action) (Evaluation, error) {
	waits := Evaluation{Reason: "Waits for the CFO.", SendNowWhy: "No CFO is running."}
	comeback := s.Options.Comeback
	stateDir := s.Store.Home.State
	cfo := readCFOState(stateDir)
	if comeback == nil || comeback.CFOEnded == nil || s.Options.Example || cfo.registered {
		return waits, ErrDeferred
	}
	if holder, err := lock.ReadNamed(stateDir, CFOLaunchLock); cfo.starting || update.Installing(stateDir) || err == nil && holder.VerifiedAlive() {
		return startingFor("the CFO"), ErrDeferred
	}
	s.starts.Lock()
	tried, isTried := s.messageStarts[""]
	isOwn := isTried && tried.message == a.ID
	switch {
	case isOwn && !tried.isDone, s.isCFOComingBack, s.isRestartingPC():
		s.starts.Unlock()
		return startingFor("the CFO"), ErrDeferred
	case isOwn && tried.said != "":
		s.starts.Unlock()
		waits.Reason = "Waits for the CFO, which did not start for it: " + tried.said
		return waits, ErrDeferred
	case isOwn:
		s.starts.Unlock()
		return waits, ErrDeferred
	}
	if s.messageStarts == nil {
		s.messageStarts = map[string]messageStart{}
	}
	s.isCFOComingBack = true
	s.messageStarts[""] = messageStart{message: a.ID, at: time.Now()}
	s.starts.Unlock()
	go s.bringCFOBackForMessage(comeback)
	s.notify()
	return startingFor("the CFO"), ErrDeferred
}

// bringCFOBackForMessage brings the CFO back for the message that asked and
// records how that went, keeping what went wrong for the board.
func (s *Service) bringCFOBackForMessage(comeback *Comeback) {
	err := comeback.CFOEnded(context.Background(), cfoMessageWhy)
	s.starts.Lock()
	s.isCFOComingBack = false
	started := s.messageStarts[""]
	started.isDone, started.at = true, time.Now()
	if err != nil {
		started.said = err.Error()
		err = fmt.Errorf("the CFO did not start for his message: %w", err)
	}
	s.messageStarts[""] = started
	s.starts.Unlock()
	s.reportComeback(err)
	s.wakeActions()
}
