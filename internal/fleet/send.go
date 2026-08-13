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
	plainSettle      = 300 * time.Millisecond
	completionSettle = 1200 * time.Millisecond
	enterRetries     = 3
	enterSleep       = 400 * time.Millisecond
	confirmBudget    = 600 * time.Millisecond
	confirmPolls     = 6
)

// Sender types text or sends named keys to one resolved Herdr pane.
type Sender struct {
	Resolve TargetResolver
	Herdr   *herdr.Client
	Sleep   func(context.Context, time.Duration) error
}

// Text types message once, submits it with bounded Enter retries, and returns
// success only after Herdr supplies a positive delivery confirmation.
func (s Sender) Text(ctx context.Context, raw string, message string) error {
	target, meta, err := s.target(ctx, raw)
	if err != nil {
		return err
	}
	if err := s.Herdr.SendLiteral(ctx, target, message); err != nil {
		return fmt.Errorf("fleet: type text for %s: %w", target, err)
	}
	if err := s.sleep(ctx, settleDuration(meta, message)); err != nil {
		return fmt.Errorf("fleet: wait before submit for %s: %w", target, err)
	}

	baseline, err := s.Herdr.WaitForWorking(ctx, target, 0, 1)
	if err != nil {
		return fmt.Errorf("fleet: inspect target before submit for %s: %w", target, err)
	}
	for attempt := 0; attempt < enterRetries; attempt++ {
		if err := s.Herdr.SendKey(ctx, target, "Enter"); err != nil {
			return fmt.Errorf("fleet: submit text for %s: %w", target, err)
		}

		state, err := s.confirm(ctx, target, meta, baseline)
		if err != nil {
			return err
		}
		switch state {
		case herdr.SubmitWorking, herdr.SubmitBlocked:
			return nil
		case herdr.SubmitIdle:
			continue
		case herdr.SubmitPending, herdr.SubmitUnknown:
			return unconfirmed(target, state)
		default:
			return unconfirmed(target, herdr.SubmitUnknown)
		}
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

func (s Sender) confirm(ctx context.Context, target herdr.Target, meta state.TaskMeta, baseline herdr.SubmitState) (herdr.SubmitState, error) {
	if baseline == herdr.SubmitIdle {
		confirmed, err := s.Herdr.WaitForWorking(ctx, target, confirmBudget, confirmPolls)
		if err != nil {
			return herdr.SubmitUnknown, fmt.Errorf("fleet: confirm text delivery for %s: %w", target, err)
		}
		return confirmed, nil
	}

	if err := s.sleep(ctx, enterSleep); err != nil {
		return herdr.SubmitUnknown, fmt.Errorf("fleet: wait for composer confirmation for %s: %w", target, err)
	}
	return s.composerState(ctx, target, meta)
}

func (s Sender) composerState(ctx context.Context, target herdr.Target, meta state.TaskMeta) (herdr.SubmitState, error) {
	captured, err := s.Herdr.Capture(ctx, target, 200, true)
	if err != nil {
		return herdr.SubmitUnknown, fmt.Errorf("fleet: inspect composer for %s: %w", target, err)
	}

	if meta.Harness == "pi" {
		return s.piComposerState(ctx, target, stripANSI(captured))
	}
	prompt := composerPrompt(meta.Harness)
	if prompt == "" {
		return herdr.SubmitUnknown, nil
	}
	lines := strings.Split(stripANSI(captured), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, prompt) {
			return herdr.SubmitUnknown, nil
		}
		if strings.TrimSpace(strings.TrimPrefix(line, prompt)) == "" {
			return herdr.SubmitWorking, nil
		}
		return herdr.SubmitIdle, nil
	}
	return herdr.SubmitUnknown, nil
}

func (s Sender) piComposerState(ctx context.Context, target herdr.Target, captured string) (herdr.SubmitState, error) {
	if state := piComposerCandidate(captured); state != herdr.SubmitWorking {
		return state, nil
	}

	detail, err := s.Herdr.AgentDetail(ctx, target)
	if err != nil {
		return herdr.SubmitUnknown, fmt.Errorf("fleet: inspect Pi agent for %s: %w", target, err)
	}
	if detail.Agent != "pi" {
		return herdr.SubmitUnknown, nil
	}
	switch detail.Status {
	case "idle", "done", "blocked":
		return herdr.SubmitWorking, nil
	default:
		return herdr.SubmitUnknown, nil
	}
}

func piComposerCandidate(captured string) herdr.SubmitState {
	lines := strings.Split(captured, "\n")
	lastSeparator, open, close := -1, -1, -1
	for index, line := range lines {
		if !piSeparator(line) {
			continue
		}
		if lastSeparator >= 0 {
			open, close = lastSeparator, index
		}
		lastSeparator = index
	}
	if close < 0 || close-open > 9 {
		return herdr.SubmitUnknown
	}
	for _, line := range lines[open+1 : close] {
		if strings.TrimSpace(line) != "" {
			return herdr.SubmitIdle
		}
	}
	footer := false
	for _, line := range lines[close+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if footer || !piFooter(trimmed) {
			return herdr.SubmitUnknown
		}
		footer = true
	}
	return herdr.SubmitWorking
}

func piFooter(line string) bool {
	return strings.HasPrefix(line, "~/") && strings.Contains(line, " (") && strings.HasSuffix(line, ")")
}

func piSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.Count(trimmed, "─") >= 8 && strings.Trim(trimmed, "─") == ""
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

func settleDuration(meta state.TaskMeta, message string) time.Duration {
	if strings.HasPrefix(message, "/") || (strings.HasPrefix(message, "$") && (meta.Harness == "" || meta.Harness == "codex")) {
		return completionSettle
	}
	return plainSettle
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

func composerPrompt(harness string) string {
	switch harness {
	case "claude":
		return "❯"
	case "codex":
		return "›"
	default:
		return ""
	}
}

func stripANSI(text string) string {
	var out strings.Builder
	for index := 0; index < len(text); {
		if text[index] == 0x1b && index+1 < len(text) && text[index+1] == '[' {
			index += 2
			for index < len(text) && (text[index] < '@' || text[index] > '~') {
				index++
			}
			if index < len(text) {
				index++
			}
			continue
		}
		out.WriteByte(text[index])
		index++
	}
	return out.String()
}

func unconfirmed(target herdr.Target, state herdr.SubmitState) error {
	return fmt.Errorf("fleet: text delivery to %s is unconfirmed: %s", target, state)
}
