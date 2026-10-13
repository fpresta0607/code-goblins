package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// Receipts have their own journal: valid grouped Scrawl feedback can exceed
// the board database's size bound. Terminal intent and holds commit together.
type pageReceiptState struct {
	Answers       []pageAnswer      `json:"answers"`
	DeliveryHolds map[string]string `json:"delivery_holds,omitempty"`
}

func (r pageReceiptState) clone() pageReceiptState {
	r.Answers = slices.Clone(r.Answers)
	for i := range r.Answers {
		r.Answers[i].Poll.Prompts = slices.Clone(r.Answers[i].Poll.Prompts)
		r.Answers[i].Item.ImageSums = slices.Clone(r.Answers[i].Item.ImageSums)
		r.Answers[i].Questions = slices.Clone(r.Answers[i].Questions)
	}
	r.DeliveryHolds = maps.Clone(r.DeliveryHolds)
	return r
}

func (s *Store) pageReceiptsPath() string {
	return filepath.Join(s.Home.State, ".page-receipts.json")
}

func (s *Store) readPageReceipts() error {
	data, err := fsx.ReadFile(s.pageReceiptsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.pageReceipts); err != nil {
		return fmt.Errorf("page receipt journal is corrupt: %w", err)
	}
	return nil
}

func (s *Store) pageReceiptsSnapshot() pageReceiptState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pageReceipts.clone()
}

func (s *Store) savePageReceipts(receipts pageReceiptState) error {
	data, err := json.Marshal(receipts)
	if err == nil {
		err = fsx.AtomicWriteFile(s.pageReceiptsPath(), append(data, '\n'))
	}
	if err != nil {
		return fmt.Errorf("%w: page receipts: %v", ErrStorage, err)
	}
	s.pageReceipts = receipts.clone()
	return nil
}

func (s *Store) keepPageAnswer(answer pageAnswer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipts := s.pageReceipts.clone()
	index := slices.IndexFunc(receipts.Answers, func(prior pageAnswer) bool { return prior.ID == answer.ID })
	if index < 0 {
		receipts.Answers = append(receipts.Answers, answer)
	} else {
		receipts.Answers[index] = answer
	}
	if answer.Status == "uncertain" {
		if receipts.DeliveryHolds == nil {
			receipts.DeliveryHolds = map[string]string{}
		}
		receipts.DeliveryHolds[pageKey(answer.Item.LavishPage)] = answer.Item.Identity
	}
	return s.savePageReceipts(receipts)
}

func (s *Store) forgetPageAnswer(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipts := s.pageReceipts.clone()
	receipts.Answers = slices.DeleteFunc(receipts.Answers, func(answer pageAnswer) bool { return answer.ID == id })
	return s.savePageReceipts(receipts)
}

// Receipt precedes acknowledgment, readable copies, and every external delivery.
// Recovery always rewrites readable feedback from the authoritative journal.
func (s *Service) keepAnswer(ctx context.Context, answer pageAnswer) bool {
	for attempt := 0; attempt < pagePollAttempts; attempt++ {
		if err := s.Store.keepPageAnswer(answer); err == nil {
			if answer.Status == "collecting" && !answer.IsPageEnd {
				if _, err := s.savePageAnswer(answer); err != nil {
					s.publish(err)
				}
			}
			return true
		} else {
			s.publish(err)
		}
		if attempt == pagePollAttempts-1 || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(pagePollPause):
		}
	}
	return false
}

func (s *Service) savePageAnswer(answer pageAnswer) (string, error) {
	name := answer.Item.ID
	if answer.Item.State != "open" && answer.Item.Task != "" {
		name = answer.Item.Task
	}
	dir := filepath.Join(s.Store.Home.State, "reviews", "feedback")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+"-"+answer.ID+".toon")
	output := strings.ReplaceAll(answer.Poll.Output, "\r\n", "\n")
	if prior, err := fsx.ReadFile(path); err == nil && string(prior) == output {
		return path, nil
	}
	return path, fsx.AtomicWriteFile(path, []byte(output))
}

