package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/state"
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

// watchPages keeps one poller for each page the supervisor watches: the
// Lavish page of any item, open or closed, until the review on it settles,
// while the item is the CFO's own or its goblin's task stands. What becomes
// of the item, a later report that withdraws its card or an answer to the
// goblin's question that closes it, never ends the watch, so nothing the
// Overlord sends on the page goes unread. lavish-axi hands a page's feedback
// to whichever poll takes it, so a page has one poller, named by its file,
// and the supervisor is the only one that polls.
func (s *Service) watchPages(ctx context.Context) {
	if s.Options.PollPage == nil {
		return
	}
	watched := s.Store.watchedPages()
	s.pagesMu.Lock()
	defer s.pagesMu.Unlock()
	if s.pages == nil {
		s.pages = map[string]bool{}
	}
	for key := range watched {
		if s.pages[key] || ctx.Err() != nil {
			continue
		}
		s.pages[key] = true
		s.pageWork.Add(1)
		go func() {
			defer s.pageWork.Done()
			s.watchPage(ctx, key)
			// A page no longer watched, such as a retired goblin's, is the
			// next cycle's sweep, so it says why soon rather than in minutes.
			s.pagesMu.Lock()
			delete(s.pages, key)
			s.pageSwept = time.Time{}
			s.pagesMu.Unlock()
		}()
	}
}

