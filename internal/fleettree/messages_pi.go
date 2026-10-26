package fleettree

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// A pi session is a JSONL file whose entries form a tree: each entry names its
// parent by id, and the entry written last is the leaf the view follows, as
// pi's own reload reads it. A /tree selection writes nothing at all: the next
// turn's first entry names the selected entry as its parent, so the branch
// appears once that turn is written. The path a view shows is therefore the
// chain from the entry written last back to its root, never the file's lines in
// order. A session writes no file until its first reply ends.
//
// The log keeps, for every entry, only what its tree needs to be followed and
// what its message shows, each piece already clipped: an entry's own line stays
// on disk, so an image's bytes are read back from it when a view asks for them
// and never held here.

// piSessionEntry is one line of a pi session file: the session header, or an
// entry of its tree with the fields its type uses. It is what one line is
// unmarshalled into, and it is not kept once the line is retained.
type piSessionEntry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
	Summary   string          `json:"summary"`
}

// piMessage is what a pi "message" entry holds, of whichever role.
type piMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stopReason"`
	Model        string          `json:"model"`
	Provider     string          `json:"provider"`
	ErrorMessage string          `json:"errorMessage"`
	ToolCallID   string          `json:"toolCallId"`
	IsError      bool            `json:"isError"`
	Command      string          `json:"command"`
	Output       string          `json:"output"`
	ExitCode     *int            `json:"exitCode"`
	Summary      string          `json:"summary"`
	Usage        piUsage         `json:"usage"`
}

// piUsage is what one assistant message says the request spent.
type piUsage struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cacheRead"`
	TotalTokens int64 `json:"totalTokens"`
	Cost        struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

// piBlock is one block of a user's or an assistant's content.
type piBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Data      string          `json:"data"`
	MIMEType  string          `json:"mimeType"`
}

// piRetained is what the log keeps of one entry: the id and parent its tree is
// followed by, the byte its line begins at, and what it shows, clipped. No raw
// JSON and no image bytes are kept.
type piRetained struct {
	id        string
	parent    string
	hasParent bool
	kind      string
	at        time.Time
	offset    int64

	role         string
	text         string
	output       string
	errors       string
	model        string
	stopReason   string
	errorMessage string
	blocks       []Block
	pictures     []Picture
	// inputTokens, outputTokens, cacheReadTokens and costUSD are what one
	// assistant message recorded, and contextTokens what its request carried.
	inputTokens, outputTokens, cacheReadTokens int64
	contextTokens                              int64
	costUSD                                    float64

	// A tool result answers the call answers, with result or a failure.
	answers        string
	result         string
	resultPictures []Picture
	failed         bool
}

// piPictureAt is where a picture is: the offset of its entry's line, and the
// block of that entry's content.
type piPictureAt struct {
	offset int64
	index  int
}

// piCallAt is where a tool call was put in the view, so the result that answers
// it finds it.
type piCallAt struct {
	message, block int
}

// piMessageLog is one pi session read as messages, kept between reads so a read
// parses only the lines written since the last and rebuilds only when the
// branch itself moved. asked, when a reader last asked for it, is guarded by the
// Reader's messagesMu, and everything else by mu.
type piMessageLog struct {
	mu    sync.Mutex
	path  string
	asked time.Time

	// offset is the byte just past the last complete line read, entries every
	// entry read, and parsed the lines this log has parsed, which a read that
	// finds the file unchanged never adds to.
	offset  int64
	entries []piRetained
	parsed  int

	leaf     string
	messages []Message
	changed  []uint64
	calls    map[string]piCallAt
	pictures map[string]piPictureAt
	revision uint64
	epoch    uint64
	// home reads the harness's own model catalog for the context window, and
	// usage is what the branch says the session spent; windowFor is the model
	// whose window window holds.
	home      string
	usage     Usage
	window    int64
	windowFor string
}

// reset starts the log over, reading the conversation anew from its first byte.
// A picture's place is an offset, so every one read before goes with it.
func (l *piMessageLog) reset(epoch uint64) {
	l.offset, l.entries, l.parsed = 0, nil, 0
	l.leaf = ""
	l.messages, l.changed = nil, nil
	l.calls = map[string]piCallAt{}
	l.pictures = map[string]piPictureAt{}
	l.revision = 0
	l.epoch = epoch
	l.usage, l.window, l.windowFor = Usage{}, 0, ""
}

