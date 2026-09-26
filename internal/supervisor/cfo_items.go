package supervisor

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// errFromInbox refuses an item that speaks for the CFO but came through a
// file inbox. Every goblin runs as the same Windows user with cfo.exe at hand,
// so any of them can read primary.json, compute the identity an item carries,
// and write a file here; a file proves nothing about who wrote it.
var errFromInbox = errors.New("an item that speaks for the CFO reaches the board only over the supervisor's pipe, where the supervisor proves who sent it; this one was written into an inbox by some other process")

// acceptCFOItem records an item only the registered primary CFO may put on
// the board, once the process that sent it over the pipe is proven to be that
// CFO: its ancestry, each parent created no later than its child, reaches the
// process primary.json registers (pid and creation time), that process is
// alive, its Herdr pane or native terminal still holds it, and the process
// was already running when it connected. The item must also carry the
// identity that proof yields, and be the CFO's own kind: a question or review
// item with no task, or a clear.
func (s *Service) acceptCFOItem(ctx context.Context, pid int, connected time.Time, req runPipeRequest) error {
	if s.Options.CFO == nil {
		return errors.New("this supervisor cannot verify the CFO")
	}
	identity, release, err := s.Options.CFO.identityOf(ctx, pid, connected)
	if err != nil {
		return err
	}
	defer release()
	switch {
	case req.Kind == "question" && req.Question != nil:
		q := *req.Question
		if q.Task != "" || q.Identity != identity {
			return errors.New("a question over the pipe must be the registered CFO's own")
		}
		if err := s.Store.acceptQuestion(q); errors.Is(err, ErrDeferred) {
			return fmt.Errorf("the board already holds %d open questions", maxQuestions)
		} else if err != nil {
			return err
		}
		return nil
	case req.Kind == "review" && req.Review != nil:
		r := *req.Review
		if !cfoRecord(r) || r.Identity != identity {
			return errors.New("a review record over the pipe must be the registered CFO's own")
		}
		if err := s.Store.acceptReview(r); errors.Is(err, ErrDeferred) {
			return fmt.Errorf("the board already holds %d open items", maxReviews)
		} else if err != nil {
			return err
		}
		return nil
	case req.Kind == "answer" && req.Answer != nil:
		return s.Store.recordCFOAnswer(*req.Answer)
	}
	return fmt.Errorf("the supervisor takes no %q request of that shape", req.Kind)
}
