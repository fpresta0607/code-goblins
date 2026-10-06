package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const (
	// pagePollTimeout bounds one poll, so a wait that closes or a supervisor
	// that stops never leaves a poll running for long behind it.
	pagePollTimeout = 5 * time.Minute
	// pagePollAttempts is how many polls in a row may fail before the CFO is
	// told the page cannot be watched.
	pagePollAttempts = 3
	// pageFeedbackInline bounds feedback carried in a wake when it cannot be
	// saved; its end is kept.
	pageFeedbackInline = 4000
)

// pagePollPause separates a failed poll, or a wake the queue refused, from
// the next attempt.
var pagePollPause = 10 * time.Second

// watchPages keeps one poller for each open item that names a Lavish page,
// and stops the poller of an item that is no longer open. lavish-axi hands a
// page's feedback to whichever poll takes it, so the supervisor is the only
// one that polls: the Overlord's answer on any page reaches the CFO.
func (s *Service) watchPages(ctx context.Context) {
	if s.Options.PollPage == nil {
		return
	}
	open := map[string]Review{}
	for _, r := range s.Store.Snapshot().Reviews {
		if r.State == "open" && r.LavishPage != "" {
			open[r.ID] = r
		}
	}
	s.pagesMu.Lock()
	defer s.pagesMu.Unlock()
	if s.pages == nil {
		s.pages = map[string]context.CancelFunc{}
	}
	for id, stop := range s.pages {
		if _, ok := open[id]; !ok {
			stop()
			delete(s.pages, id)
		}
	}
	for id, r := range open {
		if _, ok := s.pages[id]; ok || ctx.Err() != nil {
			continue
		}
		pageCtx, stop := context.WithCancel(ctx)
		s.pages[id] = stop
		s.pageWork.Add(1)
		go func() {
			defer s.pageWork.Done()
			s.watchPage(pageCtx, r)
		}()
	}
}

// watchPage polls one item's page until the Overlord answers on it or ends
// it, the item closes, or the page cannot be polled, then gives the CFO what
// happened and closes the item. A closed review window is not the end of the
// review: lavish-axi keeps the session, his answers queue on the page until a
// poll takes them, and reopening the page resumes the same review, so the
// poll goes on after a pause that keeps a window that keeps disconnecting
// from spinning it. What he sends without ending the review is a revision,
// unless the page carries a question it answers: its reporter makes the next
// version, the page says so, and the poll goes on.
func (s *Service) watchPage(ctx context.Context, r Review) {
	failures := 0
	reply := ""
	for {
		poll, err := s.Options.PollPage(ctx, r.LavishPage, reply, pagePollTimeout)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			reply = ""
		}
		if err == nil && poll.Status == "feedback" && !poll.Ended && !s.Store.asksOnPage(r) {
			failures = 0
			if reply = s.takeRevision(ctx, r, poll); reply == "" {
				return
			}
			continue
		}
		if err == nil && (poll.Status == "waiting" || poll.Status == "browser_disconnected") {
			failures = 0
			if poll.Status == "browser_disconnected" {
				if err := s.Store.windowClosed(r.ID, time.Now().UTC()); err != nil {
					s.publish(err)
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(pagePollPause):
				}
			}
			continue
		}
		if err != nil {
			if failures++; failures < pagePollAttempts {
				select {
				case <-ctx.Done():
					return
				case <-time.After(pagePollPause):
				}
				continue
			}
		}
		s.handPageToCFO(ctx, r, poll, err)
		return
	}
}

// takeRevision hands the Overlord's revision on an item's page to its
// reporter and marks the item as waiting on the next version, and returns the
// reply the page shows him: that the revision was received and what happens
// next. A goblin gets the revision in its own terminal, and the CFO is told;
// when the goblin cannot take it, or the page is the CFO's own, the CFO gets
// it to act on. The poll consumed the revision, so the wake is retried until
// the CFO has it; an empty reply means the poller stopped first.
func (s *Service) takeRevision(ctx context.Context, r Review, poll axi.PagePoll) string {
	feedback := "his feedback is in "
	if saved, err := savePageFeedback(s.Store.Home.State, r, poll.Output); err == nil {
		feedback += saved
	} else {
		s.publish(err)
		feedback = "his feedback could not be saved (" + err.Error() + "): " + bounded(poll.Output, pageFeedbackInline)
	}
	asked := strings.Join(poll.Prompts, "\n")
	if asked == "" {
		asked = feedback
	}
	key, maker, detail := r.ID, "The CFO makes", "the Overlord asked for a revision on the page "+r.LavishPage+"; "+feedback+", act on it"
	if r.Task != "" {
		key, maker, detail = r.Task, r.Task+" makes", "the Overlord asked for a revision on the page "+r.LavishPage+"; "+feedback+", relay it to the goblin"
		if s.deliverToGoblin(ctx, r, fmt.Sprintf("The Overlord asked for a revision on your review page %s: %s\nMake the next version in the same file, then run cfo notify %s --waiting-on overlord \"<why>\" --lavish %s again: it replaces the page in the same review.", r.LavishPage, asked, r.Task, r.LavishPage)) {
			detail = "the Overlord asked for a revision on the page " + r.LavishPage + ", and " + r.Task + " has it and makes the next version: " + asked
		}
	}
	if !s.queueReviewWake(ctx, key, bounded(detail, 4000)) {
		return ""
	}
	if err := s.Store.reviseReview(r.ID, time.Now().UTC()); err != nil {
		s.publish(err)
	}
	return "Revision received. " + maker + " the next version, which replaces this page."
}

