package fleettree

import (
	"encoding/json"
	"regexp"
	"strings"
)

// claudeContextWindows are the context windows of the Claude models, by the
// model id a Claude Code transcript names, which records no window of its
// own. They are the models' input limits as Anthropic publishes them
// (max_input_tokens), and a model not named here has no window the board
// can read.
var claudeContextWindows = map[string]int64{
	"claude-fable-5-1": 1_000_000, "claude-fable-5": 1_000_000, "claude-mythos-5-1": 1_000_000,
	"claude-opus-5-5": 1_000_000, "claude-opus-5": 1_000_000, "claude-opus-4-8": 1_000_000, "claude-opus-4-7": 1_000_000, "claude-opus-4-6": 1_000_000,
	"claude-sonnet-5-5": 1_000_000, "claude-sonnet-5": 1_000_000, "claude-sonnet-4-6": 1_000_000,
	"claude-haiku-5-5": 1_000_000, "claude-haiku-4-5": 200_000,
	"claude-opus-4-5": 200_000, "claude-opus-4-1": 200_000, "claude-opus-4": 200_000, "claude-sonnet-4-5": 200_000, "claude-sonnet-4": 200_000,
}

// claudeSnapshot is the date an older model's id ends with, as in
// claude-haiku-4-5-20251001.
var claudeSnapshot = regexp.MustCompile(`-\d{8}$`)

// claudeContextWindow is the context window of the Claude model a transcript
// names, and 0 for a model the board does not know. A Claude Code id with
// [1m] runs the model's million-token window.
func claudeContextWindow(model string) int64 {
	if trimmed, isLong := strings.CutSuffix(model, "[1m]"); isLong {
		if _, known := claudeContextWindows[claudeSnapshot.ReplaceAllString(trimmed, "")]; known {
			return 1_000_000
		}
		return 0
	}
	return claudeContextWindows[claudeSnapshot.ReplaceAllString(model, "")]
}

func recordedModel(entries [][]byte, harness string) string {
	for index := len(entries) - 1; index >= 0; index-- {
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Model string `json:"model"`
			} `json:"message"`
			Payload struct {
				Model string `json:"model"`
			} `json:"payload"`
		}
		if json.Unmarshal(entries[index], &entry) != nil {
			continue
		}
		if harness == "claude" && entry.Type == "assistant" && entry.Message.Model != "" && entry.Message.Model != syntheticModel {
			return entry.Message.Model
		}
		if harness == "codex" && entry.Type == "turn_context" && entry.Payload.Model != "" {
			return entry.Payload.Model
		}
	}
	return ""
}
