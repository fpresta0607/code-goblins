package fleet

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/routing"
)

const (
	// typeSettle lets a terminal take typed text before Enter submits it, and
	// completionSettle is the longer wait a message that opens a harness
	// completion popup needs.
	typeSettle       = 300 * time.Millisecond
	completionSettle = 1200 * time.Millisecond
)

// Stamp marks a steer as the CFO's with routing.SteerPrefix, once, so the
// fault detector can tell text the CFO wrote about a provider from text the
// provider wrote. A message the CFO already prefixed is left alone, and so
// is a slash or dollar command: that is for the harness, not the goblin,
// and a prefix would turn it into prose.
func Stamp(message string) string {
	if strings.HasPrefix(message, routing.SteerPrefix) || IsCommand(message) {
		return message
	}
	return routing.SteerPrefix + message
}

// IsCommand reports whether message is a slash or dollar command for the
// harness rather than text for the goblin.
func IsCommand(message string) bool {
	return strings.HasPrefix(message, "/") || strings.HasPrefix(message, "$")
}

// TypeSettleFor is how long to wait after typing message before Enter submits
// it. A message starting with `/` or `$` opens a harness completion popup, and
// an Enter that arrives while the popup is still open selects the highlighted
// completion instead of submitting what was typed - so `cfo send <id> "/exit"`
// could run an entirely different command. Both prefixes get the long wait
// unconditionally: waiting longer than plain text needs costs a fraction of a
// second against running the wrong command.
func TypeSettleFor(message string) time.Duration {
	if IsCommand(message) {
		return completionSettle
	}
	return typeSettle
}

// NormalizeKey names the terminal key cfo send --key accepts as key, in any of
// its spellings, as Enter, Escape, Ctrl+C or Ctrl+U.
func NormalizeKey(key string) (string, error) {
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

// ErrQueuedBehindTurn marks an unconfirmed delivery to a harness that was in
// a turn when the text was submitted. A turn in progress, such as a long tool
// call, reports nothing until it ends, so nothing can prove more before then;
// the text waits in the harness's input for the turn to end. It is not
// evidence the text was lost.
var ErrQueuedBehindTurn = errors.New("the agent was working, so the text waits for its current turn to end")