// deliverToGoblin types text into the goblin's own terminal, as a board
// answer reaches it, and reports whether the goblin has it; one queued behind
// its turn counts.
func (s *Service) deliverToGoblin(ctx context.Context, r Review, text string) bool {
	if s.Options.CFO == nil {
		return false
	}
	_, err := s.Options.CFO.SendGoblin(ctx, r.Task, r.Identity, text)
	if err != nil && !errors.Is(err, fleet.ErrQueuedBehindTurn) {
		s.publish(err)
		return false
	}
	return true
}

// queueReviewWake queues a review wake for the CFO, retrying until the queue
// takes it, and reports whether it did before the poller stopped.
func (s *Service) queueReviewWake(ctx context.Context, key, detail string) bool {
	for {
		_, err := wake.Append(s.Store.Home.State, "review", key, detail)
		if err == nil {
			break
		}
		s.publish(err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(pagePollPause):
		}
	}
	if _, err := wake.PublishEpisode(s.Store.Home.State); err != nil {
		s.publish(err)
	}
	return true
}

// handPageToCFO wakes the CFO with what became of an item's page and closes
// the item. His answer reaches a goblin in its own terminal and the CFO is
// told; when the goblin cannot take it, the CFO relays it, and the CFO's own
// page has no goblin, so its wake is keyed by the item. The poll consumed the
// page's feedback, so the wake is retried until the CFO has it, and the item
// stays open until then. An item the Overlord answered on its page closes
// answered, by him, on the page, and one he ended there closes as his clear;
// only an item that closed without his word is withdrawn.
func (s *Service) handPageToCFO(ctx context.Context, r Review, poll axi.PagePoll, pollErr error) {
	var detail string
	relay, key, answered := "relay it to the goblin", r.Task, "You answered on its page; the CFO relays it to the goblin."
	if r.Task == "" {
		relay, key, answered = "act on it", r.ID, "You answered on its page; the CFO has it."
	}
	closed := Review{State: "withdrawn"}
	switch {
	case pollErr != nil:
		detail = fmt.Sprintf("the supervisor cannot poll the page %s (%v); the Overlord's answer on it reaches nobody, so ask him in text", r.LavishPage, pollErr)
		closed.Reason = "The page could not be polled; the CFO was told."
	case poll.Status == "feedback":
		on := "the Overlord answered on the page " + r.LavishPage
		if poll.Ended {
			on += " and ended the review"
		}
		asked := strings.Join(poll.Prompts, "\n")
		saved, err := savePageFeedback(s.Store.Home.State, r, poll.Output)
		told := asked
		if told == "" {
			told = "his feedback is in " + saved
		}
		if r.Task != "" && s.deliverToGoblin(ctx, r, "The Overlord answered on your review page "+r.LavishPage+": "+told) {
			relay, answered = "the goblin has it: "+bounded(asked, 2000), "You answered on its page; the goblin has it."
		}
		if err == nil {
			detail = on + "; his feedback is in " + saved + ", " + relay
		} else {
			s.publish(err)
			output := poll.Output
			if len(output) > pageFeedbackInline {
				cut := len(output) - pageFeedbackInline
				for cut < len(output) && !utf8.RuneStart(output[cut]) {
					cut++
				}
				output = "(cut to its end) ..." + output[cut:]
			}
			detail = fmt.Sprintf("%s, but his feedback could not be saved (%v); %s: %s", on, err, relay, output)
		}
		closed = Review{State: "answered", AnsweredBy: "overlord", AnsweredIn: "page", Reason: answered}
		if err := s.Store.answerQuestionsOnPage(r, asked); err != nil {
			s.publish(err)
		}
	case poll.Status == "ended" && poll.EndedBy == "agent":
		detail = "an agent, not the Overlord, ended the review of " + r.LavishPage + " before he answered on it"
		closed.Reason = "An agent ended the review on its page; the CFO was told."
	case poll.Status == "ended":
		detail = "the Overlord ended the review of " + r.LavishPage + " with no more feedback"
		closed = Review{State: "cleared", Reason: "You ended the review on its page."}
	default:
		detail = "lavish-axi reported " + poll.Status + " for the page " + r.LavishPage
		closed.Reason = "The page's session changed; the CFO was told."
	}
	if !s.queueReviewWake(ctx, key, detail) {
		return
	}
	if err := s.Store.closeReview(r.ID, closed); err != nil {
		s.publish(err)
	}
}

// savePageFeedback keeps a poll's whole output for the CFO to read, since
// delivery consumed it and lavish-axi holds no other copy.
func savePageFeedback(stateDir string, r Review, output string) (string, error) {
	dir := filepath.Join(stateDir, "reviews", "feedback")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, r.ID+"-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".toon")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(output, "\r\n", "\n")), 0o600); err != nil {
		return "", err
	}
	return path, nil
}