// Only the teller performs terminal delivery. It reads the durable queue so
// a busy terminal cannot fill an in-memory channel and stop page polling.
func (s *Service) tellPage(ctx context.Context, key string) {
	s.pagesMu.Lock()
	defer s.pagesMu.Unlock()
	if s.pageTellers == nil {
		s.pageTellers = map[string]bool{}
	}
	if s.pageTellers[key] {
		return
	}
	s.pageTellers[key] = true
	s.pageWork.Add(1)
	go func() {
		defer s.pageWork.Done()
		for {
			s.pagesMu.Lock()
			answers := s.Store.pageReceiptsSnapshot().Answers
			index := slices.IndexFunc(answers, func(answer pageAnswer) bool { return pageKey(answer.Item.LavishPage) == key })
			if index < 0 || answers[index].Status == "collecting" {
				delete(s.pageTellers, key)
				s.pagesMu.Unlock()
				return
			}
			s.pagesMu.Unlock()
			if err := s.tellAnswer(ctx, answers[index]); err != nil {
				s.publish(err)
				s.pagesMu.Lock()
				delete(s.pageTellers, key)
				s.pagesMu.Unlock()
				return
			}
		}
	}()
}

func (s *Service) tellAnswer(ctx context.Context, answer pageAnswer) error {
	r := answer.Item
	if answer.Status == "sending" {
		answer.Status = "uncertain"
	}
	if answer.Status != "notifying" {
		if !answer.IsPageEnd {
			text, detail, delivered, reason, deliveredReason, queued := s.pageMessage(answer)
			isHeld := false
			if identity, found := s.Store.pageReceiptsSnapshot().DeliveryHolds[pageKey(r.LavishPage)]; found && identity == r.Identity {
				isHeld = true
			}
			if answer.Status == "uncertain" {
				if err := s.Store.keepPageAnswer(answer); err != nil {
					return err
				}
				detail += "; supervisor stopped during delivery; inspect the task before sending this or later feedback again"
			} else if isHeld {
				detail += "; an earlier delivery on this page is uncertain; inspect and relay this feedback in order"
			} else if r.Task != "" && s.Store.taskStands(r.Task) && s.Options.CFO != nil && ctx.Err() == nil {
				answer.Status = "sending"
				if err := s.Store.keepPageAnswer(answer); err != nil {
					return err
				}
				_, err := s.Options.CFO.SendGoblin(ctx, r.Task, r.Identity, text)
				if err == nil {
					detail, reason = delivered, deliveredReason
				} else if errors.Is(err, fleet.ErrQueuedForToolCall) {
					detail = queued
					if answer.IsAnswer {
						reason = "You answered on its page; your answer is queued for the goblin's next tool call."
					}
				} else {
					s.publish(err)
					if !errors.Is(err, ErrRejected) {
						answer.Status = "uncertain"
						if saveErr := s.Store.keepPageAnswer(answer); saveErr != nil {
							return saveErr
						}
						detail += "; terminal delivery is uncertain; inspect the task before sending this or later feedback again"
					}
				}
			}
			answer.Detail, answer.Reason = detail, reason
		}
		answer.Status = "notifying"
		if err := s.Store.keepPageAnswer(answer); err != nil {
			return err
		}
	}
	key := answer.Item.Task
	if key == "" {
		key = answer.Item.ID
	}
	for attempt := 0; ; attempt++ {
		_, _, err := wake.AppendFirst(s.Store.Home.State, "page-answer:"+answer.ID, "review", key, answer.Detail)
		if err == nil {
			break
		}
		s.publish(err)
		if attempt == 9 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pagePollPause):
		}
	}
	if _, err := wake.PublishEpisode(s.Store.Home.State); err != nil {
		return err
	}
	if answer.Reason != "" {
		if err := s.Store.explainAnswer(r, answer.Reason); err != nil {
			return err
		}
	}
	s.notify()
	if !answer.IsPageEnd {
		if _, err := s.savePageAnswer(answer); err != nil {
			return err
		}
	}
	return s.Store.forgetPageAnswer(answer.ID)
}