// update reads the lines written since the last read, and does nothing at all
// when the file is as it was: a view asks twice a second, and a session that
// wrote nothing since has nothing to parse, decode or rebuild. Every entry of
// the batch is kept whether or not it continues the branch, since the file is
// read past it either way. A session that has written no file yet, as pi writes
// none before its first reply ends, holds no messages.
func (l *piMessageLog) update(epoch func() uint64) error {
	info, err := os.Stat(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() < l.offset {
		l.reset(epoch())
	}
	if info.Size() == l.offset {
		return nil
	}
	file, err := fsx.Open(l.path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(l.offset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(file)
	grown := make([]piRetained, 0, 8)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 || line[len(line)-1] != '\n' {
			// A line still being written is read once it ends.
			break
		}
		at := l.offset
		l.offset += int64(len(line))
		l.parsed++
		if entry, ok := piLine(line); ok {
			grown = append(grown, l.retain(entry, at))
		}
		if readErr != nil {
			break
		}
	}
	if len(grown) == 0 {
		return nil
	}
	// The whole batch is kept first, so an entry after a /tree switch in the
	// same batch is never lost to the read that moved past it.
	l.entries = append(l.entries, grown...)
	if l.continues(grown) {
		for _, entry := range grown {
			l.show(entry)
			l.leaf = entry.id
		}
		return nil
	}
	l.rebuild(epoch)
	return nil
}

// piLine is the entry a line of a session file holds, and whether it holds one:
// the header, an empty line and anything that is not an entry hold none.
func piLine(line []byte) (piSessionEntry, bool) {
	var entry piSessionEntry
	if err := json.Unmarshal(line, &entry); err != nil || entry.Type == "" || entry.Type == "session" || entry.ID == "" {
		return piSessionEntry{}, false
	}
	return entry, true
}

// continues says whether every entry of grown hangs under the leaf the view
// holds, in order, which makes the whole batch the branch's next entries.
func (l *piMessageLog) continues(grown []piRetained) bool {
	leaf, isFirst := l.leaf, l.leaf == ""
	for _, entry := range grown {
		switch {
		case isFirst && entry.hasParent, !isFirst && (!entry.hasParent || entry.parent != leaf):
			return false
		}
		leaf, isFirst = entry.id, false
	}
	return true
}

// rebuild takes the branch anew from every entry read so far, as a /tree switch
// makes necessary, and starts the view afresh: nothing a page before it held
// still stands. Its epoch comes from the Reader's global allocator, never from
// the log's own count, so a log made again after idle eviction can never hand a
// view holding an older cursor the epoch it had.
func (l *piMessageLog) rebuild(epoch func() uint64) {
	l.epoch = epoch()
	l.messages, l.changed = nil, nil
	l.calls = map[string]piCallAt{}
	l.usage, l.window, l.windowFor = Usage{}, 0, ""
	order := l.branchOrder()
	if len(order) > 0 {
		l.leaf = l.entries[order[len(order)-1]].id
	}
	for _, index := range order {
		l.show(l.entries[index])
	}
}

// branchOrder is the entries of the path from the entry written last back to
// the root, as their places in the log's entries, oldest first.
func (l *piMessageLog) branchOrder() []int {
	at := make(map[string]int, len(l.entries))
	for i, entry := range l.entries {
		at[entry.id] = i
	}
	order := make([]int, 0, len(l.entries))
	visited := make(map[string]bool, len(l.entries))
	for index := len(l.entries) - 1; index >= 0 && !visited[l.entries[index].id]; {
		entry := l.entries[index]
		visited[entry.id] = true
		order = append(order, index)
		if !entry.hasParent {
			break
		}
		parent, ok := at[entry.parent]
		if !ok {
			break
		}
		index = parent
	}
	for left, right := 0, len(order)-1; left < right; left, right = left+1, right-1 {
		order[left], order[right] = order[right], order[left]
	}
	return order
}

// add appends a message to the branch, as one no read has answered before.
func (l *piMessageLog) add(message Message) {
	l.revision++
	message.Seq = len(l.messages)
	l.messages = append(l.messages, message)
	l.changed = append(l.changed, l.revision)
}

// touch marks the message at index as changed since the last read, which a
// result that fills in its call does.
func (l *piMessageLog) touch(index int) {
	if index < 0 || index >= len(l.messages) {
		return
	}
	l.revision++
	l.changed[index] = l.revision
}

// show adds what one retained entry shows: its message, its compaction or
// branch summary, and nothing for the entries that record a setting or an
// extension's own data.
func (l *piMessageLog) show(entry piRetained) {
	switch entry.kind {
	case "compaction":
		l.add(Message{ID: entry.id, Kind: MessageCompaction, At: entry.at, Trigger: "auto", Text: entry.text})
		return
	case "branch_summary":
		l.add(Message{ID: entry.id, Kind: MessageCompaction, At: entry.at, Trigger: "branch", Text: entry.text})
		return
	case "message":
	default:
		return
	}
	switch entry.role {
	case "user":
		source, text := promptSource(entry.text)
		l.add(Message{ID: entry.id, Kind: MessagePrompt, At: entry.at, Source: source, Text: text, Pictures: entry.pictures})
	case "assistant":
		at := len(l.messages)
		for index, block := range entry.blocks {
			if block.Kind == BlockCall && block.CallID != "" {
				l.calls[block.CallID] = piCallAt{message: at, block: index}
			}
		}
		// The blocks are the displayed message's own, since a result that
		// answers a call fills that call in place, and the retained entry must
		// stay as it was read for a branch that leaves the result out.
		l.add(Message{ID: entry.id, Kind: MessageReply, At: entry.at, Model: entry.model, Blocks: slices.Clone(entry.blocks)})
		l.usage.add(entry.inputTokens, entry.outputTokens, entry.cacheReadTokens, entry.model)
		l.usage.CostUSD += entry.costUSD
		if entry.contextTokens > 0 {
			l.usage.ContextTokens = entry.contextTokens
		}
		if entry.model != "" && entry.model != l.windowFor {
			l.windowFor, l.window = entry.model, piContextWindow(l.home, entry.model)
		}
		if entry.stopReason == "aborted" || entry.stopReason == "error" {
			why := strings.TrimSpace(entry.errorMessage)
			if why == "" {
				why = entry.stopReason
			}
			l.add(Message{ID: entry.id + ":stop", Kind: MessageInterrupt, At: entry.at, Text: why})
		}
	case "toolResult":
		l.applyResult(entry)
	case "bashExecution":
		l.add(Message{ID: entry.id, Kind: MessagePrompt, At: entry.at, Source: FromShell, Text: entry.text, Output: entry.output, Errors: entry.errors})
	case "compactionSummary":
		l.add(Message{ID: entry.id, Kind: MessageCompaction, At: entry.at, Trigger: "auto", Text: entry.text})
	case "branchSummary":
		l.add(Message{ID: entry.id, Kind: MessageCompaction, At: entry.at, Trigger: "branch", Text: entry.text})
	}
}

// applyResult fills in the call a tool result answers, and a result that answers
// no call is left out: pi writes one result for each call it made.
func (l *piMessageLog) applyResult(entry piRetained) {
	where, ok := l.calls[entry.answers]
	if !ok || where.message >= len(l.messages) || where.block >= len(l.messages[where.message].Blocks) {
		return
	}
	block := &l.messages[where.message].Blocks[where.block]
	block.Result = entry.result
	block.Pictures = entry.resultPictures
	block.State = "done"
	if entry.failed {
		block.State = "failed"
	}
	l.touch(where.message)
}

// retain keeps what one entry's line shows, clipped, registering where each of
// its images is so the bytes are read back from the line rather than kept.
func (l *piMessageLog) retain(entry piSessionEntry, offset int64) piRetained {
	kept := piRetained{id: entry.ID, kind: entry.Type, offset: offset}
	if entry.ParentID != nil {
		kept.parent, kept.hasParent = *entry.ParentID, true
	}
	kept.at, _ = time.Parse(time.RFC3339Nano, entry.Timestamp)
	switch entry.Type {
	case "compaction", "branch_summary":
		kept.text = piTruncate(entry.Summary, messageTextLimit)
		return kept
	case "message":
	default:
		return kept
	}
	var said piMessage
	if err := json.Unmarshal(entry.Message, &said); err != nil {
		return kept
	}
	kept.role = said.Role
	switch said.Role {
	case "user":
		kept.text, kept.pictures = l.content(entry.ID, offset, said.Content)
	case "assistant":
		kept.model = strings.Trim(strings.Join([]string{said.Provider, said.Model}, "/"), "/")
		kept.stopReason, kept.errorMessage = said.StopReason, said.ErrorMessage
		kept.inputTokens, kept.outputTokens, kept.cacheReadTokens = said.Usage.Input, said.Usage.Output, said.Usage.CacheRead
		kept.contextTokens, kept.costUSD = said.Usage.TotalTokens, said.Usage.Cost.Total
		for index, block := range piBlocks(said.Content) {
			switch block.Type {
			case "text":
				kept.blocks = append(kept.blocks, Block{Kind: BlockText, Text: piTruncate(block.Text, messageTextLimit)})
			case "thinking":
				kept.blocks = append(kept.blocks, Block{Kind: BlockThinking, Text: piTruncate(block.Thinking, messageTextLimit)})
			case "toolCall":
				kept.blocks = append(kept.blocks, Block{Kind: BlockCall, CallID: block.ID, Tool: block.Name, Subject: piSubject(block.Arguments), State: "running"})
			}
			l.picture(entry.ID, offset, index, block)
		}
	case "toolResult":
		kept.answers, kept.failed = said.ToolCallID, said.IsError
		kept.result, kept.resultPictures = l.content(entry.ID, offset, said.Content)
	case "bashExecution":
		kept.text = piTruncate(said.Command, messageTextLimit)
		kept.output = piTruncate(said.Output, resultLimit)
		if said.ExitCode != nil && *said.ExitCode != 0 {
			kept.errors = "exit " + strconv.Itoa(*said.ExitCode)
		}
	case "compactionSummary", "branchSummary":
		kept.text = piTruncate(said.Summary, messageTextLimit)
	}
	return kept
}

// content is what a content's blocks say, its text joined and clipped, with the
// pictures it holds kept by where they are.
func (l *piMessageLog) content(id string, offset int64, content json.RawMessage) (string, []Picture) {
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}
	var plain string
	if json.Unmarshal([]byte(trimmed), &plain) == nil {
		return piTruncate(plain, messageTextLimit), nil
	}
	var texts []string
	var pictures []Picture
	for index, block := range piBlocks(content) {
		switch block.Type {
		case "text":
			texts = append(texts, block.Text)
		case "image":
			if picture, ok := l.picture(id, offset, index, block); ok {
				pictures = append(pictures, picture)
			}
		}
	}
	return piTruncate(strings.Join(texts, ""), messageTextLimit), pictures
}

// picture keeps where an image of an entry is, as a picture of the log, and
// never the image itself: only a media type the board may serve is kept, as the
// Claude reader keeps only those, and MessagePicture reads the bytes back from
// the entry when a view asks for them.
func (l *piMessageLog) picture(id string, offset int64, index int, block piBlock) (Picture, bool) {
	if block.Type != "image" || block.Data == "" || !slices.Contains(pictureTypes, block.MIMEType) {
		return Picture{}, false
	}
	name := id + ":" + strconv.Itoa(index)
	l.pictures[name] = piPictureAt{offset: offset, index: index}
	// The size of what the base64 would decode to, as the Claude reader sizes a
	// picture it has not read.
	size := len(block.Data)/4*3 - (len(block.Data) - len(strings.TrimRight(block.Data, "=")))
	return Picture{ID: name, MediaType: block.MIMEType, Bytes: size}, true
}

// piBlocks decodes a content's blocks, of which an entry this reader does not
// know holds none.
func piBlocks(content json.RawMessage) []piBlock {
	if len(content) == 0 {
		return nil
	}
	var blocks []piBlock
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	return blocks
}

// piSubject is what a call was made on, read from the argument the tools of pi
// and the fleet name it by.
func piSubject(arguments json.RawMessage) string {
	if len(arguments) == 0 {
		return ""
	}
	var given map[string]any
	if json.Unmarshal(arguments, &given) != nil {
		return ""
	}
	for _, name := range []string{"command", "path", "file_path", "filePath", "pattern", "url", "query", "prompt"} {
		if value, ok := given[name].(string); ok && strings.TrimSpace(value) != "" {
			return piTruncate(strings.TrimSpace(value), 300)
		}
	}
	return piTruncate(strings.TrimSpace(string(arguments)), 300)
}

// piTruncate cuts text to limit characters, saying how much it left out.
func piTruncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "… (" + strconv.Itoa(len(text)-limit) + " more characters)"
}

