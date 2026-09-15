package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

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

	// typeSettle lets a pane take typed text before Enter submits it, and
	// completionSettle is the longer wait a message that opens a harness
	// completion popup needs.
	typeSettle       = 300 * time.Millisecond
	completionSettle = 1200 * time.Millisecond
)

// Sender submits text to one resolved Herdr pane's registered agent, or sends
// named keys to the pane.
type Sender struct {
	Resolve      TargetResolver
	Herdr        *herdr.Client
	Sleep        func(context.Context, time.Duration) error
	RequireAgent bool
}

// DeliveryError distinguishes evidence of typing or queuing from acceptance.
// An unknown result must be inspected before any deliberate resubmission.
type DeliveryError struct {
	Stage  string
	Target herdr.Target
}

func (e *DeliveryError) Error() string {
	return fmt.Sprintf("fleet: text delivery to %s is unconfirmed: %s; inspect cfo peek before resending", e.Target, e.Stage)
}

// SubmissionConfirmed is true only for recognized native queue evidence.
// It deliberately does not claim the agent has accepted or acted on the text.
func (e *DeliveryError) SubmissionConfirmed() bool {
	return strings.HasPrefix(e.Stage, "submitted:")
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
// Codex can leave native agent-prompt text in its composer. Only recognized
// composer evidence plus unchanged foreground identity permits one Enter.
// A visible queued message proves submission, not acceptance.
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
		if !addressedExplicitly || s.RequireAgent {
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
	var beforeProcess herdr.PaneProcessInfo
	if before.Agent == "codex" {
		beforeProcess, err = s.Herdr.PaneProcessInfo(ctx, target)
		if err != nil || beforeProcess.ShellPID == beforeProcess.ForegroundProcessGroupID {
			return fmt.Errorf("fleet: no verified Codex foreground process; nothing sent: %v", err)
		}
		screen, readErr := s.Herdr.CaptureEvidence(ctx, target)
		if readErr != nil {
			return fmt.Errorf("fleet: cannot inspect Codex composer; nothing sent: %w", readErr)
		}
		if !codexComposerEmpty(string(screen)) {
			return &DeliveryError{Stage: "not sent: Codex composer is not recognized as empty; inspect before submitting or clearing it", Target: target}
		}
	}

	if err := s.Herdr.AgentPrompt(ctx, target, message); err != nil {
		return fmt.Errorf("fleet: submit text for %s: %w", target, err)
	}

	// Submit text once. Working-agent counters can advance from unrelated
	// tool output, so they cannot confirm this message was accepted.
	var lastReadErr error
	entered := false
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
			if s.Herdr.PaneProvablyDead(ctx, target) {
				return fmt.Errorf("fleet: %s no longer holds a registered agent, so the text submitted to it cannot be confirmed: %w", target, err)
			}
			lastReadErr = err
			continue
		}
		if after.Agent != before.Agent {
			return &DeliveryError{Stage: "unknown: registered harness changed", Target: target}
		}
		if before.Agent == "codex" {
			screen, readErr := s.Herdr.CaptureEvidence(ctx, target)
			if readErr == nil {
				switch codexDelivery(string(screen), message) {
				case "submitted":
					return &DeliveryError{Stage: "submitted: visible in the harness queue", Target: target}
				case "typed":
					if entered {
						return &DeliveryError{Stage: "typed: Enter did not submit", Target: target}
					}
					current, e := s.Herdr.AgentDetail(ctx, target)
					if e != nil || current.Agent != "codex" {
						return &DeliveryError{Stage: "typed: identity no longer verified", Target: target}
					}
					process, e := s.Herdr.PaneProcessInfo(ctx, target)
					if e != nil || process != beforeProcess {
						return &DeliveryError{Stage: "typed: foreground harness not verified", Target: target}
					}
					if e = s.Herdr.SendKey(ctx, target, "Enter"); e != nil {
						return fmt.Errorf("fleet: typed but submission failed: %w", e)
					}
					entered = true
					continue
				}
			}
		}
		if before.Status != "working" && herdr.PromptAccepted(before, after) {
			return nil
		}
	}
	if lastReadErr != nil {
		return fmt.Errorf("%w; later agent reads were refused: %w", unconfirmed(target, herdr.SubmitPending), lastReadErr)
	}
	return unconfirmed(target, herdr.SubmitPending)
}

func codexComposerEmpty(screen string) bool {
	start := strings.LastIndex(screen, "\n› ")
	if start < 0 {
		return false
	}
	composer := screen[start+len("\n› "):]
	if end := strings.Index(strings.ToLower(composer), "tab to queue message"); end >= 0 {
		composer = strings.TrimSpace(composer[:end])
		return composer == "" || composer == "Ask Codex to do anything"
	}
	lines := strings.Split(strings.ReplaceAll(composer, "\r\n", "\n"), "\n")
	first := strings.TrimSpace(lines[0])
	const placeholder = "Ask Codex to do anything"
	if !strings.HasPrefix(first, placeholder) || !codexBrailleDecoration(strings.TrimPrefix(first, placeholder)) {
		return false
	}
	footerSeen := false
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if codexBrailleDecoration(line) {
			continue
		}
		if footerSeen || !codexFooter(line) {
			return false
		}
		footerSeen = true
	}
	return true
}

func codexBrailleDecoration(value string) bool {
	for _, r := range value {
		if unicode.IsSpace(r) || r >= '\u2800' && r <= '\u28ff' {
			continue
		}
		return false
	}
	return true
}

func codexFooter(line string) bool {
	parts := strings.Split(line, " · ")
	if len(parts) < 3 || !strings.HasPrefix(strings.TrimSpace(parts[0]), "gpt-") {
		return false
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return false
		}
	}
	return true
}

// Only exact message text inside a recognized Codex queue or composer is
// submission evidence. Tool output containing the message is not evidence.
func codexDelivery(screen, message string) string {
	normalize := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	want := normalize(message)
	if want == "" {
		return "unknown"
	}
	if start := strings.LastIndex(screen, "Messages to be submitted after next tool call"); start >= 0 {
		queue := screen[start:]
		if end := strings.Index(queue, "\n›"); end >= 0 {
			queue = queue[:end]
		}
		if entry := strings.Index(queue, "↳ "); entry >= 0 && normalize(queue[entry+len("↳ "):]) == want {
			return "submitted"
		}
	}
	if start := strings.LastIndex(screen, "\n› "); start >= 0 {
		composer := screen[start+len("\n› "):]
		if end := strings.Index(strings.ToLower(composer), "tab to queue message"); end >= 0 {
			typed := normalize(composer[:end])
			if typed == want {
				return "typed"
			}
		}
	}
	return "unknown"
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

// typeSettleFor is how long to wait after typing message before Enter submits
// it. A message starting with `/` or `$` opens a harness completion popup, and
// an Enter that arrives while the popup is still open selects the highlighted
// completion instead of submitting what was typed - so `cfo send <pane>
// "/exit"` can run an entirely different command. A pane with no registered
// agent is not necessarily at a shell prompt, so the popup is reachable here.
// Both prefixes get the long wait unconditionally: this path has no harness
// metadata by design, and waiting longer than a shell prompt needs costs a
// fraction of a second against running the wrong command.
func typeSettleFor(message string) time.Duration {
	if strings.HasPrefix(message, "/") || strings.HasPrefix(message, "$") {
		return completionSettle
	}
	return typeSettle
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
	if err := s.sleep(ctx, typeSettleFor(message)); err != nil {
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