// watchPage polls one page until the review on it settles, then gives the
// CFO what happened and closes the page's open items, or until the page is no
// longer watched because its goblin's task was retired, which leaves it to
// the sweep. Between polls it takes the item that now stands for the page,
// so a newer item, or one that closed meanwhile, is the one answered. A closed
// review window is not the end of the review: lavish-axi keeps the session,
// his answers queue on the page until a poll takes them, and reopening the
// page resumes the same review, so the poll goes on after a pause that keeps
// a window that keeps disconnecting from spinning it. What he sends without
// ending the review is a revision, unless the page carries a question it
// answers: its reporter makes the next version, the page says so, and the
// poll goes on. On a page whose item has closed it is a note its goblin gets
// all the same.
func (s *Service) watchPage(ctx context.Context, key string) {
	failures := 0
	reply := ""
	for {
		r, watched := s.Store.watchedPages()[key]
		if !watched {
			return
		}
		poll, err := s.Options.PollPage(ctx, r.LavishPage, reply, pagePollTimeout)
		if ctx.Err() != nil {
			return
		}
		if now, ok := s.Store.watchedPages()[key]; ok {
			r = now
		} else if now, ok := s.Store.review(r.ID); ok {
			r = now
		}
		if err == nil {
			reply = ""
		}
		if err == nil && poll.Status == "feedback" && r.State != "open" {
			failures = 0
			wakeKey := r.Task
			if wakeKey == "" {
				wakeKey = r.ID
			}
			// The next poll shows him who has it, and ends the watch when
			// he ended the review.
			if reply = s.passOnPageFeedback(ctx, r.LavishPage, r.Task, r.Identity, wakeKey, poll); reply == "" {
				return
			}
			continue
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
	if saved, err := savePageFeedback(s.Store.Home.State, r.ID, poll.Output); err == nil {
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

// handPageToCFO wakes the CFO with what became of an item's page, settles the
// page and closes every open item naming it, so the board never shows the
// page as waiting on him once its review has ended. His answer reaches a
// goblin in its own terminal and the CFO is told; when the goblin cannot take
// it, the CFO relays it, and the CFO's own page has no goblin, so its wake is
// keyed by the item. The poll consumed the page's feedback, so the wake is
// retried until the CFO has it, and the item stays open until then. An item
// the Overlord answered on its page closes answered, by him, on the page, and
// one he ended there closes as his clear; only an item that closed without
// his word is withdrawn. A page whose items had all closed already settles
// with no wake: whatever he sent on it was passed on as it came.
func (s *Service) handPageToCFO(ctx context.Context, r Review, poll axi.PagePoll, pollErr error) {
	if r.State != "open" {
		if err := s.Store.settlePage(pageKey(r.LavishPage), Review{}); err != nil {
			s.publish(err)
		}
		return
	}
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
		saved, err := savePageFeedback(s.Store.Home.State, r.ID, poll.Output)
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
	if err := s.Store.settlePage(pageKey(r.LavishPage), closed); err != nil {
		s.publish(err)
	}
}

// passOnPageFeedback hands what the Overlord sent on a page nobody waits on
// any more, its item closed or never made, to the goblin of task while it
// runs as identity, else to the CFO, and returns the reply the page shows him:
// who has it. The CFO is told either way, by a review wake keyed by key, and
// acts on it itself for its own page (no task) or a retired goblin's. The
// poll consumed the feedback, so the wake is retried until the CFO has it; an
// empty reply means the caller stopped first.
func (s *Service) passOnPageFeedback(ctx context.Context, page, task, identity, key string, poll axi.PagePoll) string {
	feedback := "his feedback is in "
	if saved, err := savePageFeedback(s.Store.Home.State, key, poll.Output); err == nil {
		feedback += saved
	} else {
		s.publish(err)
		feedback = "his feedback could not be saved (" + err.Error() + "): " + bounded(poll.Output, pageFeedbackInline)
	}
	asked := strings.Join(poll.Prompts, "\n")
	if asked == "" {
		asked = feedback
	}
	detail, reply := "the Overlord wrote on your page "+page+"; "+feedback+", act on it", "Received. The CFO has it."
	switch {
	case task == "":
	case !s.Store.taskStands(task):
		detail, reply = "the Overlord wrote on the page "+page+" of "+task+", which has been retired; "+feedback+", act on it", "Received. "+task+" has been retired, so the CFO has it."
	case s.deliverToGoblin(ctx, Review{Task: task, Identity: identity}, fmt.Sprintf("The Overlord wrote on your review page %s: %s\nTo show him a next version, change the same file and run cfo notify %s --waiting-on overlord \"<why>\" --lavish %s.", page, asked, task, page)):
		detail, reply = "the Overlord wrote on the page "+page+", and "+task+" has it: "+asked, "Received. "+task+" has it."
	default:
		detail, reply = "the Overlord wrote on the page "+page+"; "+feedback+", relay it to "+task, "Received. The CFO has it and passes it to "+task+"."
	}
	if !s.queueReviewWake(ctx, key, bounded(detail, 4000)) {
		return ""
	}
	return reply
}

// savePageFeedback keeps a poll's whole output for the CFO to read, since
// delivery consumed it and lavish-axi holds no other copy, in a file named
// for name and the time.
func savePageFeedback(stateDir, name, output string) (string, error) {
	dir := filepath.Join(stateDir, "reviews", "feedback")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+"-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".toon")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(output, "\r\n", "\n")), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// pageKey names a page by its file the way Windows does, in any case.
func pageKey(file string) string {
	return strings.ToLower(filepath.Clean(file))
}

// watchesPage reports whether the supervisor watches item r's page: the
// review on it has not settled, and the item is the CFO's own or its goblin's
// task stands. A retired goblin's page is the sweep's to end.
func (s *Store) watchesPage(r Review) bool {
	return r.LavishPage != "" && r.PageSettled == nil && r.Document == nil && (r.Task == "" || s.taskStands(r.Task))
}

// taskStands reports whether task id stands, running or paused: its record is
// there. A record that cannot be checked stands, the safe side.
func (s *Store) taskStands(id string) bool {
	_, err := os.Stat(state.TaskMetaPath(s.Home.State, id))
	return !errors.Is(err, fs.ErrNotExist)
}

// watchedPages maps the key of each page the supervisor watches to the item
// that stands for it: the newest open item naming it, else the newest item.
func (s *Store) watchedPages() map[string]Review {
	pages := map[string]Review{}
	for _, r := range s.Snapshot().Reviews {
		if !s.watchesPage(r) {
			continue
		}
		key := pageKey(r.LavishPage)
		prior, found := pages[key]
		if !found || (r.State == "open") != (prior.State == "open") && r.State == "open" || (r.State == "open") == (prior.State == "open") && !r.CreatedAt.Before(prior.CreatedAt) {
			pages[key] = r
		}
	}
	return pages
}

// review returns item id as it stands now.
func (s *Store) review(id string) (Review, bool) {
	reviews := s.Snapshot().Reviews
	i := slices.IndexFunc(reviews, func(r Review) bool { return r.ID == id })
	if i < 0 {
		return Review{}, false
	}
	return reviews[i], true
}

// settlePage records that the review on the page key names has settled, on
// every item naming it, so nothing watches it any more, and closes each of
// them still open as closed says, when it says anything.
func (s *Store) settlePage(key string, closed Review) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := time.Now().UTC()
	changed := false
	for i := range s.db.Reviews {
		r := &s.db.Reviews[i]
		if r.LavishPage == "" || r.PageSettled != nil || pageKey(r.LavishPage) != key {
			continue
		}
		r.PageSettled, changed = &at, true
		if r.State == "open" && closed.State != "" {
			r.State, r.Reason, r.AnsweredBy, r.AnsweredIn, r.UpdatedAt = closed.State, closed.Reason, closed.AnsweredBy, closed.AnsweredIn, at
		}
	}
	if !changed {
		return nil
	}
	return s.save()
}