// The fallback always names durable feedback, including when delivery is
// uncertain. Receipt text never claims the model has already read it.
func (s *Service) pageMessage(answer pageAnswer) (text, detail, delivered, reason, deliveredReason, queued string) {
	r, poll := answer.Item, answer.Poll
	saved, err := s.savePageAnswer(answer)
	feedback := "his feedback is in " + saved
	if err != nil {
		s.publish(err)
		output := poll.Output
		if len(output) > pageFeedbackInline {
			cut := len(output) - pageFeedbackInline
			for cut < len(output) && !utf8.RuneStart(output[cut]) {
				cut++
			}
			output = "(cut to its end) ..." + output[cut:]
		}
		feedback = fmt.Sprintf("his feedback could not be saved (%v); durable receipt %s is in %s: %s", err, answer.ID, s.Store.pageReceiptsPath(), output)
	}
	asked := strings.Join(poll.Prompts, "\n")
	if asked == "" {
		asked = feedback
	}
	queued = "the Overlord's feedback on the page " + r.LavishPage + " is queued for " + r.Task + "'s next tool call; " + feedback
	switch {
	case r.State != "open":
		text = fmt.Sprintf("The Overlord wrote on your review page %s: %s\nTo show him a next version, change the same file and run cfo notify %s --waiting-on overlord \"<why>\" --lavish %s.", r.LavishPage, asked, r.Task, r.LavishPage)
		detail = "the Overlord wrote on your page " + r.LavishPage + "; " + feedback + ", act on it"
		if r.Task != "" {
			detail = "the Overlord wrote on the page " + r.LavishPage + "; " + feedback + ", relay it to " + r.Task
			if !s.Store.taskStands(r.Task) {
				detail = "the Overlord wrote on the page " + r.LavishPage + " of " + r.Task + ", which has been retired; " + feedback + ", act on it"
			}
		}
		delivered = bounded("the Overlord wrote on the page "+r.LavishPage+", and "+r.Task+" has it: "+asked, 4000)
	case answer.IsAnswer:
		on := "the Overlord answered on the page " + r.LavishPage
		if poll.Ended && poll.EndedBy != "agent" {
			on += " and ended the review"
		}
		text = "The Overlord answered on your review page " + r.LavishPage + ": " + asked
		detail = on + "; " + feedback + ", relay it to the goblin"
		reason = "You answered on its page; the CFO relays it to the goblin."
		if r.Task == "" {
			detail = on + "; " + feedback + ", act on it"
			reason = "You answered on its page; the CFO has it."
		}
		delivered = on + "; " + feedback + ", the goblin has it: " + bounded(strings.Join(poll.Prompts, "\n"), 2000)
		if err != nil {
			detail = on + ", relay it to the goblin; " + feedback
			if r.Task == "" {
				detail = on + ", act on it; " + feedback
			}
			delivered = on + ", the goblin has it; " + feedback
		}
		deliveredReason = "You answered on its page; the goblin has it."
	default:
		text = fmt.Sprintf("The Overlord asked for a revision on your review page %s: %s\nMake the next version in the same file, then run cfo notify %s --waiting-on overlord \"<why>\" --lavish %s again: it replaces the page in the same review.", r.LavishPage, asked, r.Task, r.LavishPage)
		detail = "the Overlord asked for a revision on the page " + r.LavishPage + "; " + feedback + ", act on it"
		if r.Task != "" {
			detail = "the Overlord asked for a revision on the page " + r.LavishPage + "; " + feedback + ", relay it to the goblin"
		}
		delivered = bounded("the Overlord asked for a revision on the page "+r.LavishPage+", and "+r.Task+" has it and makes the next version: "+asked, 4000)
	}
	return
}
