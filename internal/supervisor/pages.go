package supervisor

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	// pagePollTimeout bounds one poll, so a wait that closes or a supervisor
	// that stops never leaves a poll running for long behind it.
	pagePollTimeout = 5 * time.Minute
	// pageAnswerSettle is how long a poll waits, once he sent something on a
	// page without ending its review, for the rest of his answer. Scrawl
	// sends a pick and a note on an element the moment he sends each, and
	// Send & End as a batch of its own, so a note and the approval after it
	// reach the supervisor in two polls: 757 ms apart on 2026-10-09, when his
	// pick was first read as a revision and his approval as a second answer.
	pageAnswerSettle = 2 * time.Second
	// pagePollAttempts is how many polls in a row may fail before the CFO is
	// told the page cannot be watched.
	pagePollAttempts = 3
	// pageFeedbackInline bounds feedback carried in a wake when it cannot be
	// saved; its end is kept.
	pageFeedbackInline = 4000
	// pageAnswerReceived is the reason an item he answered on its page gives
	// until his answer has been passed on and who has it is known.
	pageAnswerReceived = "You answered on its page."
)

// pagePollPause separates a failed poll, or a wake the queue refused, from
// the next attempt.
var pagePollPause = 10 * time.Second

// pageAnswer keeps consumed feedback and its delivery intent across restarts.
// A collecting answer remains open to the next poll's Send & End.
type pageAnswer struct {
	ID         string       `json:"id"`
	Poll       axi.PagePoll `json:"poll"`
	Item       Review       `json:"item"`
	IsAnswer   bool         `json:"is_answer,omitempty"`
	HasRetried bool         `json:"has_retried,omitempty"`
	Status     string       `json:"status"`
	Detail     string       `json:"detail,omitempty"`
	Reason     string       `json:"reason,omitempty"`
	IsPageEnd  bool         `json:"is_page_end,omitempty"`
	Closed     Review       `json:"closed,omitempty"`
}

