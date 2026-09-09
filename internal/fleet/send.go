package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	// baselineBudget is spread across baselineTries reads of the agent's own
	// counters before anything is submitted. A Herdr that is momentarily busy
	// delays the send rather than costing it its baseline, because a guessed
	// baseline is worse than a slow one.
	baselineBudget = 1200 * time.Millisecond
	baselineTries  = 4

	// confirmBudget is spread across confirmPolls reads of the agent's own
	// state after a prompt is submitted. It is deliberately far shorter than
	// the 90s spawn gives the same predicate: a spawn is a background launch
	// nobody is watching, while a person is waiting on `cfo send`, and an
	// interactive command that hangs for a minute and a half is its own
	// failure. Five seconds is many times the counter update observed on
	// every harness measured, with room for a loaded host.
	confirmBudget = 5 * time.Second
	confirmPolls  = 10

	// typeSettle lets a pane take typed text before Enter submits it.
	typeSettle = 300 * time.Millisecond
)

// Sender submits text to one resolved Herdr pane's registered agent, or sends
// named keys to the pane.
type Sender struct {
	Resolve TargetResolver
	Herdr   *herdr.Client
	Sleep   func(context.Context, time.Duration) error
}

// Text delivers message to the resolved target and returns success only once
// the delivery is accounted for.
//
// A pane that holds a registered agent takes the message through `herdr agent
// prompt`, and success is withheld until Herdr's own monotonic agent counters
// move. A pane that holds no agent - an explicit <session>:<pane-id> target
// sitting at a shell prompt - takes it as typed text submitted with Enter.
// The mode is chosen from what Herdr reports about the pane, never from task
// metadata.
//
// Neither mode types into a composer and reads the text back afterwards. That
// is what this did, and it is unreliable for the same reason spawn's
// instruction read-back was: a harness renders a submitted prompt however it
// likes, and Claude Code renders anything it treats as a paste as a collapsed
// placeholder. The composer then never shows the message, every submit reads
// as unconfirmed, and a message the CFO believes was delivered is silently
// lost mid-turn.
func (s Sender) Text(ctx context.Context, raw string, message string) error {
	target, _, err := s.target(ctx, raw)
	if err != nil {
		return err
	}

	registration, err := s.Herdr.AgentStatus(ctx, target)
	if err != nil {
		return fmt.Errorf("fleet: read agent registration for %s: %w", target, err)
	}
	switch registration {
	case herdr.AgentAlive:
	case herdr.AgentDead:
		return s.typeIntoPane(ctx, target, message)
	default:
		return fmt.Errorf("fleet: %s cannot take text: pane is %s", target, registration)
	}

	// Acceptance is measured against the counters as they stood before the
	// submit, so the baseline has to be a real read. An unreadable one is
	// never guessed at as zero: zero is the lowest value the counters can
	// hold, so a guessed baseline would read the first number a long-running
	// goblin reports as an advance and confirm a message it never took.
	before, err := s.baseline(ctx, target)
	if err != nil {
		return fmt.Errorf("fleet: read agent state for %s before submit, so acceptance could not be proven and nothing was sent: %w", target, err)
	}

	if err := s.Herdr.AgentPrompt(ctx, target, message); err != nil {
		return fmt.Errorf("fleet: submit text for %s: %w", target, err)
	}

	// The prompt is submitted once. `agent prompt` submits on success, so a
	// re-send would deliver the message twice - and a duplicated instruction
	// to a working goblin is worse than an unconfirmed one, which the caller
	// can check and repeat deliberately.
	//
	// What the confirmation proves depends on the agent. Against one waiting
	// on input, a counter that moves after the submit is the accepted prompt:
	// nothing else moves an idle agent. Against one already working - the
	// headline steer - it is weaker, because a working agent advances its
	// counters from ordinary turn output whether or not the prompt landed.
	// There the confirmation is a liveness check and the delivery guarantee
	// rests on `agent prompt` having returned success. Herdr publishes no
	// per-prompt acceptance signal to make a stronger claim from.
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

// baseline reads the agent counters the confirmation is measured against,
// retrying a Herdr that is momentarily busy. It returns an error rather than a
// zero AgentDetail, and its caller submits nothing without it: a send whose
// acceptance cannot be proven is the failure this path exists to remove, and
// the caller can retry a refusal deliberately.
func (s Sender) baseline(ctx context.Context, target herdr.Target) (herdr.AgentDetail, error) {
	var lastErr error
	for attempt := 0; attempt < baselineTries; attempt++ {
		if attempt > 0 {
			if err := s.sleep(ctx, baselineBudget/(baselineTries-1)); err != nil {
				return herdr.AgentDetail{}, err
			}
		}
		before, err := s.Herdr.AgentDetail(ctx, target)
		if err == nil {
			return before, nil
		}
		if herdr.WaitError(ctx, err) {
			return herdr.AgentDetail{}, err
		}
		lastErr = err
	}
	return herdr.AgentDetail{}, lastErr
}

// typeIntoPane delivers to a pane Herdr reports no agent for: an explicit
// <session>:<pane-id> target at a shell prompt, for example the one left
// behind after `cfo send <id> "/exit"`. There is no agent to prompt and no
// agent state to read, so the text is typed and Enter submits it. The pane is
// deliberately not read back afterwards - composer inspection is the defect
// the agent path exists to remove, not a fallback this one may reach for.
func (s Sender) typeIntoPane(ctx context.Context, target herdr.Target, message string) error {
	if err := s.Herdr.SendLiteral(ctx, target, message); err != nil {
		return fmt.Errorf("fleet: type text for %s: %w", target, err)
	}
	if err := s.sleep(ctx, typeSettle); err != nil {
		return fmt.Errorf("fleet: wait before submit for %s: %w", target, err)
	}
	if err := s.Herdr.SendKey(ctx, target, "Enter"); err != nil {
		return fmt.Errorf("fleet: submit text for %s: %w", target, err)
	}
	return nil
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

func unconfirmed(target herdr.Target, state herdr.SubmitState) error {
	return fmt.Errorf("fleet: text delivery to %s is unconfirmed: %s", target, state)
}
