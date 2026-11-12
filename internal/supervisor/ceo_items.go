package supervisor

import (
	"errors"
	"fmt"
	"time"
)

// errFromInbox refuses an item that speaks for the CEO but came through a
// file inbox. Every goblin runs as the same Windows user with goblins.exe at hand,
// so any of them can read primary.json, compute the identity an item carries,
// and write a file here; a file proves nothing about who wrote it.
var errFromInbox = errors.New("an item that speaks for the CEO reaches the board only over the supervisor's pipe, where the supervisor proves who sent it; this one was written into an inbox by some other process")

// acceptCEOItem records an item only the registered primary CEO may put on
// the board, once the process that sent it over the pipe is proven to be that
// CEO: its ancestry, each parent created no later than its child, reaches the
// process primary.json registers (pid and creation time), that process is
// alive, its native terminal still holds it, and the process
// was already running when it connected. The item must also carry the
// identity that proof yields, and be the CEO's own kind: a question or review
// item with no task, or a clear. A decision logged under AFK mode carries no
// identity: the proof is what makes it the CEO's.
func (s *Service) acceptCEOItem(pid int, connected time.Time, req runPipeRequest) error {
	if s.Options.CEO == nil {
		return errors.New("this supervisor cannot verify the CEO")
	}
	identity, release, err := s.Options.CEO.identityOf(pid, connected)
	if err != nil {
		return err
	}
	defer release()
	switch {
	case req.Kind == "question" && req.Question != nil:
		q := *req.Question
		if q.Task != "" || q.Identity != identity {
			return errors.New("a question over the pipe must be the registered CEO's own")
		}
		if err := s.Store.acceptQuestion(q); errors.Is(err, ErrDeferred) {
			return fmt.Errorf("the board already holds %d open questions", maxQuestions)
		} else if err != nil {
			return err
		}
		return nil
	case req.Kind == "review" && req.Review != nil:
		r := *req.Review
		if !ceoRecord(r) || r.Identity != identity {
			return errors.New("a review record over the pipe must be the registered CEO's own")
		}
		if err := s.Store.acceptReview(r); errors.Is(err, ErrDeferred) {
			return fmt.Errorf("the board already holds %d open items", maxReviews)
		} else if err != nil {
			return err
		}
		return nil
	case req.Kind == "answer" && req.Answer != nil:
		return s.Store.recordCEOAnswer(*req.Answer)
	case req.Kind == "credential" && req.Credential != nil:
		r := *req.Credential
		if r.By != "ceo" || r.Identity != identity {
			return errors.New("a credential request over the pipe must be the registered CEO's own")
		}
		_, err := s.acceptCredentialRequest(r)
		return err
	case req.Kind == "withdraw-run":
		return s.Store.withdrawRun(req.ID, req.Reason)
	case req.Kind == "afk-log" && req.AFK != nil:
		return s.logAFKDecision(*req.AFK)
	case req.Kind == "afk-strike" && req.AFK != nil:
		return s.strikeAFKLine(*req.AFK)
	case req.Kind == "afk-settle" && req.AFK != nil:
		return s.settleAFKLine(*req.AFK)
	}
	return fmt.Errorf("the supervisor takes no %q request of that shape", req.Kind)
}
