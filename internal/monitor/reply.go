package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

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
// harness's own record: Claude Code's conversation, the session Herdr names
// or else the newest Claude Code keeps for the goblin's worktree, and Codex's
// rollout, the session Herdr names or else the newest that names the
// worktree. pi's is read off its screen, the one harness whose record this
// does not read.
func (h *HostProgress) LastReply(ctx context.Context, meta state.TaskMeta, sample EndpointSample) string {
	switch strings.ToLower(sample.Harness) {
	case "claude":
		if sample.Session != "" {
			return lastReplyIn(h.sessionTranscript("claude", sample.Session), claudeReply)
		}
		return lastReplyIn(h.newestClaudeConversation(meta.Worktree), claudeReply)
	case "codex":
		if sample.Session != "" {
			return lastReplyIn(h.sessionTranscript("codex", sample.Session), codexReply)
		}
		rollout, _ := h.newestNativeCodexRollout(ctx, meta.Worktree)
		return lastReplyIn(rollout, codexReply)
	case "pi":
		return screenReply(sample.Capture)
	}
	return ""
}

// sessionTranscript is the transcript the harness keeps for session, its
// first pattern's newest match: a Claude subagent's own transcript is never
// the goblin's reply.
func (h *HostProgress) sessionTranscript(harness, session string) string {
	if h.Home == "" || !sessionID.MatchString(session) {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(h.Home, strings.ReplaceAll(transcriptPatterns[harness][0], "{session}", session)))
	return newestFile(matches)
}

// claudeFolderLimit is the longest folder name Claude Code keeps a working
// folder's conversations under.
const claudeFolderLimit = 200

// claudeFolder is the folder Claude Code 2.1.292 keeps the conversations
// held in dir under, named as it names it: each UTF-16 unit of dir but an
// ASCII letter or digit as a hyphen, and a name longer than
// claudeFolderLimit cut there and followed by a hyphen and the base-36
// absolute value of dir's 32-bit string hash. A goblin's worktree under a
// long scratch path took the cut, live on 2026-10-06.
func claudeFolder(dir string) string {
	units := utf16.Encode([]rune(dir))
	folder := make([]byte, len(units))
	var hash int32
	for i, unit := range units {
		folder[i] = '-'
		if unit < utf8.RuneSelf && (unicode.IsLetter(rune(unit)) || unicode.IsDigit(rune(unit))) {
			folder[i] = byte(unit)
		}
		hash = hash*31 + int32(unit)
	}
	if len(folder) <= claudeFolderLimit {
		return string(folder)
	}
	return string(folder[:claudeFolderLimit]) + "-" + strconv.FormatInt(max(int64(hash), -int64(hash)), 36)
}

// claudeConversations is every conversation Claude Code keeps in the folder
// for dir, which it names after the folder a session starts in.
func (h *HostProgress) claudeConversations(dir string) []string {
	if h.Home == "" || !filepath.IsAbs(dir) {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(h.Home, ".claude", "projects", claudeFolder(dir), "*.jsonl"))
	return matches
}

// newestClaudeConversation is the conversation Claude Code last wrote in the
// folder it keeps for worktree. A native goblin's terminal names no session,
// and the goblin is the one Claude Code working there; the folder its shell
// is in can move, so the entries' own folder is not read.
func (h *HostProgress) newestClaudeConversation(worktree string) string {
	return newestFile(h.claudeConversations(worktree))
}

// newestFile is the path in paths whose file was written last.
func newestFile(paths []string) string {
	newest := ""
	var written int64
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && (newest == "" || info.ModTime().UnixNano() > written) {
			newest, written = path, info.ModTime().UnixNano()
		}
	}
	return newest
}

// lastReplyIn reads the reply read finds in the end of the transcript at
// path.
func lastReplyIn(path string, read func(entries [][]byte) string) string {
	return read(transcriptTail(path))
}

// transcriptTail is the entries in the end of the transcript at path, the
// last transcriptEntryReach of it, and none when it cannot be read.
func transcriptTail(path string) [][]byte {
	if path == "" {
		return nil
	}
	file, err := fsx.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil
	}
	start := max(0, info.Size()-transcriptEntryReach)
	tail := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(tail, start); err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	entries := bytes.Split(tail, []byte("\n"))
	if start > 0 {
		// The first piece may begin part way through an entry.
		entries = entries[1:]
	}
	return entries
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
