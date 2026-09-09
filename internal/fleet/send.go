package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	// confirmBudget is spread across confirmPolls reads of the agent's own
	// state after a prompt is submitted. It replaces the Enter-retry loop:
	// herdr submits natively, so there is no keystroke to repeat and nothing
	// to resubmit - only acceptance to observe.
	confirmBudget = 600 * time.Millisecond
	confirmPolls  = 6
)

// Sender submits text to one resolved Herdr pane's registered agent, or sends
// named keys to the pane.
type Sender struct {
	Resolve TargetResolver
	Herdr   *herdr.Client
	Sleep   func(context.Context, time.Duration) error
}

// Text submits message to the registered agent and returns success only once
// Herdr's own agent state proves the agent accepted it.
//
// It does not type into the pane composer and inspect the text afterwards.
// That is what this did, and it is unreliable for the same reason spawn's
// instruction read-back was: a harness renders a submitted prompt however it
// likes, and Claude Code renders anything it treats as a paste as a collapsed
// placeholder. The composer then never shows the message, every submit reads
// as unconfirmed, and a message the CFO believes was delivered is silently
// lost mid-turn.
//
// Acceptance is proven from herdr's monotonic agent counters rather than from
// pane text, which is a stronger claim than the composer check made: it says
// the agent took the prompt and started a turn, not that some text appeared
// to be typed.
func (s Sender) Text(ctx context.Context, raw string, message string) error {
	target, _, err := s.target(ctx, raw)
	if err != nil {
		return err
	}

	before, err := s.Herdr.AgentDetail(ctx, target)
	if err != nil {
		if herdr.WaitError(ctx, err) {
			return fmt.Errorf("fleet: inspect target before submit for %s: %w", target, err)
		}
		// An unreadable agent is transient. Treat the pre-state as zero: any
		// later counter is then an advance, which is the conservative
		// direction - it can withhold a success, never invent one.
		before = herdr.AgentDetail{}
	}

	if err := s.Herdr.AgentPrompt(ctx, target, message); err != nil {
		return fmt.Errorf("fleet: submit text for %s: %w", target, err)
	}

	// The prompt is submitted once. `agent prompt` submits on success, so a
	// re-send would deliver the message twice - and a duplicated instruction
	// to a working goblin is worse than an unconfirmed one, which the caller
	// can check and repeat deliberately.
	var lastReadErr error
	for poll := 0; poll < confirmPolls; poll++ {
		if err := s.sleep(ctx, confirmBudget/confirmPolls); err != nil {
			return fmt.Errorf("fleet: wait for delivery confirmation for %s: %w", target, err)
		}
		after, err := s.Herdr.AgentDetail(ctx, target)
		if err != nil {
			if herdr.WaitError(ctx, err) {
				return fmt.Errorf("fleet: confirm text delivery for %s: %w", target, err)
			}
			lastReadErr = err
			continue
		}
		if herdr.PromptAccepted(before, after) {
			return nil
		}
	}
	if lastReadErr != nil {
		return fmt.Errorf("%w; later agent reads were refused: %w", unconfirmed(target, herdr.SubmitPending), lastReadErr)
	}
	return unconfirmed(target, herdr.SubmitPending)
}

// Key sends one supported terminal key without introducing text into the
// pane. The command layer keeps text and --key mutually exclusive.
func (s Sender) Key(ctx context.Context, raw string, key string) error {
	normalized, err := normalizeKey(key)
	if err != nil {
		return err
	}
	target, _, err := s.target(ctx, raw)
	if err != nil {
		return err
	}
	if err := s.Herdr.SendKey(ctx, target, normalized); err != nil {
		return fmt.Errorf("fleet: send key %q to %s: %w", key, target, err)
	}
	return nil
}

func (s Sender) target(ctx context.Context, raw string) (herdr.Target, state.TaskMeta, error) {
	if s.Resolve == nil {
		return herdr.Target{}, state.TaskMeta{}, errors.New("fleet: target resolver is required")
	}
	if s.Herdr == nil {
		return herdr.Target{}, state.TaskMeta{}, errors.New("fleet: Herdr client is required")
	}
	target, meta, err := s.Resolve.Resolve(ctx, raw)
	if err != nil {
		return herdr.Target{}, state.TaskMeta{}, err
	}
	return target, meta, nil
}

// compact removes all whitespace so wrapped composer text matches a delivered
// message regardless of pane width.
func compact(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
}

func (s Sender) sleep(ctx context.Context, duration time.Duration) error {
	if s.Sleep != nil {
		return s.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func normalizeKey(key string) (string, error) {
	switch strings.ToLower(key) {
	case "enter":
		return "Enter", nil
	case "escape", "esc":
		return "Escape", nil
	case "ctrl+c", "ctrl-c", "c-c":
		return "Ctrl+C", nil
	case "ctrl+u", "ctrl-u", "c-u":
		return "Ctrl+U", nil
	default:
		return "", fmt.Errorf("fleet: unsupported key %q; use Enter, Escape, Ctrl-C, or Ctrl-U", key)
	}
}

// composerWindow bounds how many non-empty lines up from the pane bottom the
// composer is searched, so a prompt line in old scrollback can never read as
// the current composer. Blank pane rows are not charged to the window, so a
// tall pane with unfilled rows beneath the footer cannot push the composer
// out of it.
const composerWindow = 20

func unconfirmed(target herdr.Target, state herdr.SubmitState) error {
	return fmt.Errorf("fleet: text delivery to %s is unconfirmed: %s", target, state)
}