// isPiSession says whether path is a pi session file, which pi keeps under its
// agent folder's sessions.
func isPiSession(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/.pi/agent/sessions/")
}

// piLog is the log of the pi session at path, read on to its end, with its mu
// held for the caller to unlock.
func (r *Reader) piLog(path string) (*piMessageLog, error) {
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	epoch := func() uint64 { return r.messageEpoch.Add(1) }
	r.messagesMu.Lock()
	if r.piLogs == nil {
		r.piLogs = map[string]*piMessageLog{}
	}
	log := r.piLogs[path]
	if log == nil {
		log = &piMessageLog{path: path, home: r.Home}
		log.reset(epoch())
		r.piLogs[path] = log
	}
	log.asked = now
	r.forgetPiLogs(now)
	r.messagesMu.Unlock()
	log.mu.Lock()
	if err := log.update(epoch); err != nil {
		log.mu.Unlock()
		r.messagesMu.Lock()
		if r.piLogs[path] == log {
			delete(r.piLogs, path)
		}
		r.messagesMu.Unlock()
		return nil, err
	}
	return log, nil
}

// forgetPiLogs drops the pi sessions no reader asked for in messageLogIdle. The
// caller holds r.messagesMu.
func (r *Reader) forgetPiLogs(now time.Time) {
	for path, log := range r.piLogs {
		if now.Sub(log.asked) > messageLogIdle {
			delete(r.piLogs, path)
		}
	}
}

