package monitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Conversation names the record a harness keeps of one agent's
// conversation: the record of Session when it is set, else the records the
// harness keeps for the folder Dir the agent started in. Started is the
// earliest the conversation can have begun, such as its task's spawn, which
// spares reading records filed before; zero reads them all.
type Conversation struct {
	Harness string
	Session string
	Dir     string
	Started time.Time
}

// TextDigest names text typed into a harness, so its record can be searched
// for the text without keeping it a second time: the SHA-256 of the text with
// each run of whitespace made one space, as a harness may wrap or trim it.
func TextDigest(text string) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(text), " ")))
	return hex.EncodeToString(sum[:])
}

// Took reports whether the harness's own record of conversation c shows the
// text digest names handed to its model at or after since: a prompt that
// started a turn, or text typed during a turn that the harness took into it
// at a tool call. Nothing else proves the agent has the text: a harness
// queues text typed during a turn, and Claude Code runs its prompt hook when
// it queues it, not when it takes it. Each record the conversation names is
// searched, since another session in the same folder, such as one the agent
// started itself, can be the newest; only each record's end is read.
func (h *HostProgress) Took(ctx context.Context, c Conversation, digest string, since time.Time) bool {
	for _, path := range h.conversationFiles(ctx, c) {
		for _, entry := range transcriptTail(path) {
			texts, at, ok := handedText(c.Harness, entry)
			if !ok || at.Before(since) {
				continue
			}
			for _, text := range texts {
				if TextDigest(text) == digest {
					return true
				}
			}
		}
	}
	return false
}

// conversationFiles is every record conversation c names.
func (h *HostProgress) conversationFiles(ctx context.Context, c Conversation) []string {
	harness := strings.ToLower(c.Harness)
	if _, known := transcriptPatterns[harness]; !known {
		return nil
	}
	if c.Session != "" {
		if path := h.sessionTranscript(harness, c.Session); path != "" {
			return []string{path}
		}
		return nil
	}
	switch harness {
	case "claude":
		return h.claudeConversations(c.Dir)
	case "codex":
		return h.nativeCodexRollouts(ctx, c.Dir, c.Started)
	case "pi":
		return h.piSessions(c.Dir)
	}
	return nil
}

// piFolderCharacters are the characters pi 0.85 writes as a hyphen in the
// folder it keeps a working folder's sessions in, which it wraps in two
// hyphens: C:\dev\x is --C--dev-x--.
var piFolderCharacters = strings.NewReplacer(`\`, "-", "/", "-", ":", "-")

// piSessions is every session pi keeps in the folder for dir.
func (h *HostProgress) piSessions(dir string) []string {
	if h.Home == "" || !filepath.IsAbs(dir) {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(h.Home, ".pi", "agent", "sessions", "--"+piFolderCharacters.Replace(dir)+"--", "*.jsonl"))
	return matches
}

// handedText reads entry, one line of harness's record, as text handed to
// its model on the user's behalf, and when. Claude Code records a prompt
// that starts a turn as a user message and text it took into a running turn
// as a queued_command attachment; Codex records either as a user message
// item; pi as a user message. A tool's result, a subagent's entries and
// whatever the agent itself wrote are none.
func handedText(harness string, entry []byte) ([]string, time.Time, bool) {
	var line struct {
		Type        string    `json:"type"`
		Timestamp   time.Time `json:"timestamp"`
		IsSidechain bool      `json:"isSidechain"`
		Attachment  struct {
			Type   string `json:"type"`
			Prompt string `json:"prompt"`
		} `json:"attachment"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		Payload struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"payload"`
	}
	if json.Unmarshal(entry, &line) != nil {
		return nil, time.Time{}, false
	}
	switch strings.ToLower(harness) {
	case "claude":
		if line.IsSidechain {
			return nil, time.Time{}, false
		}
		if line.Type == "attachment" && line.Attachment.Type == "queued_command" {
			return []string{unpasted(line.Attachment.Prompt)}, line.Timestamp, true
		}
		if line.Type == "user" && line.Message.Role == "user" {
			texts, ok := promptTexts(line.Message.Content)
			for i := range texts {
				texts[i] = unpasted(texts[i])
			}
			return texts, line.Timestamp, ok
		}
	case "codex":
		if line.Type == "response_item" && line.Payload.Type == "message" && line.Payload.Role == "user" {
			texts, ok := promptTexts(line.Payload.Content)
			return texts, line.Timestamp, ok
		}
	case "pi":
		if line.Type == "message" && line.Message.Role == "user" {
			texts, ok := promptTexts(line.Message.Content)
			return texts, line.Timestamp, ok
		}
	}
	return nil, time.Time{}, false
}

// pastedContent marks where Claude Code 2.1.292 took typed text as a paste:
// it keeps a long answer typed during a turn as <pasted_content id="072c">,
// a line break, the text, a line break and </pasted_content id="072c">, as it
// did live on 2026-10-06.
var pastedContent = regexp.MustCompile(`</?pasted_content id="[^"]*">`)

// unpasted is text with the marks of where Claude Code took it as a paste
// taken out.
func unpasted(text string) string {
	return pastedContent.ReplaceAllString(text, "")
}

// promptTexts is the text of a user message's content: the string itself,
// or its text blocks, each alone and, when there are several, all joined,
// since a harness can hand several texts over in one message; none for a
// tool's result.
func promptTexts(content json.RawMessage) ([]string, bool) {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return []string{text}, true
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil, false
	}
	var texts []string
	for _, block := range blocks {
		switch block.Type {
		case "text", "input_text":
			texts = append(texts, block.Text)
		case "tool_result":
			return nil, false
		}
	}
	if len(texts) > 1 {
		texts = append(texts, strings.Join(texts, "\n"))
	}
	return texts, len(texts) > 0
}
