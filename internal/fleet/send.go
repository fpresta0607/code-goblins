package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
)

const (
	// preSubmitBudget is spread across preSubmitTries attempts at each read
	// taken before anything is submitted. The registration probe and the
	// baseline ask the same question of the same Herdr, so a Herdr that is
	// momentarily busy delays the send rather than costing it either answer.
	preSubmitBudget = 1200 * time.Millisecond
	preSubmitTries  = 4

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
// Herdr's own monotonic agent counters prove the registered agent accepted it.
//
// A pane Herdr reports no agent for is delivered to only when the caller
// addressed it directly as <session>:<pane-id>: the text is typed and Enter
// submits it, and the result is always reported unconfirmed, because a pane
// with no registered agent has nothing to prove acceptance with. Such a pane
// is not necessarily at a shell prompt - a detection manifest that has not
// kept up with a harness release leaves a live harness holding no registered
// agent - which is exactly why that delivery never claims success. A task
// selector whose agent is gone is refused outright and never typed into: a
// goblin is addressed through its agent or not at all.
//
// Neither mode types into a composer and reads the text back afterwards. That
// is what this did, and it is unreliable for the same reason spawn's
// instruction read-back was: a harness renders a submitted prompt however it
// likes, and Claude Code renders anything it treats as a paste as a collapsed
// placeholder. The composer then never shows the message, every submit reads
// as unconfirmed, and a message the CFO believes was delivered is silently
// lost mid-turn.
func (s Sender) Text(ctx context.Context, raw string, message string) error {
	target, addressedExplicitly, err := s.target(ctx, raw)
	if err != nil {
		return err
	}

	registration, err := preSubmitRead(ctx, s.sleep, func() (herdr.AgentStatus, error) {
		return s.Herdr.AgentStatus(ctx, target)
	})
	if err != nil {
		return fmt.Errorf("fleet: read agent registration for %s: %w", target, err)
	}
	switch registration {
	case herdr.AgentAlive:
	case herdr.AgentDead:
		if !addressedExplicitly {
			return fmt.Errorf("fleet: %s holds no registered agent, so the goblin has nothing to receive the text; address the pane as <session>:<pane-id> to type into it anyway", target)
		}
		return s.typeIntoPane(ctx, target, message)
	default:
		return fmt.Errorf("fleet: %s cannot take text: pane is %s", target, registration)
	}

	// Acceptance is measured against the counters as they stood before the
	// submit, so the baseline has to be a real read. An unreadable one is
	// never guessed at as zero: zero is the lowest value the counters can
	// hold, so a guessed baseline would read the first number a long-running
	// goblin reports as an advance and confirm a message it never took.
	before, err := preSubmitRead(ctx, s.sleep, func() (herdr.AgentDetail, error) {
		return s.Herdr.AgentDetail(ctx, target)
	})
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
			// A pane Herdr can prove holds no agent will never report
			// accepting anything, so spending the rest of the budget on it
			// only delays a certain answer and buries its real cause behind
			// the decode-shaped refusal an unregistered agent read produces.
			if s.paneProvablyDead(ctx, target) {
				return fmt.Errorf("fleet: %s no longer holds a registered agent, so the text submitted to it cannot be confirmed: %w", target, err)
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

// preSubmitRead runs one Herdr read across the pre-submit budget, retrying a
// Herdr that is momentarily unavailable and giving up immediately on a
// terminal failure. It returns an error rather than a usable zero value, and
// its callers submit nothing without an answer: a send whose acceptance cannot
// be proven is the failure this path exists to remove, and the caller can
// retry a refusal deliberately.
func preSubmitRead[T any](ctx context.Context, sleep func(context.Context, time.Duration) error, read func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt < preSubmitTries; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, preSubmitBudget/(preSubmitTries-1)); err != nil {
				return zero, err
			}
		}
		value, err := read()
		if err == nil {
			return value, nil
		}
		if herdr.WaitError(ctx, err) {
			return zero, err
		}
		lastErr = err
	}
	return zero, lastErr
}

// paneProvablyDead reports whether Herdr gave a trustworthy answer that the
// pane holds no agent. An unreadable probe counts as not-dead, so a Herdr that
// merely cannot answer never turns a slow confirmation into a hard failure.
func (s Sender) paneProvablyDead(ctx context.Context, target herdr.Target) bool {
	status, err := s.Herdr.AgentStatus(ctx, target)
	return err == nil && (status == herdr.AgentDead || status == herdr.AgentMissing)
}

// typeIntoPane types the message into an explicitly addressed pane Herdr
// reports no agent for and submits it with Enter, for example the pane left
// behind after `cfo send <id> "/exit"`. It always reports the delivery
// unconfirmed: there is no agent to prompt and no agent state to read, and the
// pane is deliberately not read back, because composer inspection is the
// defect the agent path exists to remove rather than a fallback this one may
// reach for.
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
	return fmt.Errorf("%w; it was typed into a pane holding no registered agent, so nothing reports whether it was received", unconfirmed(target, herdr.SubmitUnknown))
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

// target resolves raw and reports whether the caller addressed the pane
// directly as <session>:<pane-id> rather than through a task selector. Only an
// explicitly addressed pane carries no task metadata, and that is the one
// thing the resolver's metadata is still consulted for: delivery is chosen
// from what Herdr reports about the pane, never from a recorded harness.
func (s Sender) target(ctx context.Context, raw string) (herdr.Target, bool, error) {
	if s.Resolve == nil {
		return herdr.Target{}, false, errors.New("fleet: target resolver is required")
	}
	if s.Herdr == nil {
		return herdr.Target{}, false, errors.New("fleet: Herdr client is required")
	}
	target, meta, err := s.Resolve.Resolve(ctx, raw)
	if err != nil {
		return herdr.Target{}, false, err
	}
	return target, meta.HerdrPaneID == "", nil
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