// piMessagePicture is the picture id of the pi session at path, read back from
// the entry that holds it. It is ErrNoRecord for a picture no read found, and
// for a media type the board may not serve.
func (r *Reader) piMessagePicture(path, id string) (string, []byte, error) {
	r.messagesMu.Lock()
	log := r.piLogs[path]
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
	entry, ok := piLine(line)
	if !ok {
		return "", nil, ErrNoRecord
	}
	var said piMessage
	if json.Unmarshal(entry.Message, &said) != nil {
		return "", nil, ErrNoRecord
	}
	blocks := piBlocks(said.Content)
	if at.index >= len(blocks) {
		return "", nil, ErrNoRecord
	}
	block := blocks[at.index]
	if block.Type != "image" || !slices.Contains(pictureTypes, block.MIMEType) {
		return "", nil, ErrNoRecord
	}
	data, err := base64.StdEncoding.DecodeString(block.Data)
	if err != nil || len(data) == 0 {
		return "", nil, ErrNoRecord
	}
	return block.MIMEType, data, nil
}

// piPage is the page of the log's messages before seq before, or its newest
// page.
func (l *piMessageLog) page(before int) MessagePage {
	end := len(l.messages)
	if before >= 0 {
		end = min(end, before)
	}
	start := max(0, end-MessagesPerPage)
	page := MessagePage{Messages: make([]Message, 0, end-start), More: start > 0}
	for _, message := range l.messages[start:end] {
		page.Messages = append(page.Messages, shared(message))
	}
	if l.usage != (Usage{}) {
		usage := l.usage
		usage.ContextWindow = l.window
		page.Usage = &usage
	}
	return page
}

// piContextWindow reads the context window pi's own model catalog names for
// model, whose recorded name is provider/model, and 0 when the catalog or the
// model is not there. It reads nothing else and changes nothing.
func piContextWindow(home, model string) int64 {
	data, err := fsx.ReadFile(filepath.Join(home, ".pi", "agent", "models-store.json"))
	if err != nil {
		return 0
	}
	var catalog map[string]struct {
		Models []struct {
			ID            string `json:"id"`
			ContextWindow int64  `json:"contextWindow"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &catalog) != nil {
		return 0
	}
	id := model
	if at := strings.LastIndex(id, "/"); at >= 0 {
		id = id[at+1:]
	}
	for _, provider := range catalog {
		for _, listed := range provider.Models {
			if listed.ID == id {
				return listed.ContextWindow
			}
		}
	}
	return 0
}

// since is every message that began or changed after cursor, with where the
// reader then stands, and whether the branch was taken anew.
func (l *piMessageLog) since(cursor MessageCursor) ([]Message, MessageCursor, bool) {
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