// watchPages keeps one poller for each page the supervisor watches: the
// Lavish page of any item, open or closed, until the review on it settles,
// while the item is the CFO's own or its goblin's task stands. What becomes
// of the item, such as the goblin's withdraw of its card, never ends the
// watch, so nothing the Overlord sends on the page goes unread. lavish-axi
// hands a page's feedback to whichever poll takes it, so a page has one
// poller, named by its file, and the supervisor is the only one that polls.
func (s *Service) watchPages(ctx context.Context) {
	watched := s.Store.watchedPages()
	for _, answer := range s.Store.pageReceiptsSnapshot().Answers {
		key := pageKey(answer.Item.LavishPage)
		if answer.Status == "collecting" {
			if _, found := watched[key]; !found {
				watched[key] = answer.Item
			}
		} else {
			s.tellPage(ctx, key)
		}
	}
	if s.Options.PollPage == nil {
		return
	}
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
// the sweep. Between polls it takes the item that now stands for the page;
// each poll and collected answer stay bound to that version. A closed
// review window is not the end of the review: lavish-axi keeps the session,
// his answers queue on the page until a poll takes them, and reopening the
// page resumes the same review, so the poll goes on after a pause that keeps
// a window that keeps disconnecting from spinning it.
//
// What he sends is read as one answer with whatever he sends within
// pageAnswerSettle after it, so a note and the Send & End that approves it
// are one answer, never a revision and then an answer. An answer closes the
// page's open items the moment the poll reads it: one that ends the review or
// picks an option the page declared.
// What he sends without ending the review that answers nothing is a revision:
// its reporter makes the next version, the page says so, and the poll goes
// on. What he sends on a page whose item has closed is a note its goblin gets
// all the same. An agent's end of the review is never his: what he sent
// before it is passed on as it stands, and the next poll hands the agent's
// end to the CFO. A poll that fails while the rest of his answer may be on
// its way is asked once more at once, before what he sent is passed on
// without it. What the goblin and the CFO are told goes in order beside the
// poll, so the next poll never waits on a goblin's terminal, where a goblin
// in a turn takes typed text only at its next tool call, after up to 5 s.
func (s *Service) watchPage(ctx context.Context, key string) {
	failures := 0
	reply := ""
	var polled Review
	var answer *pageAnswer
	for _, pending := range s.Store.pageReceiptsSnapshot().Answers {
		if pending.Status == "collecting" && pageKey(pending.Item.LavishPage) == key {
			answer = &pending
			if answer.IsPageEnd || answer.Poll.Ended {
				if _, isQueued := s.passOnAnswer(ctx, *answer); !isQueued {
					return
				}
				answer = nil
				continue
			}
			if answer.IsAnswer {
				s.answeredOnPage(*answer)
			}
			break
		}
	}
	for {
		r, watched := s.Store.watchedPages()[key]
		if !watched {
			if answer != nil {
				s.passOnAnswer(ctx, *answer)
			}
			return
		}
		if answer != nil && !samePageVersion(answer.Item, r) {
			if _, isQueued := s.passOnAnswer(ctx, *answer); !isQueued {
				return
			}
			answer, reply = nil, ""
		}
		if !samePageVersion(polled, r) {
			reply = ""
		}
		timeout := pagePollTimeout
		if answer != nil {
			timeout = pageAnswerSettle
		}
		polled = r
		poll, err := s.Options.PollPage(ctx, r.LavishPage, reply, timeout)
		if now, ok := s.Store.review(r.ID); ok && samePageVersion(now, r) {
			r = now
		}
		if err == nil {
			reply = ""
		}
		if err == nil && (poll.Status == "feedback" || answer != nil && poll.Status == "ended" && poll.EndedBy != "agent") {
			failures = 0
			if answer == nil {
				answer = &pageAnswer{ID: rand.Text(), Poll: poll, Item: r, Status: "collecting"}
			} else {
				answer.Poll = together(answer.Poll, poll)
			}
			if answer.Item.State == "open" && !answer.IsAnswer && (answer.Poll.Ended && answer.Poll.EndedBy != "agent" || answer.Poll.Picked) {
				answer.IsAnswer = true
			}
			if !s.keepAnswer(ctx, *answer) {
				return
			}
			if answer.IsAnswer {
				s.answeredOnPage(*answer)
			}
			// No receipt yet: passOnAnswer gives the one receipt once the answer
			// settles, saying whether it was a revision.
			if !answer.Poll.Ended && ctx.Err() == nil {
				continue
			}
		}
		if ctx.Err() != nil {
			if answer != nil {
				s.passOnAnswer(ctx, *answer)
			}
			return
		}
		if err != nil && answer != nil && !answer.HasRetried {
			// Scrawl keeps what he sent for the next poll, which starts it
			// again when it stopped: it stops as its last review ends with
			// no window and no poll connected, failing the poll under way.
			answer.HasRetried = true
			if !s.keepAnswer(ctx, *answer) {
				return
			}
			continue
		}
		if answer != nil {
			whole := *answer
			answer = nil
			var isQueued bool
			reply, isQueued = s.passOnAnswer(ctx, whole)
			if !isQueued {
				return
			}
			if reply == "" {
				if now, ok := s.Store.watchedPages()[key]; ok && !samePageVersion(now, whole.Item) {
					continue
				}
				return
			}
			if err == nil && (poll.Status == "ended" || poll.Ended) && poll.EndedBy == "agent" {
				poll.Status = "ended"
				s.handPageToCFO(ctx, r, poll, nil)
				if now, ok := s.Store.watchedPages()[key]; ok && !samePageVersion(now, r) {
					continue
				}
				return
			}
			if err == nil && poll.Status != "browser_disconnected" {
				continue
			}
		}
		if err == nil && (poll.Status == "waiting" || poll.Status == "browser_disconnected") {
			failures = 0
			if poll.Status == "browser_disconnected" {
				if err := s.Store.windowClosed(r, time.Now().UTC()); err != nil {
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
		if now, ok := s.Store.watchedPages()[key]; !ok || samePageVersion(now, r) {
			return
		}
	}
}

// together reads what he sent on a page in two polls as one send: its prompts
// in the order he sent them, both outputs kept whole, and the review ended
// when the later poll ended it.
func together(first, then axi.PagePoll) axi.PagePoll {
	whole := first
	whole.Prompts = append(slices.Clone(first.Prompts), then.Prompts...)
	whole.Picked = first.Picked || then.Picked
	whole.Output = first.Output + "\n" + then.Output
	if then.Ended || then.Status == "ended" {
		whole.Ended, whole.EndedBy = true, then.EndedBy
	}
	return whole
}

// answeredOnPage closes the captured version and its older cards the moment
// a poll reads his answer there, with what he wrote, before anyone is told
// it, and nothing else: a question its goblin asked stays its own. The boards
// are told at once: the supervisor's cycle tells them only of what the cycle
// itself changed.
func (s *Service) answeredOnPage(answer pageAnswer) {
	if err := s.Store.closePage(answer.Item, Review{State: "answered", AnsweredBy: "overlord", AnsweredIn: "page", Reason: pageAnswerReceived, Answer: pageWords(answer.Poll)}); err != nil {
		s.publish(err)
	}
	s.notify()
}

// passOnAnswer releases a settled answer to its durable, ordered teller.
// Polling and its receipt never wait for a terminal to take the answer.
func (s *Service) passOnAnswer(ctx context.Context, answer pageAnswer) (reply string, isQueued bool) {
	r := answer.Item
	if answer.IsPageEnd {
		if err := s.Store.settlePage(r, answer.Closed); err != nil {
			s.publish(err)
			return "", false
		}
	} else if answer.IsAnswer {
		s.answeredOnPage(answer)
	} else if r.State == "open" {
		if err := s.Store.reviseReview(r, time.Now().UTC()); err != nil {
			s.publish(err)
		}
		s.notify()
	}
	if answer.IsAnswer && answer.Poll.Ended {
		if err := s.Store.settlePage(r, Review{}); err != nil {
			s.publish(err)
		}
	}
	answer.Status = "queued"
	if !s.keepAnswer(ctx, answer) {
		return "", false
	}
	s.tellPage(ctx, pageKey(r.LavishPage))
	if answer.IsPageEnd || answer.IsAnswer && answer.Poll.Ended {
		return "", true
	}
	if r.State == "open" && !answer.IsAnswer {
		maker := "The CFO makes"
		if r.Task != "" {
			maker = r.Task + " makes"
		}
		return "Revision received. " + maker + " the next version, which replaces this page.", true
	}
	return "Received.", true
}

// handPageToCFO wakes the CFO with what became of an item's page when its
// review ended with no answer on it, or the page cannot be polled, settles the
// page and closes every open item naming it, so the board never shows the
// page as waiting on him once its review has ended. The CFO's own page has no
// goblin, so its wake is keyed by the item. An item he ended on its page
// closes as his clear; only an item that closed without his word is
// withdrawn. A page whose items had all closed already settles with no wake:
// whatever he sent on it was passed on as it came.
func (s *Service) handPageToCFO(ctx context.Context, r Review, poll axi.PagePoll, pollErr error) {
	if r.State != "open" {
		if err := s.Store.settlePage(r, Review{}); err != nil {
			s.publish(err)
		}
		return
	}
	var detail string
	closed := Review{State: "withdrawn"}
	switch {
	case pollErr != nil:
		detail = fmt.Sprintf("the supervisor cannot poll the page %s (%v); the Overlord's answer on it reaches nobody, so ask him in text", r.LavishPage, pollErr)
		closed.Reason = "The page could not be polled; the CFO was told."
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
	answer := pageAnswer{ID: rand.Text(), Item: r, Poll: poll, Status: "collecting", Detail: detail, IsPageEnd: true, Closed: closed}
	if !s.keepAnswer(ctx, answer) {
		return
	}
	s.passOnAnswer(ctx, answer)
}

// pageKey names a page by its file the way Windows does, in any case.
func pageKey(file string) string {
	return strings.ToLower(filepath.Clean(file))
}

func samePageVersion(a, b Review) bool {
	return a.ID == b.ID && a.Identity == b.Identity && a.Task == b.Task && a.CreatedAt.Equal(b.CreatedAt) && pageKey(a.LavishPage) == pageKey(b.LavishPage)
}

// A page answer also closes its reporter's older cards for that page, but
// never a replacement version of the captured item or a newer card.
func pageVersionIncludes(item, r Review) bool {
	return r.LavishPage != "" && pageKey(r.LavishPage) == pageKey(item.LavishPage) && r.Identity == item.Identity && r.Task == item.Task && !r.CreatedAt.After(item.CreatedAt) && (r.ID != item.ID || r.CreatedAt.Equal(item.CreatedAt))
}

// watchesPage reports whether the supervisor watches item r's page: the
// review on it has not settled, and the item is the CFO's own or its goblin's
// task stands. A retired goblin's page is the sweep's to end.
func (s *Store) watchesPage(r Review) bool {
	return r.LavishPage != "" && r.PageSettled == nil && (r.Task == "" || s.taskStands(r.Task))
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

// closePage closes the captured version and its older cards as closed says,
// and leaves their page watched.
func (s *Store) closePage(item Review, closed Review) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := time.Now().UTC()
	changed := false
	for i := range s.db.Reviews {
		r := &s.db.Reviews[i]
		if r.State != "open" || r.PageSettled != nil || !pageVersionIncludes(item, *r) {
			continue
		}
		r.State, r.Reason, r.AnsweredBy, r.AnsweredIn, r.Answer, r.UpdatedAt = closed.State, closed.Reason, closed.AnsweredBy, closed.AnsweredIn, closed.Answer, at
		if r.State == "cleared" {
			s.auditClear(*r)
		}
		changed = true
	}
	if !changed {
		return nil
	}
	return s.save()
}

// pageWords is what he wrote on a page in one poll's answer, his prompts one
// to a line, bounded as an item's reason is.
func pageWords(poll axi.PagePoll) string {
	return bounded(strings.Join(poll.Prompts, "\n"), 2000)
}

// explainAnswer gives each item of the page key names that closed when he
// answered there, and says only that, the reason that says who has his
// answer, now that it was passed on, and all he wrote with it.
func (s *Store) explainAnswer(item Review, reason, words string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for i := range s.db.Reviews {
		r := &s.db.Reviews[i]
		if r.State != "answered" || r.Reason != pageAnswerReceived || !pageVersionIncludes(item, *r) {
			continue
		}
		r.Reason, r.Answer, changed = reason, words, true
	}
	if !changed {
		return nil
	}
	return s.save()
}

// settlePage settles the captured version and its older cards, and closes
// each still open as closed says, when it says anything.
func (s *Store) settlePage(item Review, closed Review) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := time.Now().UTC()
	changed := false
	for i := range s.db.Reviews {
		r := &s.db.Reviews[i]
		if r.PageSettled != nil || !pageVersionIncludes(item, *r) {
			continue
		}
		r.PageSettled, changed = &at, true
		if r.State == "open" && closed.State != "" {
			r.State, r.Reason, r.AnsweredBy, r.AnsweredIn, r.UpdatedAt = closed.State, closed.Reason, closed.AnsweredBy, closed.AnsweredIn, at
			if r.State == "cleared" {
				s.auditClear(*r)
			}
		}
	}
	if !changed {
		return nil
	}
	return s.save()
}
