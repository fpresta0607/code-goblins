package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// ReplyReader reads the reply that ended a goblin's last turn, empty when
// none can be read: its turn is still running or ended on a tool call, or its
// harness keeps nothing this reads.
type ReplyReader interface {
	LastReply(ctx context.Context, meta state.TaskMeta, sample EndpointSample) string
}

// LastReply reads the reply that ended the goblin's last turn from its
// harness's own record: Claude Code's conversation or Codex's rollout, the
// goblin's own as the board's tree reads it, which is the session Herdr
// names or else the one the board's record or the harness itself proves.
// pi's is read off its screen, the one harness whose record this does not
// read.
func (h *HostProgress) LastReply(ctx context.Context, meta state.TaskMeta, sample EndpointSample) string {
	var read func(entries [][]byte) string
	switch strings.ToLower(sample.Harness) {
	case "claude":
		read = claudeReply
	case "codex":
		read = codexReply
	case "pi":
		return screenReply(sample.Capture)
	default:
		return ""
	}
	meta.Harness = sample.Harness
	goblin := fleettree.Goblin{Meta: meta, Session: sample.Session}
	if sample.Session == "" {
		goblin.HarnessPID, goblin.HarnessStarted, _ = h.harness(ctx, meta, sample)
	}
	return lastReplyIn(h.tree().Conversation(ctx, goblin), read)
}

// lastReplyIn reads the reply read finds in the end of the transcript at
// path.
func lastReplyIn(path string, read func(entries [][]byte) string) string {
	if path == "" {
		return ""
	}
	file, err := fsx.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	start := max(0, info.Size()-fleettree.TranscriptEntryReach)
	tail := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(tail, start); err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	entries := bytes.Split(tail, []byte("\n"))
	if start > 0 {
		// The first piece may begin part way through an entry.
		entries = entries[1:]
	}
	return read(entries)
}

// claudeReply is the text that ended the last turn of a Claude Code
// conversation: the text after its last prompt, tool call or tool result,
// read back from its end. A turn still running, one that ended on a tool
// call, and Claude Code's own placeholder reply have none. A subagent's
// entries are passed over.
func claudeReply(entries [][]byte) string {
	var texts []string
	for i := len(entries) - 1; i >= 0; i-- {
		var entry struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				Model   string          `json:"model"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(entries[i], &entry) != nil || entry.IsSidechain {
			continue
		}
		if entry.Type == "user" {
			break
		}
		if entry.Type != "assistant" {
			continue
		}
		if entry.Message.Model == "<synthetic>" {
			return ""
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(entry.Message.Content, &blocks) != nil {
			return ""
		}
		toolCall := false
		for j := len(blocks) - 1; j >= 0 && !toolCall; j-- {
			switch blocks[j].Type {
			case "text":
				if text := strings.TrimSpace(blocks[j].Text); text != "" {
					texts = append([]string{text}, texts...)
				}
			case "tool_use":
				toolCall = true
			}
		}
		if toolCall {
			break
		}
	}
	return strings.Join(texts, "\n\n")
}

// codexReply is the last agent message of the last turn a Codex rollout
// records as complete, and none once a turn started after it.
func codexReply(entries [][]byte) string {
	for i := len(entries) - 1; i >= 0; i-- {
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				Type             string `json:"type"`
				Role             string `json:"role"`
				LastAgentMessage string `json:"last_agent_message"`
			} `json:"payload"`
		}
		if json.Unmarshal(entries[i], &entry) != nil {
			continue
		}
		switch {
		case entry.Type == "event_msg" && entry.Payload.Type == "task_complete":
			return strings.TrimSpace(entry.Payload.LastAgentMessage)
		case entry.Type == "event_msg" && (entry.Payload.Type == "task_started" || entry.Payload.Type == "user_message" || entry.Payload.Type == "turn_aborted"),
			entry.Type == "response_item" && entry.Payload.Role == "user":
			return ""
		}
	}
	return ""
}

// screenReply is what a screen shows above the composer pi draws between its
// last two full-width rules, its paragraphs joined back from the rows the
// screen wrapped them in; none when no composer shows.
func screenReply(capture []byte) string {
	rows := strings.Split(strings.ReplaceAll(string(capture), "\r\n", "\n"), "\n")
	var rules []int
	for i, row := range rows {
		if row = strings.TrimSpace(row); len([]rune(row)) >= 8 && strings.Trim(row, "─") == "" {
			rules = append(rules, i)
		}
	}
	if len(rules) < 2 {
		return ""
	}
	var paragraphs, paragraph []string
	for _, row := range rows[:rules[len(rules)-2]] {
		if row = strings.TrimSpace(row); row != "" {
			paragraph = append(paragraph, row)
		} else if len(paragraph) > 0 {
			paragraphs, paragraph = append(paragraphs, strings.Join(paragraph, " ")), nil
		}
	}
	if len(paragraph) > 0 {
		paragraphs = append(paragraphs, strings.Join(paragraph, " "))
	}
	return strings.Join(paragraphs, "\n\n")
}
