package supervisor

import (
	"errors"
	"slices"

	"github.com/fpresta0607/code-goblins/internal/wake"
)

// askedChange is a Start or a Resume the Overlord clicked. One goblin starts
// or resumes at a time, and only with memory and disk to spare, so a click
// that comes while another runs, or while either is short, waits its turn
// rather than being refused: one click starts or resumes a goblin. resume is
// what a Resume's click named; a Start's is nil.
type askedChange struct {
	task   string
	resume *lifecycleRequest
}

// launching names the goblin that starts or resumes now, or is empty. The
// caller holds s.starts.
func (s *Service) launching() string {
	if s.starting != "" {
		return s.starting
	}
	for task, action := range s.changing {
		if action == "resume" {
			return task
		}
	}
	return ""
}

// isAsked says a Start or Resume the Overlord clicked on task waits its turn.
// The caller holds s.starts.
func (s *Service) isAsked(task string) bool {
	return slices.ContainsFunc(s.asked, func(change askedChange) bool { return change.task == task })
}

// ask files a Start or a Resume to run in its turn, then runs whatever's turn
// has come. A click on a task that starts, resumes or waits its turn already
// changes nothing.
func (s *Service) ask(change askedChange) {
	s.starts.Lock()
	if s.starting != change.task && s.changing[change.task] != "resume" && !s.isAsked(change.task) {
		s.asked = append(s.asked, change)
	}
	s.starts.Unlock()
	s.notify()
	s.runAsked()
}

// withdraw takes back a Start or Resume of task that still waits its turn,
// as a Pause, Stop or Remove clicked after it does.
func (s *Service) withdraw(task string) {
	s.starts.Lock()
	s.asked = slices.DeleteFunc(s.asked, func(change askedChange) bool { return change.task == task })
	s.starts.Unlock()
}

// runAsked runs, oldest first, each Start and Resume the Overlord clicked
// whose turn has come: nothing else starts or resumes, and memory and disk
// allow. It stops at the first that must wait. One that no longer applies,
// such as a goblin the CFO resumed meanwhile, is dropped, and one that fails
// goes to the CFO, as every failure does.
func (s *Service) runAsked() {
	for {
		s.starts.Lock()
		if len(s.asked) == 0 || s.launching() != "" {
			s.starts.Unlock()
			return
		}
		next := s.asked[0]
		s.starts.Unlock()
		var err error
		if next.resume == nil {
			err = s.startQueued(next.task, true)
		} else {
			err = s.changeTask(*next.resume)
		}
		var started StartRefusal
		var changed taskRefusal
		isStartRefusal, isChangeRefusal := errors.As(err, &started), errors.As(err, &changed)
		if isStartRefusal && started.Passing || isChangeRefusal && changed.isPassing {
			return
		}
		s.starts.Lock()
		s.asked = slices.DeleteFunc(s.asked, func(change askedChange) bool { return change.task == next.task })
		if err != nil && !started.Held && !isChangeRefusal && next.resume == nil {
			if s.startErrors == nil {
				s.startErrors = map[string]string{}
			}
			s.startErrors[next.task] = err.Error()
		}
		s.starts.Unlock()
		if err != nil && !started.Held && !isChangeRefusal {
			verb := "start failed: "
			if next.resume != nil {
				verb = "resume failed: "
			}
			s.tellCFOOfFailure(next.task, verb+err.Error())
		}
		s.notify()
	}
}

// tellCFOOfFailure gives the CFO a start or change that failed on the board,
// as a notify keyed by its task, which is no question.
func (s *Service) tellCFOOfFailure(task, detail string) {
	if _, err := wake.Append(s.Store.Home.State, "notify", task, bounded(detail, 1500)); err != nil {
		s.publish(err)
	} else if _, err := wake.PublishEpisode(s.Store.Home.State); err != nil {
		s.publish(err)
	}
}
