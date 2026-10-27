package fleettree

import (
	"bufio"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"io"
	"maps"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// A Codex rollout is a JSONL file Codex appends to as its session runs: its
// opening session_meta, then one entry per event. What its own terminal draws
// the conversation from is the app-server item stream, written as each item
// completes: a prompt (UserMessage), an answer (AgentMessage), an edit
// (FileChange), a command (CommandExecution), a sub-action of a command
// (Extension, ImageView) and a compaction. The rollout's response_items carry
// the calls themselves, written when a call starts and answered by its output
// when it ends, so a call still running is read from there.
//
// A Reasoning item's published summary is readable when recorded. Encrypted
// reasoning and raw content are never read as thinking blocks.
//
// Nothing a rollout holds is rewritten once written, so the log reads on from
// its last complete line, and only a file that shrank is read again.

// codexEntry is one line of a rollout: the shape every type shares, of which
// each type uses the part it writes.
type codexEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Payload   struct {
		Type             string           `json:"type"`
		Item             codexItem        `json:"item"`
		ID               string           `json:"id"`
		Model            string           `json:"model"`
		Effort           string           `json:"effort"`
		Reason           string           `json:"reason"`
		Message          string           `json:"message"`
		CallID           string           `json:"call_id"`
		Name             string           `json:"name"`
		Input            string           `json:"input"`
		Content          []codexPart      `json:"content"`
		Arguments        json.RawMessage  `json:"arguments"`
		Output           json.RawMessage  `json:"output"`
		Usage            *codexTokenUsage `json:"usage"`
		ThreadTokenUsage *codexTokenUsage `json:"thread_token_usage"`
		Info             *struct {
			Total         *codexTokenUsage `json:"total_token_usage"`
			Last          *codexTokenUsage `json:"last_token_usage"`
			ContextWindow int64            `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

// codexItem is one item of Codex's own conversation stream, of whichever type.
type codexItem struct {
	Type        string                 `json:"type"`
	ID          string                 `json:"id"`
	Content     []codexPart            `json:"content"`
	SummaryText []codexPart            `json:"summary_text"`
	Changes     map[string]codexChange `json:"changes"`
}

// codexPart is one part of a message's or a summary's content.
type codexPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	URL      string `json:"url"`
	ImageURL string `json:"image_url"`
}

type codexTokenUsage struct {
	Input  int64 `json:"input_tokens"`
	Cached int64 `json:"cached_input_tokens"`
	Output int64 `json:"output_tokens"`
	Total  int64 `json:"total_tokens"`
}

func (usage *codexTokenUsage) valid() bool {
	return usage != nil && usage.Input >= 0 && usage.Cached >= 0 && usage.Cached <= usage.Input && usage.Output >= 0 && usage.Total >= 0 && usage.Input <= math.MaxInt64-usage.Output
}

type codexPictureAt struct {
	offset   int64
	index    int
	isOutput bool
}

// codexChange is one file a FileChange item changed: its unified diff for an
// update, and its whole content for a file added or deleted.
type codexChange struct {
	Type        string `json:"type"`
	Content     string `json:"content"`
	UnifiedDiff string `json:"unified_diff"`
}

// codexMessageLog is one rollout read as messages, kept between reads so each
// read parses only the lines written since the last. asked, when a reader
// last asked for it, is guarded by the Reader's messagesMu, and everything
// else by mu.
type codexMessageLog struct {
	mu    sync.Mutex
	path  string
	asked time.Time

	read     int64
	epoch    uint64
	messages []Message
	changed  []uint64
	revision uint64
	// reply is the message the assistant's next block belongs to, -1 when
	// the next block begins one, and model the model of the turn being read.
	reply    int
	model    string
	calls    map[string]blockAt
	usage    Usage
	pictures map[string]codexPictureAt
}

// reset starts the log over, reading the rollout anew from its first byte.
func (l *codexMessageLog) reset(epoch uint64) {
	l.read, l.epoch = 0, epoch
	l.messages, l.changed, l.revision = nil, nil, 0
	l.reply, l.model = -1, ""
	l.calls = map[string]blockAt{}
	l.usage = Usage{}
	l.pictures = map[string]codexPictureAt{}
}

// update parses the entries written since the last read. A rollout that shrank
// was replaced, and is read again from its start as a read of its own. The
// caller holds l.mu.
func (l *codexMessageLog) update(epoch func() uint64) error {
	file, err := fsx.Open(l.path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < l.read {
		l.reset(epoch())
	}
	l.read, err = readOn(file, l.read, info.Size(), func(start int64, lines [][]byte) {
		for _, line := range lines {
			l.entry(line, start)
			start += int64(len(line)) + 1
		}
	})
	return err
}

// entry folds one rollout entry, at offset in the file, into the log.
func (l *codexMessageLog) entry(line []byte, offset int64) {
	var entry codexEntry
	if json.Unmarshal(line, &entry) != nil {
		return
	}
	id := "@" + strconv.FormatInt(offset, 10)
	switch entry.Type {
	case "turn_context":
		// Each turn's context names the model and effort it runs with, as
		// the session's /model last set them.
		l.model = entry.Payload.Model
		if entry.Payload.Effort != "" {
			l.usage.Effort = entry.Payload.Effort
		}
	case "token_usage_record":
		// Codex writes one usage record as each model response ends, so the
		// next block of the assistant's begins another reply.
		l.reply = -1
		l.recordUsage(entry.Payload.ThreadTokenUsage, entry.Payload.Usage, true)
	case "compacted":
		l.compaction(entry, id)
	case "event_msg":
		switch entry.Payload.Type {
		case "item_completed":
			l.completed(entry, id, offset)
		case "token_count":
			if info := entry.Payload.Info; info != nil {
				l.recordUsage(info.Total, info.Last, false)
				if info.ContextWindow > 0 {
					l.usage.ContextWindow = info.ContextWindow
				}
			}
		case "turn_aborted":
			l.reply = -1
			l.add(Message{ID: id, Kind: MessageInterrupt, At: entry.Timestamp, Text: bounded(strings.TrimSpace(entry.Payload.Reason), messageTextLimit)})
		}
	case "response_item":
		l.response(entry, id, offset)
	}
}

// completed folds one item of the conversation stream into the log. An item
// this reader does not show, such as a command's own record of itself, whose
// output already came with the call, shows nothing; and ContextCompaction is
// passed over, since the compacted record that wrote it already showed the
// compaction.
func (l *codexMessageLog) completed(entry codexEntry, id string, offset int64) {
	item := entry.Payload.Item
	switch item.Type {
	case "UserMessage":
		l.reply = -1
		l.prompt(entry.Timestamp, cmp.Or(item.ID, id), codexText(item.Content), l.keepPictures(item.Content, offset, false))
	case "AgentMessage":
		if text := strings.TrimSpace(codexText(item.Content)); text != "" {
			l.block(entry.Timestamp, id, Block{Kind: BlockText, Text: bounded(text, messageTextLimit)})
		}
	case "Reasoning":
		if text := strings.TrimSpace(codexText(item.SummaryText)); text != "" {
			l.block(entry.Timestamp, id, Block{Kind: BlockThinking, Text: bounded(text, messageTextLimit)})
		}
	case "FileChange":
		l.edit(item)
	}
}

// response folds one response_item into the log: a call Codex has started, the
// output that answers one, or a message a child agent sent its parent, which a
// Claude Code goblin reads as a task notification and this shows the same way.
func (l *codexMessageLog) response(entry codexEntry, id string, offset int64) {
	payload := entry.Payload
	switch payload.Type {
	case "function_call", "custom_tool_call":
		l.block(entry.Timestamp, id, Block{Kind: BlockCall, CallID: payload.CallID, Tool: payload.Name, Subject: codexSubject(payload.Input, payload.Arguments), State: CallRunning})
	case "function_call_output", "custom_tool_call_output":
		l.outcome(payload.CallID, payload.Output, offset)
	case "agent_message":
		l.reply = -1
		l.add(Message{ID: cmp.Or(payload.ID, id), Kind: MessagePrompt, At: entry.Timestamp, Source: FromNotice, Text: bounded(codexText(payload.Content), messageTextLimit)})
	}
}

// compaction shows the compaction Codex recorded. It keeps no summary of what
// it compacted into, so the message carries none.
func (l *codexMessageLog) compaction(entry codexEntry, id string) {
	l.reply = -1
	l.add(Message{ID: id, Kind: MessageCompaction, At: entry.Timestamp, Trigger: "auto", Text: bounded(strings.TrimSpace(entry.Payload.Message), messageTextLimit)})
}

// prompt adds a prompt to the log, with where it came from.
func (l *codexMessageLog) prompt(at time.Time, id, text string, pictures []Picture) {
	source, words := promptSource(strings.TrimSpace(systemReminder.ReplaceAllString(text, "")))
	message := Message{ID: id, Kind: MessagePrompt, At: at, Source: source, Text: bounded(words, messageTextLimit), Pictures: pictures}
	if message.Text != "" || len(pictures) > 0 {
		l.add(message)
	}
}

// block appends one block of the assistant's to the reply being written, and
// begins a reply where none is open.
func (l *codexMessageLog) block(at time.Time, id string, block Block) {
	if l.reply < 0 {
		l.reply = l.add(Message{ID: id, Kind: MessageReply, At: at, Model: l.model})
	}
	reply := &l.messages[l.reply]
	if block.Kind == BlockCall && block.CallID != "" {
		l.calls[block.CallID] = blockAt{l.reply, len(reply.Blocks)}
	}
	reply.Blocks = append(reply.Blocks, block)
	l.touch(l.reply)
}

// outcome fills in the call a rollout's output answers. An output that no call
// opened is left out, as Claude Code's reader leaves out a result that answers
// nothing.
func (l *codexMessageLog) outcome(callID string, output json.RawMessage, offset int64) {
	where, ok := l.calls[callID]
	if !ok || where.message >= len(l.messages) || where.block >= len(l.messages[where.message].Blocks) {
		return
	}
	text := resultText(output)
	block := &l.messages[where.message].Blocks[where.block]
	block.Result = bounded(text, resultLimit)
	block.State = "done"
	var parts []codexPart
	if json.Unmarshal(output, &parts) == nil {
		block.Pictures = l.keepPictures(parts, offset, true)
	}
	// Codex opens its command's own report with these words either way, the
	// exit code of each command it ran staying in the report's text.
	if strings.HasPrefix(strings.TrimSpace(text), "Script failed") {
		block.State = "failed"
	}
	l.touch(where.message)
}

func (l *codexMessageLog) recordUsage(total, last *codexTokenUsage, shouldAdd bool) {
	if total.valid() {
		l.usage.InputTokens, l.usage.OutputTokens, l.usage.CacheReadTokens = total.Input, total.Output, total.Cached
	} else if shouldAdd && last.valid() && l.usage.InputTokens <= math.MaxInt64-last.Input && l.usage.OutputTokens <= math.MaxInt64-last.Output && l.usage.CacheReadTokens <= math.MaxInt64-last.Cached {
		l.usage.InputTokens += last.Input
		l.usage.OutputTokens += last.Output
		l.usage.CacheReadTokens += last.Cached
	}
	if last.valid() {
		// Codex's input count already includes cache hits.
		l.usage.ContextTokens = last.Total
		if last.Total == 0 {
			l.usage.ContextTokens = last.Input + last.Output
		}
	}
	if (total.valid() || last.valid()) && l.model != "" {
		l.usage.Model = l.model
	}
}

func codexPictureData(part codexPart) (string, string, bool) {
	var url string
	switch part.Type {
	case "image":
		url = part.URL
	case "input_image":
		url = part.ImageURL
	default:
		return "", "", false
	}
	header, data, hasData := strings.Cut(url, ",")
	media := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	padding := len(data) - len(strings.TrimRight(data, "="))
	if !hasData || header != "data:"+media+";base64" || !slices.Contains(pictureTypes, media) || len(data) == 0 || len(data) > pictureEntryReach || len(data)%4 != 0 || padding > 2 {
		return "", "", false
	}
	return media, data, true
}

func (l *codexMessageLog) keepPictures(parts []codexPart, offset int64, isOutput bool) []Picture {
	var pictures []Picture
	for index, part := range parts {
		media, data, isPicture := codexPictureData(part)
		if !isPicture {
			continue
		}
		id := strconv.FormatInt(offset, 10) + ":" + strconv.Itoa(index)
		l.pictures[id] = codexPictureAt{offset, index, isOutput}
		size := len(data)/4*3 - (len(data) - len(strings.TrimRight(data, "=")))
		pictures = append(pictures, Picture{ID: id, MediaType: media, Bytes: size})
	}
	return pictures
}

func (r *Reader) codexMessagePicture(path, id string) (string, []byte, error) {
	r.messagesMu.Lock()
	log := r.codexLogs[path]
	r.messagesMu.Unlock()
	if log == nil {
		return "", nil, ErrNoRecord
	}
	log.mu.Lock()
	at, isKept := log.pictures[id]
	log.mu.Unlock()
	if !isKept {
		return "", nil, ErrNoRecord
	}
	file, err := fsx.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	line, err := bufio.NewReader(io.NewSectionReader(file, at.offset, pictureEntryReach)).ReadBytes('\n')
	if err != nil {
		return "", nil, err
	}
	var entry codexEntry
	if json.Unmarshal(line, &entry) != nil {
		return "", nil, ErrNoRecord
	}
	parts := entry.Payload.Item.Content
	if at.isOutput && json.Unmarshal(entry.Payload.Output, &parts) != nil {
		return "", nil, ErrNoRecord
	}
	if at.index >= len(parts) {
		return "", nil, ErrNoRecord
	}
	media, encoded, isPicture := codexPictureData(parts[at.index])
	if !isPicture {
		return "", nil, ErrNoRecord
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return "", nil, ErrNoRecord
	}
	return media, data, nil
}

// edit folds a FileChange item into the call that made it: the call before it
// that no output has answered yet, since Codex writes the change as it lands
// and the call's output only after.
func (l *codexMessageLog) edit(item codexItem) {
	if len(item.Changes) == 0 {
		return
	}
	where, ok := l.openCall()
	if !ok {
		return
	}
	var edit Edit
	for _, path := range slices.Sorted(maps.Keys(item.Changes)) {
		change := item.Changes[path]
		for _, line := range codexChangedLines(change) {
			switch {
			case strings.HasPrefix(line, "+"):
				edit.Added++
			case strings.HasPrefix(line, "-"):
				edit.Removed++
			}
			if len(edit.Diff) < diffLinesKept {
				edit.Diff = append(edit.Diff, bounded(line, agentLineLimit))
			}
		}
	}
	block := &l.messages[where.message].Blocks[where.block]
	block.Edit = edit
	l.touch(where.message)
}

// openCall is the call that no output has answered yet, from the newest
// backwards: the call a change that lands now belongs to.
func (l *codexMessageLog) openCall() (blockAt, bool) {
	for message := len(l.messages) - 1; message >= 0; message-- {
		blocks := l.messages[message].Blocks
		for block := len(blocks) - 1; block >= 0; block-- {
			if blocks[block].Kind == BlockCall && blocks[block].State == CallRunning {
				return blockAt{message, block}, true
			}
		}
	}
	return blockAt{}, false
}

// codexChangedLines are the lines a FileChange item says it changed: its
// unified diff as it is for an update, each line of the file marked added or
// removed as a diff marks it for one written or deleted whole.
func codexChangedLines(change codexChange) []string {
	if change.Type == "update" {
		return strings.Split(strings.TrimSuffix(change.UnifiedDiff, "\n"), "\n")
	}
	mark := "+"
	if change.Type == "delete" {
		mark = "-"
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(change.Content, "\n"), "\n") {
		if line != "" {
			lines = append(lines, mark+line)
		}
	}
	return lines
}

// codexText is the text of a message's or a summary's parts.
func codexText(parts []codexPart) string {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// codexSubject is what a call was made on: the first line of the script the
// exec tool was given, or the arguments of a function call, read as Claude
// Code's own calls are.
func codexSubject(input string, arguments json.RawMessage) string {
	if line := firstLine(input); line != "" {
		return bounded(line, agentLineLimit)
	}
	return bounded(callSubject(arguments, ""), agentLineLimit)
}

// add appends message to the log and returns where it is.
func (l *codexMessageLog) add(message Message) int {
	message.Seq = len(l.messages)
	l.revision++
	l.messages = append(l.messages, message)
	l.changed = append(l.changed, l.revision)
	return message.Seq
}

// touch marks the message at index changed.
func (l *codexMessageLog) touch(index int) {
	l.revision++
	l.changed[index] = l.revision
}

// page is the messages before seq before, the newest page of them; a negative
// before reads the newest.
func (l *codexMessageLog) page(before int) MessagePage {
	end := len(l.messages)
	if before >= 0 {
		end = min(end, before)
	}
	start := max(0, end-MessagesPerPage)
	page := MessagePage{Messages: make([]Message, 0, end-start), More: start > 0}
	for _, message := range l.messages[start:end] {
		page.Messages = append(page.Messages, shared(message))
	}
	page.Usage = usageOf(l.usage, l.usage.ContextWindow)
	return page
}

// since is every message that began or changed after cursor, with where the
// reader then stands.
func (l *codexMessageLog) since(cursor MessageCursor) ([]Message, MessageCursor, bool) {
	now := MessageCursor{l.epoch, l.revision}
	if cursor.epoch != l.epoch {
		return nil, now, true
	}
	var messages []Message
	for i, revision := range l.changed {
		if revision > cursor.revision {
			messages = append(messages, shared(l.messages[i]))
		}
	}
	return messages, now, false
}

// isCodexRollout says whether path is a Codex rollout, which Codex keeps under
// ~/.codex/sessions.
func isCodexRollout(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/.codex/sessions/")
}

// codexLog is the log of the rollout at path, read on to its end, with its mu
// held for the caller to unlock.
func (r *Reader) codexLog(path string) (*codexMessageLog, error) {
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	epoch := func() uint64 { return r.messageEpoch.Add(1) }
	r.messagesMu.Lock()
	if r.codexLogs == nil {
		r.codexLogs = map[string]*codexMessageLog{}
	}
	log := r.codexLogs[path]
	if log == nil {
		log = &codexMessageLog{path: path}
		log.reset(epoch())
		r.codexLogs[path] = log
	}
	log.asked = now
	r.forgetCodexLogs(now)
	r.messagesMu.Unlock()
	log.mu.Lock()
	if err := log.update(epoch); err != nil {
		log.mu.Unlock()
		r.messagesMu.Lock()
		if r.codexLogs[path] == log {
			delete(r.codexLogs, path)
		}
		r.messagesMu.Unlock()
		return nil, err
	}
	return log, nil
}

// forgetCodexLogs drops the rollouts no reader asked for in messageLogIdle.
// The caller holds r.messagesMu.
func (r *Reader) forgetCodexLogs(now time.Time) {
	for path, log := range r.codexLogs {
		if now.Sub(log.asked) > messageLogIdle {
			delete(r.codexLogs, path)
		}
	}
}
