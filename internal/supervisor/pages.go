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
	// pageTellings bounds what a page's poller hands its teller before it
	// waits for the teller to catch up.
	pageTellings = 16
	// pageAnswerReceived is the reason an item he answered on its page gives
	// until his answer has been passed on and who has it is known.
	pageAnswerReceived = "You answered on its page."
)

// pagePollPause separates a failed poll, or a wake the queue refused, from
// the next attempt.
var pagePollPause = 10 * time.Second

// pageAnswer is what he sent on a page that the supervisor has not yet passed
// on: his sends so far, read as one, the page's item as it stood when he
// began, whether they answer that item, and whether a poll for the rest of
// them failed and was asked again.
type pageAnswer struct {
	poll       axi.PagePoll
	item       Review
	isAnswer   bool
	hasRetried bool
}

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
// a window that keeps disconnecting from spinning it.
//
// What he sends is read as one answer with whatever he sends within
// pageAnswerSettle after it, so a note and the Send & End that approves it
// are one answer, never a revision and then an answer. An answer closes the
// page's open items the moment the poll reads it: one that ends the review,
// picks an option the page declared, or answers a question the page carries.
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
	tell := make(chan func(), pageTellings)
	told := make(chan struct{})
	go func() {
		defer close(told)
		for telling := range tell {
			telling()
		}
	}()
	defer func() {
		close(tell)
		<-told
	}()
	failures := 0
	reply := ""
	var answer *pageAnswer
	for {
		r, watched := s.Store.watchedPages()[key]
		if !watched {
			if answer != nil {
				s.passOnAnswer(ctx, *answer, tell)
			}
			return
		}
		timeout := pagePollTimeout
		if answer != nil {
			timeout = pageAnswerSettle
		}
		poll, err := s.Options.PollPage(ctx, r.LavishPage, reply, timeout)
		if ctx.Err() != nil {
			// A poll took what he sent, and Scrawl holds no other copy.
			if answer != nil {
				s.passOnAnswer(ctx, *answer, tell)
			}
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
		if err == nil && (poll.Status == "feedback" || answer != nil && poll.Status == "ended" && poll.EndedBy != "agent") {
			failures = 0
			if answer == nil {
				answer = &pageAnswer{poll: poll, item: r}
			} else {
				answer.poll = together(answer.poll, poll)
			}
			if answer.item.State == "open" && !answer.isAnswer && (answer.poll.Ended || answer.poll.Picked || s.Store.asksOnPage(answer.item)) {
				answer.isAnswer = true
				s.answeredOnPage(answer.item, answer.poll)
			}
			if !answer.poll.Ended {
				continue
			}
		}
		if err != nil && answer != nil && !answer.hasRetried {
			// Scrawl keeps what he sent for the next poll, which starts it
			// again when it stopped: it stops as its last review ends with
			// no window and no poll connected, failing the poll under way.
			answer.hasRetried = true
			continue
		}
		if answer != nil {
			whole := *answer
			answer = nil
			if reply = s.passOnAnswer(ctx, whole, tell); reply == "" {
				return
			}
			if err == nil && poll.Status != "browser_disconnected" {
				continue
			}
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

// answeredOnPage closes every open item naming the page of item r the moment
// a poll reads his answer there, before anyone is told it, and closes the
// questions r's page carries with what he wrote. The boards are told at once:
// the supervisor's cycle tells them only of what the cycle itself changed.
func (s *Service) answeredOnPage(r Review, poll axi.PagePoll) {
	if err := s.Store.closePage(pageKey(r.LavishPage), Review{State: "answered", AnsweredBy: "overlord", AnsweredIn: "page", Reason: pageAnswerReceived}); err != nil {
		s.publish(err)
	}
	if err := s.Store.answerQuestionsOnPage(r, strings.Join(poll.Prompts, "\n")); err != nil {
		s.publish(err)
	}
	s.notify()
}

// passOnAnswer passes on his answer on the page of answer.item and returns the
// reply the page shows him, or "" when the watch ends: he ended the review on
// an item he answered, or the poller stopped first. What he sent on an item
// that had closed before he began is a note its goblin gets, told after
// everything the page's teller holds, and its reply waits on that delivery.
func (s *Service) passOnAnswer(ctx context.Context, answer pageAnswer, tell chan<- func()) string {
	r := answer.item
	switch {
	case r.State != "open":
		key := r.Task
		if key == "" {
			key = r.ID
		}
		replied := make(chan string, 1)
		tell <- func() { replied <- s.passOnPageFeedback(ctx, r.LavishPage, r.Task, r.Identity, key, answer.poll) }
		return <-replied
	case answer.isAnswer:
		return s.takeAnswer(ctx, r, answer.poll, tell)
	default:
		return s.takeRevision(ctx, r, answer.poll, tell)
	}
}

// takeAnswer passes on his answer on the page of item r, which closed the
// moment the poll read it: its goblin gets it in its own terminal and the CFO
// is told, or the CFO gets it to act on for its own page, and the item then
// says who has it. The poll consumed the answer, so the wake is retried until
// the CFO has it. An answer that ended the review settles the page and ends
// the watch; otherwise the page stays watched for what he sends next, and
// its reply tells him who his answer goes to.
func (s *Service) takeAnswer(ctx context.Context, r Review, poll axi.PagePoll, tell chan<- func()) string {
	asked := strings.Join(poll.Prompts, "\n")
	saved, saveErr := savePageFeedback(s.Store.Home.State, r.ID, poll.Output)
	if saveErr != nil {
		s.publish(saveErr)
	}
	tell <- func() {
		on := "the Overlord answered on the page " + r.LavishPage
		if poll.Ended {
			on += " and ended the review"
		}
		relay, key, answered := "relay it to the goblin", r.Task, "You answered on its page; the CFO relays it to the goblin."
		if r.Task == "" {
			relay, key, answered = "act on it", r.ID, "You answered on its page; the CFO has it."
		}
		told := asked
		if told == "" {
			told = "his feedback is in " + saved
		}
		if r.Task != "" && s.deliverToGoblin(ctx, r, "The Overlord answered on your review page "+r.LavishPage+": "+told) {
			relay, answered = "the goblin has it: "+bounded(asked, 2000), "You answered on its page; the goblin has it."
		}
		detail := on + "; his feedback is in " + saved + ", " + relay
		if saveErr != nil {
			output := poll.Output
			if len(output) > pageFeedbackInline {
				cut := len(output) - pageFeedbackInline
				for cut < len(output) && !utf8.RuneStart(output[cut]) {
					cut++
				}
				output = "(cut to its end) ..." + output[cut:]
			}
			detail = fmt.Sprintf("%s, but his feedback could not be saved (%v); %s: %s", on, saveErr, relay, output)
		}
		if !s.queueReviewWake(ctx, key, detail) {
			return
		}
		if err := s.Store.explainAnswer(pageKey(r.LavishPage), answered); err != nil {
			s.publish(err)
		}
	}
	if poll.Ended {
		if err := s.Store.settlePage(pageKey(r.LavishPage), Review{}); err != nil {
			s.publish(err)
		}
		return ""
	}
	if r.Task == "" {
		return "Received. The CFO has your answer."
	}
	return "Received. Your answer goes to " + r.Task + "."
}

// takeRevision hands the Overlord's revision on an item's page to its
// reporter and marks the item as waiting on the next version at once, which
// the boards are told, and returns the reply the page shows him: that the
// revision was received and what happens next. A goblin gets the revision in
// its own terminal, and the CFO is told; when the goblin cannot take it, or
// the page is the CFO's own, the CFO gets it to act on. The poll consumed the
// revision, so the wake is retried until the CFO has it.
func (s *Service) takeRevision(ctx context.Context, r Review, poll axi.PagePoll, tell chan<- func()) string {
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
	if err := s.Store.reviseReview(r.ID, time.Now().UTC()); err != nil {
		s.publish(err)
	}
	s.notify()
	maker := "The CFO makes"
	if r.Task != "" {
		maker = r.Task + " makes"
	}
	tell <- func() {
		key, detail := r.ID, "the Overlord asked for a revision on the page "+r.LavishPage+"; "+feedback+", act on it"
		if r.Task != "" {
			key, detail = r.Task, "the Overlord asked for a revision on the page "+r.LavishPage+"; "+feedback+", relay it to the goblin"
			if s.deliverToGoblin(ctx, r, fmt.Sprintf("The Overlord asked for a revision on your review page %s: %s\nMake the next version in the same file, then run cfo notify %s --waiting-on overlord \"<why>\" --lavish %s again: it replaces the page in the same review.", r.LavishPage, asked, r.Task, r.LavishPage)) {
				detail = "the Overlord asked for a revision on the page " + r.LavishPage + ", and " + r.Task + " has it and makes the next version: " + asked
			}
		}
		s.queueReviewWake(ctx, key, bounded(detail, 4000))
	}
	return "Revision received. " + maker + " the next version, which replaces this page."
}

// deliverToGoblin types text into the goblin's own terminal, as a board
// answer reaches it, and reports whether the goblin has it; one queued for
// its next tool call counts.
func (s *Service) deliverToGoblin(ctx context.Context, r Review, text string) bool {
	if s.Options.CFO == nil {
		return false
	}
	_, err := s.Options.CFO.SendGoblin(ctx, r.Task, r.Identity, text)
	if err != nil && !errors.Is(err, fleet.ErrQueuedForToolCall) {
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
		if err := s.Store.settlePage(pageKey(r.LavishPage), Review{}); err != nil {
			s.publish(err)
		}
		return
	}
	var detail string
	key := r.Task
	if key == "" {
		key = r.ID
	}
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

// closePage closes every open item naming the page key names as closed says,
// and leaves the page watched.
func (s *Store) closePage(key string, closed Review) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := time.Now().UTC()
	changed := false
	for i := range s.db.Reviews {
		r := &s.db.Reviews[i]
		if r.State != "open" || r.LavishPage == "" || r.PageSettled != nil || pageKey(r.LavishPage) != key {
			continue
		}
		r.State, r.Reason, r.AnsweredBy, r.AnsweredIn, r.UpdatedAt = closed.State, closed.Reason, closed.AnsweredBy, closed.AnsweredIn, at
		changed = true
	}
	if !changed {
		return nil
	}
	return s.save()
}

// explainAnswer gives each item of the page key names that closed when he
// answered there, and says only that, the reason that says who has his
// answer, now that it was passed on.
func (s *Store) explainAnswer(key, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for i := range s.db.Reviews {
		r := &s.db.Reviews[i]
		if r.State != "answered" || r.Reason != pageAnswerReceived || pageKey(r.LavishPage) != key {
			continue
		}
		r.Reason, changed = reason, true
	}
	if !changed {
		return nil
	}
	return s.save()
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
