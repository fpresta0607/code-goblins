package monitor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/fsx"
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
// for the text without keeping it a second time: the SHA-256 of the words a
// reader sees in it, with each run of whitespace made one space, as a harness
// may wrap or trim it, and every character that draws nothing left out, as a
// harness may drop one. Claude Code 2.1.322 made each pasted tab four spaces
// and dropped a word joiner, U+2060, from two messages it took live on
// 2026-11-12, which then read Queued or Not confirmed for good (#1915).
func TextDigest(text string) string {
	seen := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsGraphic(r) {
			return r
		}
		return -1
	}, text)
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(seen), " ")))
	return hex.EncodeToString(sum[:])
}

// Took reports whether the harness's own record of conversation c shows the
// text digest names handed to its model at or after since: a prompt that
// started a turn, or text typed during a turn that the harness took into it
// at a tool call. Nothing else proves the agent has the text: a harness
// queues text typed during a turn, and Claude Code runs its prompt hook when
// it queues it, not when it takes it. Each record the conversation names is
// searched, since another session in the same folder, such as one the agent
// started itself, can be the newest, and each as far back as since.
func (h *HostProgress) Took(ctx context.Context, c Conversation, digest string, since time.Time) bool {
	for _, path := range h.conversationFiles(ctx, c) {
		if h.tookIn(c.Harness, recordSearch{path: path, digest: digest, since: since.UTC()}) {
			return true
		}
	}
	return false
}

// recordSearch is one search of the record at path for the text digest names,
// taken at or after since, which is in UTC so that every look at one delivery
// is the same search.
type recordSearch struct {
	path, digest string
	since        time.Time
}

// recordSearches bounds the searches a reader remembers. Past it all are
// forgotten, which costs each one still running a walk back through its
// record and nothing else.
const recordSearches = 1024

// tookIn reports whether harness's record at search's path shows its text
// taken. The first look reads the record back from its end, a
// transcriptEntryReach at a time, until one read holds entries that carry a
// time and none of them at or after since. A harness stamps an entry as it
// makes it and writes it then or later, and in 64 MiB of a record read on
// 2026-11-12 Claude Code 2.1.322 wrote at most 1 MiB on end with no time in
// it, so nothing behind such a read was taken after since. The look read only
// the record's last 4 MiB before, whatever since was, and the CEO's record
// had grown 10 MiB since it took two messages and 40 MiB since it took two
// more, live that day: all four read Queued or Not confirmed for good
// (#1915). Each later look reads only what the record gained, entries its
// harness has finished writing, so a delivery watched for a day does not read
// hours of its record again at every look.
func (h *HostProgress) tookIn(harness string, search recordSearch) bool {
	file, err := fsx.Open(search.path)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false
	}
	h.mu.Lock()
	floor, isLater := h.searched[search]
	h.mu.Unlock()
	searchedTo := floor
	var rest []byte
	for end := info.Size(); end > floor; {
		start := max(floor, end-transcriptEntryReach)
		piece := make([]byte, end-start, int(end-start)+len(rest))
		if _, err := file.ReadAt(piece, start); err != nil && !errors.Is(err, io.EOF) {
			return false
		}
		end = start
		if searchedTo == floor {
			// What follows the record's last line break is an entry its
			// harness is still writing.
			finished := bytes.LastIndexByte(piece, '\n') + 1
			if finished == 0 {
				continue
			}
			searchedTo, piece = start+int64(finished), piece[:finished]
		}
		entries := bytes.Split(append(piece, rest...), []byte("\n"))
		if start > floor {
			// The first of them begins before this read.
			rest, entries = entries[0], entries[1:]
		}
		hasTime, hasAfter := false, false
		for _, entry := range entries {
			texts, at, ok := handedText(harness, entry)
			hasTime = hasTime || !at.IsZero()
			if at.Before(search.since) {
				continue
			}
			hasAfter = true
			if ok && slices.ContainsFunc(texts, func(text string) bool { return TextDigest(text) == search.digest }) {
				h.mu.Lock()
				delete(h.searched, search)
				h.mu.Unlock()
				return true
			}
		}
		if !isLater && hasTime && !hasAfter {
			break
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.searched == nil || !isLater && len(h.searched) >= recordSearches {
		h.searched = map[recordSearch]int64{}
	}
	h.searched[search] = searchedTo
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
		return fleettree.PiSessions(h.Home, c.Dir)
	}
	return nil
}

// handedText reads entry, one line of harness's record, as text handed to
// its model on the user's behalf, and when. Claude Code records a prompt
// that starts a turn as a user message and text it took into a running turn
// as a queued_command attachment; Codex records either as a user message
// item; pi as a user message. A slash command Claude Code runs itself is
// its words: its tags in a user message, or a system local_command record,
// or for /remote-control only a bridge_status record that opens with it. A
// tool's result, a subagent's entries and whatever the agent itself wrote
// are none, and of those too it returns the time the entry carries, zero for
// an entry that carries none.
func handedText(harness string, entry []byte) ([]string, time.Time, bool) {
	var line struct {
		Type        string          `json:"type"`
		Subtype     string          `json:"subtype"`
		Content     json.RawMessage `json:"content"`
		Timestamp   time.Time       `json:"timestamp"`
		IsSidechain bool            `json:"isSidechain"`
		Attachment  struct {
			Type   string          `json:"type"`
			Prompt json.RawMessage `json:"prompt"`
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
			return nil, line.Timestamp, false
		}
		// A queued line's prompt is its text, or its blocks when it took a
		// picture.
		if line.Type == "attachment" && line.Attachment.Type == "queued_command" {
			texts, ok := promptTexts(line.Attachment.Prompt)
			for i := range texts {
				texts[i] = unpasted(texts[i])
			}
			return withoutPictureMarks(texts), line.Timestamp, ok
		}
		if line.Type == "user" && line.Message.Role == "user" {
			texts, ok := promptTexts(line.Message.Content)
			texts = withoutPictureMarks(texts)
			for i := range texts {
				texts[i] = unpasted(texts[i])
				if command, isCommand := fleettree.TypedCommand(texts[i]); isCommand && strings.HasPrefix(texts[i], "<command-") {
					texts[i] = command
				}
				// A shell command ran what was typed after its !, with or
				// without a space between them (#2242).
				if command, isShell := fleettree.ShellCommand(texts[i]); isShell {
					texts[i] = "! " + command
					texts = append(texts, "!"+command)
				}
			}
			return texts, line.Timestamp, ok
		}
		var text string
		if line.Type == "system" && json.Unmarshal(line.Content, &text) == nil {
			switch line.Subtype {
			case "local_command":
				command, ok := fleettree.TypedCommand(text)
				return []string{command}, line.Timestamp, ok
			case "bridge_status":
				command, ok := fleettree.StatusCommand(text)
				return []string{command}, line.Timestamp, ok
			}
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
	return nil, line.Timestamp, false
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

// pictureMark is where Claude Code 2.1.323 put a picture it took for the path
// that names it: right after the path in a line taken into a running turn,
// and in place of the path in a prompt that started one (#2541).
var pictureMark = regexp.MustCompile(`\[Image #\d+\]`)

// withoutPictureMarks is texts, and after them each of them that holds a
// picture's mark with its marks left out, so text taken with a picture is
// his words, and a mark he typed himself is still one of them.
func withoutPictureMarks(texts []string) []string {
	for _, text := range texts {
		if bare := pictureMark.ReplaceAllString(text, ""); bare != text {
			texts = append(texts, bare)
		}
	}
	return texts
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
