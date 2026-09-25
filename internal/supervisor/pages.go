package supervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/axi"
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

// watchPage polls one item's page until the Overlord answers on it, ends it,
// or leaves it, then gives the CFO what happened and closes the item.
func (s *Service) watchPage(ctx context.Context, r Review) {
	failures := 0
	for {
		poll, err := s.Options.PollPage(ctx, r.LavishPage, pagePollTimeout)
		if ctx.Err() != nil {
			return
		}
		if err == nil && poll.Status == "waiting" {
			failures = 0
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

// handPageToCFO wakes the CFO with what became of an item's page and closes
// the item: the CFO relays a goblin's answer, and whoever needs another look
// opens a new item. The poll consumed the page's feedback, so the wake is
// retried until the CFO has it, and the item stays open until then. The CFO's
// own page has no goblin to key or relay to, so its wake is keyed by the item.
func (s *Service) handPageToCFO(ctx context.Context, r Review, poll axi.PagePoll, pollErr error) {
	var detail, reason string
	relay, key, answered := "relay it to the goblin", r.Task, "The Overlord answered on the page; the CFO relays it."
	if r.Task == "" {
		relay, key, answered = "act on it", r.ID, "The Overlord answered on the page; the CFO has it."
	}
	switch {
	case pollErr != nil:
		detail = fmt.Sprintf("the supervisor cannot poll the page %s (%v); the Overlord's answer on it reaches nobody, so ask him in text", r.LavishPage, pollErr)
		reason = "The page could not be polled; the CFO was told."
	case poll.Status == "feedback":
		if saved, err := savePageFeedback(s.Store.Home.State, r, poll.Output); err == nil {
			detail = "the Overlord answered on the page " + r.LavishPage + "; his feedback is in " + saved + ", " + relay
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
			detail = fmt.Sprintf("the Overlord answered on the page %s, but his feedback could not be saved (%v); %s: %s", r.LavishPage, err, relay, output)
		}
		if poll.Ended {
			detail += "; he ended the review"
		}
		reason = answered
	case poll.Status == "ended":
		detail = "the Overlord ended the review of " + r.LavishPage + " with no more feedback"
		reason = "The Overlord ended the review."
	case poll.Status == "browser_disconnected":
		detail = "the Overlord's review window for " + r.LavishPage + " disconnected; ask him whether to reopen it or end it"
		reason = "The review window disconnected; the CFO was told."
	default:
		detail = "lavish-axi reported " + poll.Status + " for the page " + r.LavishPage
		reason = "The page's session changed; the CFO was told."
	}
	for {
		_, err := wake.Append(s.Store.Home.State, "review", key, detail)
		if err == nil {
			break
		}
		s.publish(err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(pagePollPause):
		}
	}
	if _, err := wake.PublishEpisode(s.Store.Home.State); err != nil {
		s.publish(err)
	}
	if err := s.Store.withdrawReview(r.ID, reason); err != nil {
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
