package fleettree

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/routing"
)

// Message is one message of a Claude Code session as the board's message
// view shows it: a prompt, a reply, a compaction or an interrupt. Seq is its
// place in the session, the offset in the conversation of the entry it began
// at, so a read of the conversation's tail and a read of the whole of it give
// a message the same, and ID stays the same for its life: Claude Code's id of
// the reply, or the id of the entry that holds the rest.
type Message struct {
	ID   string    `json:"id"`
	Seq  int       `json:"seq"`
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`
	// Source is where a prompt came from, and IsQueued says Claude Code took
	// it while it worked, into the turn already running or the next.
	Source   string `json:"source,omitempty"`
	IsQueued bool   `json:"queued,omitempty"`
	// Text is a prompt's words, a slash or ! command as it was typed, an
	// interrupt's notice or a compaction's summary.
	Text string `json:"text,omitempty"`
	// Output and Errors are what a slash or ! command printed.
	Output string `json:"output,omitempty"`
	Errors string `json:"errors,omitempty"`
	// Trigger is what started a compaction: auto or manual.
	Trigger string `json:"trigger,omitempty"`
	// Model is the model that wrote a reply, and Blocks the reply's text,
	// thinking and tool calls in the order it wrote them.
	Model    string    `json:"model,omitempty"`
	Blocks   []Block   `json:"blocks,omitempty"`
	Pictures []Picture `json:"pictures,omitempty"`
}

// Block is one block of a reply: text, thinking Claude Code recorded the
// words of, or a tool call with its outcome and what came back.
type Block struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	// CallID, Tool and Subject are a call's id, its tool and what it was
	// called on; State is CallRunning until its result is written.
	CallID  string `json:"call_id,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Subject string `json:"subject,omitempty"`
	Input   string `json:"input,omitempty"`
	State   string `json:"state,omitempty"`
	Result  string `json:"result,omitempty"`
	// Spilled is the size in bytes of a result too large for the
	// conversation, which Claude Code saved to a file of its own, and Result
	// then the start of that file.
	Spilled  int64     `json:"spilled,omitempty"`
	Pictures []Picture `json:"pictures,omitempty"`
	Edit     Edit      `json:"edit,omitzero"`
	Agent    AgentLink `json:"agent,omitzero"`
	// Plan is the checklist a call wrote, as Claude Code's TodoWrite and
	// Codex's update_plan write the whole of it each time, or the task list
	// Claude Code's TaskCreate and TaskUpdate calls built, on the latest of
	// them alone (messageLog.taskDone), and none for a call that failed, was
	// denied or was interrupted, or that Codex refused.
	Plan []PlanStep `json:"plan,omitempty"`
}

// PlanStep is one step of a plan a call wrote: its words and whether it is
// pending, in progress or completed.
type PlanStep struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

// The states of a plan's step, and the most steps one plan keeps.
const (
	PlanPending    = "pending"
	PlanInProgress = "in_progress"
	PlanCompleted  = "completed"
	planStepLimit  = 50
)

// callPlan is the plan a call of tool wrote with input, read from Claude
// Code's TodoWrite todos or Codex's update_plan steps, whose arguments Codex
// records as a JSON string and refuses when a step lacks its words or a
// state it knows, and nil for any other call or a plan it cannot read.
func callPlan(tool string, input json.RawMessage) []PlanStep {
	var steps []PlanStep
	add := func(text, status string) { steps = appendStep(steps, text, status) }
	switch tool {
	case "TodoWrite":
		var fields struct {
			Todos []struct {
				Content string `json:"content"`
				Status  string `json:"status"`
			} `json:"todos"`
		}
		if json.Unmarshal(input, &fields) != nil {
			return nil
		}
		for _, todo := range fields.Todos {
			add(todo.Content, planStatus(todo.Status))
		}
	case "update_plan":
		var arguments string
		var fields struct {
			Plan []struct {
				Step   *string `json:"step"`
				Status string  `json:"status"`
			} `json:"plan"`
		}
		if json.Unmarshal(input, &arguments) != nil || json.Unmarshal([]byte(arguments), &fields) != nil {
			return nil
		}
		for _, step := range fields.Plan {
			if step.Step == nil || !slices.Contains([]string{PlanPending, PlanInProgress, PlanCompleted}, step.Status) {
				return nil
			}
			add(*step.Step, step.Status)
		}
	}
	return steps
}

// appendStep adds a step with the first line of text to steps, unless that
// line is empty or the plan already holds as many steps as one keeps.
func appendStep(steps []PlanStep, text, status string) []PlanStep {
	if text = firstLine(text); text != "" && len(steps) < planStepLimit {
		steps = append(steps, PlanStep{Text: bounded(text, agentLineLimit), Status: status})
	}
	return steps
}

// planTask is one task of the list Claude Code's task calls build: its ID,
// which TaskCreate's result gives and TaskUpdate names, its subject and its
// state.
type planTask struct {
	ID, Subject, Status string
}

// taskCall is what a TaskCreate or TaskUpdate call asks of the task list:
// the task it names, by TaskUpdate's ID, and the subject and state it gives,
// a state of deleted taking the task off the list.
type taskCall struct {
	TaskID  string `json:"taskId"`
	Subject string `json:"subject"`
	Status  string `json:"status"`
}

// isTaskTool reports whether tool is one of Claude Code's that change its
// task list.
func isTaskTool(tool string) bool {
	return tool == "TaskCreate" || tool == "TaskUpdate"
}

// planStatus is a todo's or a task's state, pending for any Claude Code names
// that the board does not draw.
func planStatus(status string) string {
	switch status {
	case PlanInProgress, PlanCompleted:
		return status
	}
	return PlanPending
}

// Edit is what a call that edited a file changed: how many lines it added
// and removed, and its changed lines with their context, each marked +, - or
// a space as a diff marks it.
type Edit struct {
	Added   int      `json:"added"`
	Removed int      `json:"removed"`
	Diff    []string `json:"diff,omitempty"`
}

// AgentLink is the sub-agent a call started: its node in its goblin's tree,
// whose own output GET /api/tasks/<id>/agent serves, and its type.
type AgentLink struct {
	Node string `json:"node"`
	Type string `json:"type"`
}

// Picture is an image a prompt or a call's result holds, which
// Reader.MessagePicture reads by its ID.
type Picture struct {
	ID        string `json:"id"`
	MediaType string `json:"media_type"`
	Bytes     int    `json:"bytes"`
}

// The kinds of a Message and of a Block, the sources of a prompt, and the
// states a call ends in beside those of an AgentEntry.
const (
	MessagePrompt     = "prompt"
	MessageReply      = "reply"
	MessageCompaction = "compaction"
	MessageInterrupt  = "interrupt"

	BlockText     = "text"
	BlockThinking = "thinking"
	BlockCall     = "call"

	// FromOverlord is a prompt typed into the session that names no other
	// source.
	FromOverlord = "overlord"
	// FromCEO is the CEO's brief or a line the CEO sent.
	FromCEO = "ceo"
	// FromAnswer is the Overlord's answer from the Command Center, and
	// FromPage is one he gave on a session's Scrawl page.
	FromAnswer = "answer"
	FromPage   = "page"
	// FromNotice is the fleet's own word to a session rather than the
	// Overlord's: Claude Code's notice that a background task or sub-agent
	// ended or a monitor reported, and the wake line the supervisor types
	// into a Codex or pi CEO's terminal.
	FromNotice = "notice"
	FromSlash  = "slash"
	FromShell  = "shell"

	CallDenied      = "denied"
	CallInterrupted = "interrupted"
)

// MessagesPerPage is how many messages one page holds.
const MessagesPerPage = 50

const (
	// messageTextLimit bounds a prompt's, a reply's or a summary's text, and
	// resultLimit a call's result or a command's output, in characters.
	messageTextLimit = 32000
	resultLimit      = 4000
	// diffLinesKept bounds an edit's diff.
	diffLinesKept = 200
	// pictureEntryReach bounds the entry a picture is read back from.
	pictureEntryReach = 64 << 20
	// messageTailReach is how much of the end of a conversation is read
	// before its newest page is served: a CEO's conversation runs to a
	// hundred megabytes, which take seconds to read whole.
	messageTailReach = 8 << 20
)

// MessagePage is a page of a session's messages, oldest first, and whether
// older messages come before its first, with what the session's usage says
// as far as the conversation records it, and the prompts its harness holds.
type MessagePage struct {
	Conversation string          `json:"conversation"`
	Messages     []Message       `json:"messages"`
	More         bool            `json:"more"`
	Usage        *Usage          `json:"usage,omitempty"`
	Waiting      []WaitingPrompt `json:"waiting,omitempty"`
}

// WaitingPrompt keeps a Claude Code prompt visible from enqueue through its
// handoff until its delivered Message appears, or the session answers after
// the handoff with none that is it, and no longer once Up takes it back to
// the prompt line. It has no delivery offset yet,
// so it stays separate from messages; IsSending marks the handoff, and Action
// names the board message that typed it, which the supervisor fills in.
type WaitingPrompt struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	Source    string    `json:"source,omitempty"`
	Text      string    `json:"text"`
	IsSending bool      `json:"sending,omitempty"`
	Action    string    `json:"action,omitempty"`
}

// Usage is what a session has spent, read from its own conversation: its
// input, output and cached tokens, the dollars it recorded, and the context
// its last request carried against the model's window. A field a
// conversation does not record is left zero, and the board leaves that stat
// out rather than inventing it. The model and effort a session runs are no
// part of it: its one record says them (SessionEngine reads the harness's).
type Usage struct {
	InputTokens     int64   `json:"input_tokens,omitempty"`
	OutputTokens    int64   `json:"output_tokens,omitempty"`
	CacheReadTokens int64   `json:"cache_read_tokens,omitempty"`
	CostUSD         float64 `json:"cost_usd,omitempty"`
	ContextTokens   int64   `json:"context_tokens,omitempty"`
	ContextWindow   int64   `json:"context_window,omitempty"`
}

// syntheticModel is what Claude Code names as the model of an entry it writes
// itself rather than the model, such as a usage limit's notice.
const syntheticModel = "<synthetic>"

// IsHarnessReply reports whether message is a reply its harness wrote itself
// in the model's place, as Claude Code does where a usage limit or a failed
// request ends a turn, which no hook of it reports.
func (message Message) IsHarnessReply() bool {
	return message.Kind == MessageReply && message.Model == syntheticModel
}

// add folds one request's usage into the session's.
func (u *Usage) add(input, output, cacheRead int64) {
	u.InputTokens += input
	u.OutputTokens += output
	u.CacheReadTokens += cacheRead
	u.ContextTokens = input + output + cacheRead
}

// MessageCursor is where a reader of a session's messages stands: the read
// of the conversation it last had messages from and how many changes it had
// seen by then.
type MessageCursor struct {
	epoch, revision, incarnation uint64
}

var messageIdentitySeed = rand.Text()

var messageEpoch atomic.Uint64

func (cursor MessageCursor) Conversation() string {
	if cursor.epoch == 0 {
		return "empty"
	}
	return fmt.Sprintf("%s-%x", messageIdentitySeed, cursor.epoch)
}

type messageFileState struct {
	info                 os.FileInfo
	size                 int64
	prefix, tail         [32]byte
	prefixOffset         int64
	prefixSize, tailSize int64
}

type messageCheckpoint struct {
	epoch     uint64
	fileState messageFileState
	piOffset  int64
	piLeaf    string
}

func (state *messageFileState) changed(file io.ReaderAt, info os.FileInfo, allowedFrom int64) (bool, error) {
	if state.info == nil {
		return false, nil
	}
	if !os.SameFile(state.info, info) || info.Size() < state.size || (info.Size() == state.info.Size() && !info.ModTime().Equal(state.info.ModTime())) {
		return true, nil
	}
	for _, sample := range []struct {
		offset, size int64
		hash         [32]byte
	}{{state.prefixOffset, state.prefixSize, state.prefix}, {state.size - state.tailSize, state.tailSize, state.tail}} {
		if sample.size == 0 || sample.offset < allowedFrom {
			continue
		}
		data := make([]byte, sample.size)
		if _, err := file.ReadAt(data, sample.offset); err != nil {
			return false, err
		}
		if sha256.Sum256(data) != sample.hash {
			return true, nil
		}
	}
	return false, nil
}

func (state *messageFileState) capture(file io.ReaderAt, info os.FileInfo, from, size int64) error {
	prefixSize := min(size-from, int64(4096))
	tailSize := min(size-from, int64(4096))
	prefix := make([]byte, prefixSize)
	tail := make([]byte, tailSize)
	if prefixSize > 0 {
		if _, err := file.ReadAt(prefix, from); err != nil {
			return err
		}
		if _, err := file.ReadAt(tail, size-tailSize); err != nil {
			return err
		}
	}
	*state = messageFileState{info: info, size: size, prefix: sha256.Sum256(prefix), tail: sha256.Sum256(tail), prefixOffset: from, prefixSize: prefixSize, tailSize: tailSize}
	return nil
}

// messageLog is one Claude Code conversation read as messages, kept between
// reads so each read parses only the entries written since the last. asked,
// when a reader last asked for it, is guarded by the Reader's messagesMu, and
// everything else by mu, so a first read of a long conversation holds up no
// other conversation's.
type messageLog struct {
	path  string
	asked time.Time

	mu          sync.Mutex
	incarnation uint64
	fileState   messageFileState
	messageState
}

// messageState is what a read of a conversation found, which a read of the
// whole conversation puts in place of a read of its tail at once.
type messageState struct {
	read int64
	// from is where the read began: 0, or the first entry of a long
	// conversation's tail, read first so its newest page waits for no more.
	// The whole conversation is then read beside it, and whole closes once
	// that read is in place of this one, or failed for wholeErr.
	from     int64
	whole    chan struct{}
	wholeErr error
	// epoch tells this read of the conversation from any earlier one.
	epoch uint64
	// messages are the conversation's messages, and changed the revision at
	// which each last changed; revision counts every change.
	messages []Message
	changed  []uint64
	revision uint64
	// replies finds a reply by Claude Code's id of it, calls a call by its
	// id, and pictures where each picture's entry is in the file.
	replies  map[string]int
	calls    map[string]blockAt
	pictures map[string]pictureAt
	// tasks is the task list Claude Code's task calls built, in the order
	// they created its tasks, as a read from the conversation's start knows
	// it, and isTaskListLost says a call on a task the list does not hold
	// showed the list was made elsewhere, so this read cannot know it.
	// taskCalls is what each task call not yet settled asks of the list, and
	// taskCard the call whose block shows it.
	tasks          []planTask
	isTaskListLost bool
	taskCalls      map[string]taskCall
	taskCard       string
	// usage is what the conversation says the session spent, and window the
	// context window of the model it last named (claudeContextWindow).
	usage   Usage
	window  int64
	waiting []WaitingPrompt
	// handed is when Claude Code last took a waiting prompt off its queue.
	handed time.Time
}

type blockAt struct {
	message, block int
}

// pictureAt is where a picture is: the offset of its entry, the block of
// its content and, in a result, the part of that block; part is -1 for a
// picture that is a block of its own.
type pictureAt struct {
	offset      int64
	block, part int
}

// reset starts the log over as the read epoch of its conversation.
func (l *messageLog) reset(epoch uint64) {
	l.messageState = messageState{epoch: epoch, replies: map[string]int{}, calls: map[string]blockAt{}, pictures: map[string]pictureAt{}, taskCalls: map[string]taskCall{}}
}

// messageEntry is the part of a Claude Code transcript entry the message
// view reads.
type messageEntry struct {
	Type               string    `json:"type"`
	Subtype            string    `json:"subtype"`
	UUID               string    `json:"uuid"`
	Timestamp          time.Time `json:"timestamp"`
	Cwd                string    `json:"cwd"`
	IsSidechain        bool      `json:"isSidechain"`
	IsMeta             bool      `json:"isMeta"`
	IsCompactSummary   bool      `json:"isCompactSummary"`
	PromptSource       string    `json:"promptSource"`
	ToolDenialKind     string    `json:"toolDenialKind"`
	PermissionDecision struct {
		Decision string `json:"decision"`
	} `json:"permissionDecision"`
	CompactMetadata struct {
		Trigger string `json:"trigger"`
	} `json:"compactMetadata"`
	Operation  string          `json:"operation"`
	Content    json.RawMessage `json:"content"`
	Attachment struct {
		Type   string          `json:"type"`
		Prompt json.RawMessage `json:"prompt"`
	} `json:"attachment"`
	Message struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type messageBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
	Source    pictureSource   `json:"source"`
}

type pictureSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// apiRequest begins the entries Claude Code 2.1.308 keeps of each request
// it sent its model, copies of what the conversation already holds.
var apiRequest = []byte(`{"type":"api-request`)

type messageBatchLine struct {
	offset int64
	data   []byte
}

type messageBatch struct {
	reset           bool
	from, end, size int64
	state           messageFileState
	lines           []messageBatchLine
}

func readMessageBatch(ctx context.Context, path string, open func(string) (messageReadFile, error), previous messageFileState, offset, from, reach int64) (messageBatch, error) {
	forceReset := false
	for attempt := 0; attempt < 2; attempt++ {
		batch, changed, err := func() (messageBatch, bool, error) {
			file, err := open(path)
			if err != nil {
				return messageBatch{}, false, err
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil {
				return messageBatch{}, false, err
			}
			allowedFrom := int64(0)
			if reach > 0 {
				allowedFrom = max(0, info.Size()-reach)
			}
			changed, err := previous.changed(file, info, allowedFrom)
			if err != nil {
				return messageBatch{}, errors.Is(err, io.EOF), err
			}
			batch := messageBatch{reset: forceReset || changed || info.Size() < offset, from: from, size: info.Size()}
			start := offset
			if batch.reset {
				start, batch.from = 0, 0
			}
			var guard messageFileState
			if err := guard.capture(file, info, allowedFrom, info.Size()); err != nil {
				return messageBatch{}, errors.Is(err, io.EOF), err
			}
			if !batch.reset {
				changed, err = previous.changed(file, info, allowedFrom)
				if err != nil {
					return messageBatch{}, errors.Is(err, io.EOF), err
				}
				if changed {
					return messageBatch{}, true, nil
				}
			}
			if start == 0 && reach > 0 && info.Size() > reach {
				batch.from, err = entryAfter(file, info.Size()-reach, info.Size())
				if err != nil {
					return messageBatch{}, errors.Is(err, io.EOF), err
				}
				start = batch.from
			}
			// A read whose ctx ends stops at the last whole entry it read,
			// which the batch still holds once the file is checked, so its
			// caller keeps what it read before it reports the end.
			batch.end, err = readOn(ctx, file, start, info.Size(), func(at int64, lines [][]byte) (int64, error) {
				for _, line := range lines {
					if err := ctx.Err(); err != nil {
						return at, err
					}
					batch.lines = append(batch.lines, messageBatchLine{offset: at, data: bytes.Clone(line)})
					at += int64(len(line)) + 1
				}
				return at, nil
			})
			if err != nil && ctx.Err() == nil {
				return messageBatch{}, errors.Is(err, io.EOF), err
			}
			if err := batch.state.capture(file, info, min(batch.end, max(batch.from, allowedFrom)), batch.end); err != nil {
				return messageBatch{}, errors.Is(err, io.EOF), err
			}
			current, err := os.Stat(path)
			if err != nil {
				return messageBatch{}, false, err
			}
			changed, err = guard.changed(file, current, allowedFrom)
			if err != nil {
				return messageBatch{}, errors.Is(err, io.EOF), err
			}
			if changed {
				return messageBatch{}, true, nil
			}
			return batch, false, nil
		}()
		if changed {
			forceReset = true
			continue
		}
		if err != nil {
			return messageBatch{}, err
		}
		return batch, nil
	}
	return messageBatch{}, errors.New("conversation changed during read")
}

// update parses the entries written since the last read. A conversation that
// shrank, or whose file is another or was rewritten (messageFileState), was
// replaced, and is read again from its start as a read of its own.
// A first read of a conversation longer than reach, when reach is not 0,
// begins at the first entry of its last reach bytes, and the whole
// conversation is read beside it. The caller holds l.mu.
func (l *messageLog) update(ctx context.Context, epoch func() uint64, reach int64) error {
	return l.updateWith(ctx, epoch, reach, openMessageFileContext(ctx))
}

func (l *messageLog) updateWith(ctx context.Context, epoch func() uint64, reach int64, open func(string) (messageReadFile, error)) error {
	batch, err := readMessageBatch(ctx, l.path, open, l.fileState, l.read, l.from, reach)
	if err != nil {
		return err
	}
	if batch.reset {
		l.reset(epoch())
	}
	startWhole := l.read == 0 && batch.from > 0
	l.from = batch.from
	for _, line := range batch.lines {
		l.entry(line.data, line.offset)
	}
	l.read, l.fileState = batch.end, batch.state
	if startWhole {
		l.readWhole(epoch)
	}
	return ctx.Err()
}

// entryAfter is where the first entry at or after offset begins: just past
// the newline before it.
func entryAfter(file io.ReaderAt, offset, size int64) (int64, error) {
	buffer := make([]byte, 64<<10)
	for next := offset - 1; next < size; {
		count, err := file.ReadAt(buffer[:min(int64(len(buffer)), size-next)], next)
		if i := bytes.IndexByte(buffer[:count], '\n'); i >= 0 {
			return next + int64(i) + 1, nil
		}
		next += int64(count)
		if errors.Is(err, io.EOF) {
			return next, nil
		}
		if err != nil {
			return 0, err
		}
	}
	return size, nil
}

// readWhole reads the whole conversation beside the read of its tail, and
// puts what it found in place of what that read found, as a read of a new
// epoch, unless the log was read anew meanwhile. The caller holds l.mu.
func (l *messageLog) readWhole(epoch func() uint64) {
	done, tail := make(chan struct{}), l.epoch
	l.whole, l.wholeErr = done, nil
	go func() {
		defer close(done)
		whole := &messageLog{path: l.path}
		whole.reset(0)
		err := whole.update(context.Background(), epoch, 0)
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.epoch != tail {
			return
		}
		if err == nil {
			// Read on to what was written while the whole was read.
			err = whole.update(context.Background(), epoch, 0)
		}
		if err != nil {
			l.wholeErr = err
			return
		}
		file, err := fsx.Open(l.path)
		if err != nil {
			l.wholeErr = err
			return
		}
		info, err := file.Stat()
		if err == nil {
			var changed bool
			changed, err = whole.fileState.changed(file, info, 0)
			if err == nil && changed {
				err = errors.New("conversation changed during whole read")
			}
		}
		if err == nil {
			var changed bool
			changed, err = l.fileState.changed(file, info, 0)
			if err == nil && changed && whole.epoch == 0 {
				whole.epoch = epoch()
			}
		}
		file.Close()
		if err != nil {
			l.wholeErr = err
			return
		}
		if whole.epoch != 0 {
			l.messageState = whole.messageState
			l.fileState = whole.fileState
			return
		}
		l.adopt(whole.messageState, epoch)
		l.fileState = whole.fileState
	}()
}

// adopt puts what a read of the whole conversation found in place of what
// the read of its tail found. When the whole holds each message of the tail
// at its seq, a view of the tail holds nothing it does not: the read keeps
// its epoch, so a view is sent no page again, and only a message the whole
// reads otherwise, or read on to, counts as changed. Otherwise, as when the
// tail held no message, it is a read of a new epoch. The caller holds l.mu.
func (l *messageLog) adopt(whole messageState, epoch func() uint64) {
	tail := l.messageState
	l.messageState = whole
	at := 0
	if len(tail.messages) > 0 {
		at = l.end(tail.messages[0].Seq)
	}
	isHeld := len(tail.messages) > 0 && len(l.messages)-at >= len(tail.messages)
	for j := 0; isHeld && j < len(tail.messages); j++ {
		isHeld = l.messages[at+j].Seq == tail.messages[j].Seq && l.messages[at+j].ID == tail.messages[j].ID
	}
	if !isHeld {
		l.epoch = epoch()
		return
	}
	l.epoch, l.revision = tail.epoch, tail.revision+1
	for i := range l.changed {
		switch j := i - at; {
		case j < 0:
			l.changed[i] = 0
		case j < len(tail.messages) && reflect.DeepEqual(l.messages[i], tail.messages[j]):
			l.changed[i] = tail.changed[j]
		default:
			l.changed[i] = l.revision
		}
	}
}

// entry folds one transcript entry, at offset in the file, into the log. A
// sub-agent's entries, and the ones Claude Code adds for its model alone,
// show nothing.
func (l *messageLog) entry(line []byte, offset int64) {
	if bytes.HasPrefix(line, apiRequest) {
		return
	}
	var entry messageEntry
	if json.Unmarshal(line, &entry) != nil || entry.IsSidechain {
		return
	}
	// A read of a tail begins at its first prompt, interrupt or compaction:
	// an answer or a summary before them may have begun before the tail.
	if l.from > 0 && len(l.messages) == 0 && (entry.Type == "assistant" || entry.IsCompactSummary) {
		return
	}
	id := entry.UUID
	if id == "" {
		id = "@" + strconv.FormatInt(offset, 10)
	}
	switch {
	case entry.Type == "assistant":
		l.reply(entry, id, offset)
	case entry.Type == "user" && entry.IsCompactSummary:
		l.summary(entry, id, offset)
	case entry.Type == "user" && !entry.IsMeta:
		l.user(entry, id, offset)
	case entry.Type == "attachment" && entry.Attachment.Type == "queued_command":
		// A queued prompt is its text, or its blocks when a picture was
		// pasted into it, whose text alone is read, as typed.
		l.prompt(Message{ID: id, Seq: int(offset), At: entry.Timestamp, IsQueued: true}, takenPicture.ReplaceAllString(resultText(entry.Attachment.Prompt), "$1"))
	case entry.Type == "queue-operation":
		l.queueOperation(entry, id)
	case entry.Type == "system" && entry.Subtype == "compact_boundary":
		l.add(Message{ID: id, Seq: int(offset), Kind: MessageCompaction, At: entry.Timestamp, Trigger: entry.CompactMetadata.Trigger})
	case entry.Type == "system" && (entry.Subtype == "local_command" || entry.Subtype == "bridge_status"):
		l.localCommand(entry, id, offset)
	}
}

// localCommand reads what Claude Code records of a slash command it ran
// itself rather than handing it to its model. Some versions record no user
// message for such a command: a system local_command record holds the
// command and another what it printed (2.1.309), and /remote-control leaves
// only a bridge_status record that opens with the command, as
// "/remote-control is active · Continue here, …" (2.1.304 to 2.1.319). Either
// is the command's row, so a command sent from the board takes its place in
// the conversation rather than waiting under it (#2193).
func (l *messageLog) localCommand(entry messageEntry, id string, offset int64) {
	var text string
	if json.Unmarshal(entry.Content, &text) != nil || l.commandOutput(text) {
		return
	}
	if entry.Subtype == "local_command" {
		if command, ok := TypedCommand(text); ok {
			l.add(Message{ID: id, Seq: int(offset), Kind: MessagePrompt, At: entry.Timestamp, Source: FromSlash, Text: command})
		}
		return
	}
	command, ok := StatusCommand(text)
	if !ok {
		return
	}
	status := bounded(strings.TrimSpace(text), resultLimit)
	// A status that follows its own command's row is what it printed.
	if last := len(l.messages) - 1; last >= 0 && l.messages[last].Kind == MessagePrompt && l.messages[last].Source == FromSlash && l.messages[last].Text == command {
		l.messages[last].Output = status
		l.touch(last)
		return
	}
	l.add(Message{ID: id, Seq: int(offset), Kind: MessagePrompt, At: entry.Timestamp, Source: FromSlash, Text: command, Output: status})
}

// leadingCommand is the slash command a line opens with.
var leadingCommand = regexp.MustCompile(`^/[A-Za-z][\w:-]*`)

// TypedCommand is the slash command text records as typed, as its tags or
// as the command itself, and whether text is one.
func TypedCommand(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if source, words := promptSource(text); source == FromSlash {
		return bounded(words, messageTextLimit), true
	}
	if leadingCommand.MatchString(text) {
		return bounded(strings.Join(strings.Fields(text), " "), messageTextLimit), true
	}
	return "", false
}

// ShellCommand is the command a shell-mode prompt's text ran, the words typed
// after its !, and whether text is one.
func ShellCommand(text string) (string, bool) {
	match := bashInput.FindStringSubmatch(strings.TrimSpace(text))
	if match == nil {
		return "", false
	}
	return strings.TrimSpace(match[1]), true
}

// StatusCommand is the slash command a bridge_status record's text opens
// with, and whether it opens with one.
func StatusCommand(text string) (string, bool) {
	command := leadingCommand.FindString(strings.TrimSpace(text))
	return command, command != ""
}

func (l *messageLog) queueOperation(entry messageEntry, id string) {
	var content string
	// A dequeue has no content, and an enqueue of a picture none that is text.
	_ = json.Unmarshal(entry.Content, &content)
	source, text := promptOf(content)
	at := -1
	switch entry.Operation {
	case "enqueue":
		l.waiting = append(l.waiting, WaitingPrompt{ID: id, At: entry.Timestamp, Source: source, Text: text})
	case "dequeue":
		at = slices.IndexFunc(l.waiting, func(prompt WaitingPrompt) bool { return !prompt.IsSending })
	case "remove", "popAll":
		at = slices.IndexFunc(l.waiting, func(prompt WaitingPrompt) bool {
			return !prompt.IsSending && prompt.Source == source && prompt.Text == text
		})
	}
	if at >= 0 {
		// A line Up took back is on the prompt line, his to change, and in
		// no queue: it waits for nothing until it is submitted again (#2332).
		if entry.Operation == "popAll" || l.waiting[at].Source == FromNotice || l.waiting[at].Text == "" {
			l.waiting = slices.Delete(l.waiting, at, at+1)
		} else {
			l.waiting[at].IsSending = true
			l.handed = entry.Timestamp
		}
	}
}

// stopWaiting ends the wait a delivered prompt of source and text answers. A
// shell command answers a wait for his words too: Claude Code queues a !
// command typed during a turn by its words alone and runs it at the turn's
// end as a shell command (#2242).
func (l *messageLog) stopWaiting(source, text string) {
	isFor := func(prompt WaitingPrompt) bool {
		return prompt.Text == text && (prompt.Source == source || source == FromShell && prompt.Source == FromOverlord)
	}
	at := slices.IndexFunc(l.waiting, func(prompt WaitingPrompt) bool { return prompt.IsSending && isFor(prompt) })
	if at < 0 {
		at = slices.IndexFunc(l.waiting, isFor)
	}
	if at >= 0 {
		l.waiting = slices.Delete(l.waiting, at, at+1)
	}
}

// add appends message to the log and returns where it is.
func (l *messageLog) add(message Message) int {
	l.revision++
	l.messages = append(l.messages, message)
	l.changed = append(l.changed, l.revision)
	return len(l.messages) - 1
}

// touch marks the message at index changed.
func (l *messageLog) touch(index int) {
	l.revision++
	l.changed[index] = l.revision
}

// reply folds one block of a reply into it: Claude Code writes each block of
// an answer as an entry of its own under the answer's id, a tool's result
// sometimes between them. Thinking it kept only the signature of shows
// nothing. A reply's seq is the offset of its first entry that shows.
//
// An answer written after the last handoff is the session going on from it,
// so a prompt still sending by then has no delivered row to wait for: the
// record holds it in words the log does not read as it, as Claude Code's own
// prompt after a usage limit, which it records for its model alone, or not at
// all. Its wait ends here, so nothing reads Sending for good (#2327). An
// answer stamped before the handoff and recorded after it, as a notice Claude
// Code writes itself can be, ends none.
func (l *messageLog) reply(entry messageEntry, id string, offset int64) {
	if entry.Timestamp.After(l.handed) {
		l.waiting = slices.DeleteFunc(l.waiting, func(prompt WaitingPrompt) bool { return prompt.IsSending })
	}
	var blocks []messageBlock
	if json.Unmarshal(entry.Message.Content, &blocks) != nil {
		return
	}
	var added []Block
	for _, block := range blocks {
		switch {
		case block.Type == "text" && strings.TrimSpace(block.Text) != "":
			added = append(added, Block{Kind: BlockText, Text: bounded(block.Text, messageTextLimit)})
		case block.Type == "thinking" && strings.TrimSpace(block.Thinking) != "":
			added = append(added, Block{Kind: BlockThinking, Text: bounded(block.Thinking, messageTextLimit)})
		case block.Type == "tool_use":
			call := Block{Kind: BlockCall, CallID: block.ID, Tool: block.Name, Subject: bounded(callSubject(block.Input, entry.Cwd), agentLineLimit), State: CallRunning, Plan: callPlan(block.Name, block.Input)}
			if block.Name == "Agent" || block.Name == "Task" {
				var input struct {
					SubagentType string `json:"subagent_type"`
				}
				_ = json.Unmarshal(block.Input, &input)
				call.Agent = AgentLink{Node: "subagent:" + block.ID, Type: cmp.Or(input.SubagentType, "general-purpose")}
			}
			if isTaskTool(block.Name) {
				var asked taskCall
				_ = json.Unmarshal(block.Input, &asked)
				l.taskCalls[block.ID] = asked
				subject := asked.Subject
				if i := l.task(asked.TaskID); subject == "" && i >= 0 {
					subject = l.tasks[i].Subject
				}
				if subject = firstLine(subject); subject != "" {
					call.Subject = bounded(subject, agentLineLimit)
				}
			}
			added = append(added, call)
		}
	}
	if len(added) == 0 {
		return
	}
	if entry.Message.ID != "" {
		id = entry.Message.ID
	}
	index, isKnown := l.replies[id]
	if !isKnown {
		index = l.add(Message{ID: id, Seq: int(offset), Kind: MessageReply, At: entry.Timestamp, Model: entry.Message.Model})
		l.replies[id] = index
		// Claude Code writes one entry for each block of an answer, and
		// repeats the answer's usage on them: only the first counts it. A
		// notice Claude Code writes itself, such as a usage limit's, names
		// no model ("<synthetic>") and used nothing, so it changes neither
		// the model's window nor how full its context is.
		if entry.Message.Model != syntheticModel {
			usage := entry.Message.Usage
			l.usage.add(usage.InputTokens+usage.CacheCreationInputTokens, usage.OutputTokens, usage.CacheReadInputTokens)
			if entry.Message.Model != "" {
				l.window = claudeContextWindow(entry.Message.Model)
			}
		}
	}
	reply := &l.messages[index]
	for _, block := range added {
		if block.Kind == BlockCall {
			l.calls[block.CallID] = blockAt{index, len(reply.Blocks)}
		}
		reply.Blocks = append(reply.Blocks, block)
	}
	l.touch(index)
}

// user folds one of the user's entries into the log: the results of the
// calls it answers, an interrupt, a command's output, or a prompt with any
// pictures pasted into it.
func (l *messageLog) user(entry messageEntry, id string, offset int64) {
	var text string
	if json.Unmarshal(entry.Message.Content, &text) == nil {
		switch notice, isInterrupt := interrupt(text); {
		case isInterrupt:
			l.add(Message{ID: id, Seq: int(offset), Kind: MessageInterrupt, At: entry.Timestamp, Text: notice})
		case !l.commandOutput(text):
			l.prompt(Message{ID: id, Seq: int(offset), At: entry.Timestamp, IsQueued: entry.PromptSource == "queued"}, text)
		}
		return
	}
	var blocks []messageBlock
	if json.Unmarshal(entry.Message.Content, &blocks) != nil {
		return
	}
	var texts []string
	var pictures []Picture
	for i, block := range blocks {
		switch block.Type {
		case "tool_result":
			l.result(entry, block, offset, i)
		case "text":
			if notice, isInterrupt := interrupt(block.Text); isInterrupt {
				l.add(Message{ID: id, Seq: int(offset), Kind: MessageInterrupt, At: entry.Timestamp, Text: notice})
				return
			}
			texts = append(texts, block.Text)
		case "image":
			if picture, ok := l.picture(block.Source, pictureAt{offset: offset, block: i, part: -1}); ok {
				pictures = append(pictures, picture)
			}
		}
	}
	if len(texts) > 0 || len(pictures) > 0 {
		l.prompt(Message{ID: id, Seq: int(offset), At: entry.Timestamp, IsQueued: entry.PromptSource == "queued", Pictures: pictures}, strings.Join(texts, "\n"))
	}
}

// interrupt is the notice Claude Code records where he stopped it, as
// [Request interrupted by user] or [Request interrupted by user for tool
// use], without its brackets, and whether text is one.
func interrupt(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "[Request interrupted by user") || !strings.HasSuffix(text, "]") {
		return "", false
	}
	return text[1 : len(text)-1], true
}

var (
	systemReminder = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	// pastedContent marks where Claude Code took typed text as a paste, as
	// the monitor reads it (internal/monitor/took.go).
	pastedContent = regexp.MustCompile(`</?pasted_content id="[^"]*">`)
	// takenPicture is a picture's path and the mark Claude Code 2.1.323 put
	// right after it as it took the picture into a running turn with the
	// line that names it (#2541).
	takenPicture  = regexp.MustCompile(`(?i)(\.(?:png|jpe?g|gif|webp))\[Image #\d+\]`)
	commandName   = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	commandArgs   = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	bashInput     = regexp.MustCompile(`(?s)^<bash-input>(.*?)</bash-input>`)
	commandStdout = regexp.MustCompile(`(?s)<(local-command|bash)-(stdout|stderr)>(.*?)</(?:local-command|bash)-(?:stdout|stderr)>`)
	// boardAnswer begins the words the board types for the Overlord's answer
	// from the Command Center (internal/supervisor/questions.go and the
	// items beside it), to a goblin after the CEO's stamp.
	boardAnswer = regexp.MustCompile(`^(?:The Overlord answered |The Overlord changed the answer |User answer to CEO question |\d+ user answers to CEO questions)`)
	// These are the complete shapes emitted by gate_run_end.go and notify.go,
	// after fleet.Stamp, rather than arbitrary sentences beginning with CEO.
	runEndedNotice = regexp.MustCompile(`^Your run .+ ended with exit -?\d+ after [^;]+; read its output and carry on from it\.$`)
	helperNotice   = regexp.MustCompile(`(?s)^Your helper [A-Za-z0-9_-]+ (?:is done: its work is committed on its branch, .+\. Merge it into your branch with: goblins helper merge [A-Za-z0-9_-]+|asks: .+ Answer it with: goblins send [A-Za-z0-9_-]+ "<answer>"|failed: .+\. Tell it what to do with: goblins send [A-Za-z0-9_-]+ "<what>", or stop it with: goblins kill [A-Za-z0-9_-]+ --reason "<why>")$`)
	wakeDigest     = regexp.MustCompile(`^WAKE QUEUE: (?:\d+ pending\n|empty$)`)
)

// ceoBrief ends the line a goblin starts on, which names its brief
// (internal/spawn/native_launch.go).
const ceoBrief = ": it is your instruction from the CEO."

func (l *messageLog) prompt(message Message, text string) {
	message.Kind = MessagePrompt
	message.Source, message.Text = promptOf(text)
	if message.Text != "" || len(message.Pictures) > 0 {
		l.add(message)
		l.stopWaiting(message.Source, message.Text)
	}
}

// promptOf is where a prompt of text came from and the words it shows.
func promptOf(text string) (string, string) {
	source, words := promptSource(strings.TrimSpace(pastedContent.ReplaceAllString(systemReminder.ReplaceAllString(text, ""), "")))
	return source, bounded(words, messageTextLimit)
}

// WakePrefix begins the wake line the supervisor types into a Codex or pi
// CEO's native terminal (internal/supervisor/ceo_wake.go's wakeLine builds its
// line from here), so the board reads that line as the fleet's own and the two
// can never drift apart.
const WakePrefix = "ceo watcher wake: "

// promptSource is where a prompt of text came from and the words it shows:
// a task notice's summary, the fleet's own typed wake line, a command as it
// was typed, the CEO's words without the stamp the CEO's lines carry, and his
// own words where the board typed them into a goblin behind both stamps.
func promptSource(text string) (string, string) {
	if strings.HasPrefix(text, "<task-notification>") {
		return FromNotice, notice(text)
	}
	// The supervisor's wake line is the fleet speaking, not the Overlord: he
	// never typed it, and it is never copied as his words. Only this exact
	// opening is the fleet's, so anything else he types, "ceo" included,
	// stays his own message.
	isAFKDrain := strings.HasPrefix(text, "AFK MODE IS ON: ") && strings.Contains(text, "\nIt is complete autopilot: no Command Center prompt opens for him while it is on, and nothing is asked of him or held for him.\n") && strings.Contains(text, "\nWAKE QUEUE: ")
	if strings.HasPrefix(text, WakePrefix) || strings.HasPrefix(text, "== SESSION LOCK ==\n") || wakeDigest.MatchString(text) || isAFKDrain {
		return FromNotice, text
	}
	// A slash command is its tags, the name first, or its message first for
	// a command that hands its model a prompt.
	if match := commandName.FindStringSubmatch(text); strings.HasPrefix(text, "<command-") && match != nil {
		var args string
		if match := commandArgs.FindStringSubmatch(text); match != nil {
			args = match[1]
		}
		return FromSlash, strings.TrimSpace(match[1] + " " + args)
	}
	if command, isShell := ShellCommand(text); isShell {
		return FromShell, command
	}
	words, isStamped := strings.CutPrefix(text, routing.SteerPrefix)
	if isStamped {
		// A message he typed to a goblin on the board is his, so the board
		// can show it as his and read it as delivered.
		if his, isHis := strings.CutPrefix(words, routing.BoardMessagePrefix); isHis {
			return FromOverlord, his
		}
		if runEndedNotice.MatchString(words) || helperNotice.MatchString(words) {
			return FromNotice, words
		}
		lower := strings.ToLower(words)
		if strings.Contains(lower, "answered on your review page") || strings.Contains(lower, "answered on the page") || strings.Contains(lower, "answered on its page") {
			return FromPage, words
		}
	}
	switch {
	case boardAnswer.MatchString(words):
		lower := strings.ToLower(words)
		if strings.Contains(lower, "on its page") || strings.Contains(lower, "on the page") || strings.Contains(lower, "on your review page") {
			return FromPage, words
		}
		return FromAnswer, words
	case isStamped, strings.HasPrefix(text, "Read ") && strings.HasSuffix(text, ceoBrief):
		return FromCEO, words
	}
	return FromOverlord, text
}

// notice is what a task-notification says: its summary, and a monitor's
// event under it.
func notice(text string) string {
	tags := map[string]string{}
	for _, match := range notificationTag.FindAllStringSubmatch(text, -1) {
		if _, seen := tags[match[1]]; !seen {
			tags[match[1]] = strings.TrimSpace(match[2])
		}
	}
	return strings.TrimSpace(tags["summary"] + "\n" + tags["event"])
}

// commandOutput folds what a slash or ! command printed into the command,
// the last message, and reports whether text was such output.
func (l *messageLog) commandOutput(text string) bool {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "<local-command-std") && !strings.HasPrefix(text, "<bash-std") {
		return false
	}
	matches := commandStdout.FindAllStringSubmatch(text, -1)
	if len(l.messages) == 0 {
		return true
	}
	command := &l.messages[len(l.messages)-1]
	if command.Kind != MessagePrompt || command.Source != FromSlash && command.Source != FromShell {
		return true
	}
	for _, match := range matches {
		printed := bounded(strings.TrimSpace(ansi.ReplaceAllString(match[3], "")), resultLimit)
		if match[2] == "stdout" {
			command.Output = printed
		} else {
			command.Errors = printed
		}
	}
	l.touch(len(l.messages) - 1)
	return true
}

// summary gives the compaction before it the summary Claude Code goes on
// from.
func (l *messageLog) summary(entry messageEntry, id string, offset int64) {
	var text string
	if json.Unmarshal(entry.Message.Content, &text) != nil {
		return
	}
	text = bounded(strings.TrimSpace(text), messageTextLimit)
	if last := len(l.messages) - 1; last >= 0 && l.messages[last].Kind == MessageCompaction && l.messages[last].Text == "" {
		l.messages[last].Text = text
		l.touch(last)
		return
	}
	l.add(Message{ID: id, Seq: int(offset), Kind: MessageCompaction, At: entry.Timestamp, Text: text})
}

// toolOutcome is the part of what Claude Code recorded of a call's result
// the message view reads.
type toolOutcome struct {
	Interrupted         bool            `json:"interrupted"`
	PersistedOutputPath string          `json:"persistedOutputPath"`
	PersistedOutputSize int64           `json:"persistedOutputSize"`
	Type                string          `json:"type"`
	Content             json.RawMessage `json:"content"`
	StructuredPatch     []struct {
		Lines []string `json:"lines"`
	} `json:"structuredPatch"`
	// Task is the task a TaskCreate made, and Success false a TaskUpdate
	// Claude Code refused without an error, as of a task it does not hold.
	Task struct {
		ID string `json:"id"`
	} `json:"task"`
	Success *bool `json:"success"`
}

// persisted is a result too large for the conversation as Claude Code writes
// it there: its size, the file it saved it to, and the start of it.
var persisted = regexp.MustCompile(`(?s)^<persisted-output>\s*Output too large \(([\d.]+)(B|KB|MB)\)\. Full output saved to: ([^\r\n]+)\s*(?:Preview \([^)\n]*\):\n)?(.*?)\s*</persisted-output>$`)

// sizeUnits are the units persisted names a size in.
var sizeUnits = map[string]float64{"B": 1, "KB": 1 << 10, "MB": 1 << 20}

// result settles a call with what came back: block, the part'th of an entry
// at offset.
func (l *messageLog) result(entry messageEntry, block messageBlock, offset int64, part int) {
	at, isCalled := l.calls[block.ToolUseID]
	if !isCalled {
		return
	}
	var outcome toolOutcome
	_ = json.Unmarshal(entry.ToolUseResult, &outcome)
	call := l.messages[at.message].Blocks[at.block]
	switch {
	case outcome.Interrupted || entry.ToolDenialKind == "interrupted":
		call.State = CallInterrupted
	case entry.PermissionDecision.Decision == "reject" || entry.ToolDenialKind != "":
		call.State = CallDenied
	case block.IsError, isTaskTool(call.Tool) && outcome.Success != nil && !*outcome.Success:
		call.State = CallFailed
	default:
		call.State = CallDone
	}
	text := resultText(block.Content)
	call.Result = bounded(strings.TrimSpace(toolError.ReplaceAllString(text, "")), resultLimit)
	if spill := persisted.FindStringSubmatch(strings.TrimSpace(text)); spill != nil {
		call.Result, call.Spilled = l.spilled(spill, outcome)
	}
	var parts []messageBlock
	_ = json.Unmarshal(block.Content, &parts)
	var pictures []Picture
	for i, content := range parts {
		if content.Type == "image" {
			if picture, ok := l.picture(content.Source, pictureAt{offset: offset, block: part, part: i}); ok {
				pictures = append(pictures, picture)
			}
		}
	}
	call.Pictures = pictures
	if call.State == CallDone {
		call.Edit = edited(outcome)
	} else {
		call.Plan = nil
	}
	l.messages[at.message].Blocks[at.block] = call
	if asked, isTaskCall := l.taskCalls[call.CallID]; isTaskCall {
		delete(l.taskCalls, call.CallID)
		if call.State == CallDone {
			l.taskDone(at, call, asked, outcome.Task.ID)
		}
	}
	l.touch(at.message)
}

// taskDone puts into the task list the change a task call that ran, call at
// at, made as asked, a TaskCreate making the task created, and moves the
// list's card to that call: the conversation shows the list once, at its
// latest task call, the earlier ones staying rows. A read that cannot know
// the list, one of a tail or one that lost it, shows it nowhere.
func (l *messageLog) taskDone(at blockAt, call Block, asked taskCall, created string) {
	if call.Tool == "TaskCreate" {
		l.tasks = append(l.tasks, planTask{ID: created, Subject: asked.Subject, Status: PlanPending})
		l.isTaskListLost = l.isTaskListLost || created == ""
	} else if i := l.task(asked.TaskID); i < 0 {
		l.isTaskListLost = true
	} else if asked.Status == "deleted" {
		l.tasks = slices.Delete(l.tasks, i, i+1)
	} else {
		l.tasks[i].Subject = cmp.Or(asked.Subject, l.tasks[i].Subject)
		l.tasks[i].Status = cmp.Or(asked.Status, l.tasks[i].Status)
	}
	if card, isShown := l.calls[l.taskCard]; isShown && l.taskCard != "" {
		l.messages[card.message].Blocks[card.block].Plan = nil
		l.touch(card.message)
	}
	l.taskCard = ""
	if l.from > 0 || l.isTaskListLost {
		return
	}
	var steps []PlanStep
	for _, task := range l.tasks {
		steps = appendStep(steps, task.Subject, planStatus(task.Status))
	}
	l.messages[at.message].Blocks[at.block].Plan = steps
	l.taskCard = call.CallID
}

// task is where the task list holds the task id, and -1 when it holds none.
func (l *messageLog) task(id string) int {
	return slices.IndexFunc(l.tasks, func(task planTask) bool { return task.ID == id })
}

// spilled is the start of a result Claude Code saved to a file of its own,
// as spill, a match of persisted, names it, and the result's size: read from
// the file when it is a regular file directly in this conversation's own
// tool-results folder, where Claude Code keeps them, reached through no link
// or junction, else the preview the conversation holds.
func (l *messageLog) spilled(spill []string, outcome toolOutcome) (string, int64) {
	preview, size := bounded(spill[4], resultLimit), outcome.PersistedOutputSize
	if size == 0 {
		value, _ := strconv.ParseFloat(spill[1], 64)
		size = int64(value * sizeUnits[spill[2]])
	}
	path := filepath.Clean(cmp.Or(outcome.PersistedOutputPath, strings.TrimSpace(spill[3])))
	session := strings.TrimSuffix(l.path, filepath.Ext(l.path))
	if !strings.EqualFold(filepath.Dir(path), filepath.Join(session, "tool-results")) {
		return preview, size
	}
	root, err := os.OpenRoot(session)
	if err != nil {
		return preview, size
	}
	defer root.Close()
	// root refuses a link or junction that leads out of the conversation's
	// folder, and Lstat reports a link to a file as no regular file.
	name := filepath.Join("tool-results", filepath.Base(path))
	if info, err := root.Lstat(name); err != nil || !info.Mode().IsRegular() {
		return preview, size
	}
	file, err := root.Open(name)
	if err != nil {
		return preview, size
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return preview, size
	}
	// A character is at most four bytes.
	data := make([]byte, min(info.Size(), 4*resultLimit))
	count, err := file.ReadAt(data, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return preview, size
	}
	return bounded(strings.TrimSpace(strings.ToValidUTF8(string(data[:count]), "")), resultLimit), info.Size()
}

// edited is what an edit's recorded result says it changed: its patch, or
// for a file it wrote anew every line of it.
func edited(outcome toolOutcome) Edit {
	var lines []string
	for _, hunk := range outcome.StructuredPatch {
		lines = append(lines, hunk.Lines...)
	}
	var content string
	if outcome.Type == "create" && len(lines) == 0 && json.Unmarshal(outcome.Content, &content) == nil && content != "" {
		for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
			lines = append(lines, "+"+line)
		}
	}
	var edit Edit
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+"):
			edit.Added++
		case strings.HasPrefix(line, "-"):
			edit.Removed++
		}
	}
	if len(lines) > 0 {
		edit.Diff = make([]string, 0, min(len(lines), diffLinesKept))
		for _, line := range lines[:min(len(lines), diffLinesKept)] {
			edit.Diff = append(edit.Diff, bounded(line, agentLineLimit))
		}
	}
	return edit
}

// pictureTypes are the images a picture is served as.
var pictureTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// picture keeps where an image of source is, as a picture of the log named
// by where it is, so a read of the conversation's tail and a read of the
// whole of it name it the same.
func (l *messageLog) picture(source pictureSource, at pictureAt) (Picture, bool) {
	if source.Type != "base64" || !slices.Contains(pictureTypes, source.MediaType) {
		return Picture{}, false
	}
	id := strconv.FormatInt(at.offset, 10) + "." + strconv.Itoa(at.block)
	if at.part >= 0 {
		id += "." + strconv.Itoa(at.part)
	}
	l.pictures[id] = at
	return Picture{ID: id, MediaType: source.MediaType, Bytes: len(source.Data)/4*3 - (len(source.Data) - len(strings.TrimRight(source.Data, "=")))}, true
}

// page is the messages before seq before, the newest page of them; a
// negative before reads the newest. What the session spent is left out
// while only the conversation's tail is read.
func (l *messageLog) page(before int) MessagePage {
	end := l.end(before)
	start := max(0, end-MessagesPerPage)
	page := MessagePage{Messages: make([]Message, 0, end-start), More: start > 0 || l.from > 0, Waiting: l.waitingPrompts()}
	for _, message := range l.messages[start:end] {
		page.Messages = append(page.Messages, shared(message))
	}
	if l.usage != (Usage{}) && l.from == 0 {
		usage := l.usage
		usage.ContextWindow = l.window
		page.Usage = &usage
	}
	return page
}

func (l *messageLog) waitingPrompts() []WaitingPrompt {
	waiting := make([]WaitingPrompt, 0, len(l.waiting))
	for _, prompt := range l.waiting {
		if prompt.Source != FromNotice && prompt.Text != "" {
			waiting = append(waiting, prompt)
		}
	}
	return waiting
}

// end is where the messages before seq before end, or all of them for a
// negative before.
func (l *messageLog) end(before int) int {
	if before < 0 {
		return len(l.messages)
	}
	end, _ := slices.BinarySearchFunc(l.messages, before, func(message Message, seq int) int { return cmp.Compare(message.Seq, seq) })
	return end
}

// shared is message as a reader may keep it while the log changes: its
// blocks are its own, and a block's pictures and diff are never changed
// once set.
func shared(message Message) Message {
	message.Blocks = slices.Clone(message.Blocks)
	return message
}

// messageLogIdle is how long a conversation no reader asked for is kept: an
// open view asks twice a second, and a view's stream is renewed within
// seconds of its tenth minute, so every conversation a view shows is kept,
// and only those.
const messageLogIdle = 5 * time.Minute

func lockMessages(ctx context.Context, mutex *sync.Mutex) error {
	if ctx.Done() == nil {
		mutex.Lock()
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if mutex.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// messageLog is the log of the conversation at path, read on to its end, with
// its mu held for the caller to unlock.
func (r *Reader) messageLog(ctx context.Context, path string, reach int64) (*messageLog, error) {
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	epoch := func() uint64 { return messageEpoch.Add(1) }
	if err := lockMessages(ctx, &r.messagesMu); err != nil {
		return nil, err
	}
	if r.messageLogs == nil {
		r.messageLogs = map[string]*messageLog{}
	}
	log := r.messageLogs[path]
	if log == nil {
		log = &messageLog{path: path, incarnation: messageEpoch.Add(1)}
		checkpoint, ok := r.messageCheckpoints[path]
		if ok {
			log.reset(checkpoint.epoch)
			log.fileState = checkpoint.fileState
			delete(r.messageCheckpoints, path)
		} else {
			log.reset(epoch())
		}
		r.messageLogs[path] = log
	}
	log.asked = now
	r.forgetIdleMessages(now)
	r.messagesMu.Unlock()
	if err := lockMessages(ctx, &log.mu); err != nil {
		return nil, err
	}
	if err := log.update(ctx, epoch, reach); err != nil {
		log.mu.Unlock()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if lockErr := lockMessages(ctx, &r.messagesMu); lockErr != nil {
			return nil, lockErr
		}
		if r.messageLogs[path] == log {
			delete(r.messageLogs, path)
		}
		r.messagesMu.Unlock()
		return nil, err
	}
	return log, nil
}

// forgetIdleMessages drops the conversations no reader asked for in
// messageLogIdle. The caller holds r.messagesMu.
func (r *Reader) forgetIdleMessages(now time.Time) {
	for path, log := range r.messageLogs {
		if now.Sub(log.asked) > messageLogIdle {
			log.mu.Lock()
			if r.messageCheckpoints == nil {
				r.messageCheckpoints = map[string]messageCheckpoint{}
			}
			r.messageCheckpoints[path] = messageCheckpoint{epoch: log.epoch, fileState: log.fileState}
			log.mu.Unlock()
			delete(r.messageLogs, path)
		}
	}
}

// MessagePage reads the conversation at path on from where the last read
// stopped and returns the page of its messages before seq before, or its
// newest page for a negative before, with where the reader then stands: a
// Claude Code conversation, a pi session or a Codex rollout, each read by its
// own harness's entries.
func (r *Reader) MessagePage(path string, before int) (MessagePage, MessageCursor, error) {
	return r.messagePage(context.Background(), path, before, messageTailReach)
}

func (r *Reader) MessagePageContext(ctx context.Context, path string, before int) (MessagePage, MessageCursor, error) {
	return r.messagePage(ctx, path, before, 0)
}

func (r *Reader) messagePage(ctx context.Context, path string, before int, reach int64) (MessagePage, MessageCursor, error) {
	if isPiSession(path) {
		log, err := r.piLog(ctx, path)
		if err != nil {
			return MessagePage{}, MessageCursor{}, err
		}
		defer log.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return MessagePage{}, MessageCursor{}, err
		}
		cursor := MessageCursor{log.epoch, log.revision, log.incarnation}
		page := log.page(before)
		page.Conversation = cursor.Conversation()
		return page, cursor, nil
	}
	if isCodexRollout(path) {
		log, err := r.codexLog(ctx, path)
		if err != nil {
			return MessagePage{}, MessageCursor{}, err
		}
		defer log.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return MessagePage{}, MessageCursor{}, err
		}
		cursor := MessageCursor{log.epoch, log.revision, log.incarnation}
		page := log.page(before)
		page.Conversation = cursor.Conversation()
		return page, cursor, nil
	}
	log, err := r.messageLog(ctx, path, reach)
	if err != nil {
		return MessagePage{}, MessageCursor{}, err
	}
	// A page that reaches the first message of a read of the conversation's
	// tail waits for the whole of it, which a failed read of the whole
	// starts once more.
	isRetried := false
	for log.from > 0 && (reach == 0 || before >= 0 && log.end(before) <= MessagesPerPage) {
		if log.wholeErr != nil {
			if isRetried {
				err := log.wholeErr
				log.mu.Unlock()
				return MessagePage{}, MessageCursor{}, err
			}
			log.readWhole(func() uint64 { return messageEpoch.Add(1) })
			isRetried = true
		}
		whole := log.whole
		log.mu.Unlock()
		select {
		case <-ctx.Done():
			return MessagePage{}, MessageCursor{}, ctx.Err()
		case <-whole:
		}
		if err := lockMessages(ctx, &log.mu); err != nil {
			return MessagePage{}, MessageCursor{}, err
		}
	}
	defer log.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return MessagePage{}, MessageCursor{}, err
	}
	cursor := MessageCursor{log.epoch, log.revision, log.incarnation}
	page := log.page(before)
	page.Conversation = cursor.Conversation()
	return page, cursor, nil
}

func (r *Reader) UsageAt(path string, cursor MessageCursor) *Usage {
	r.messagesMu.Lock()
	pi, codex, claude := r.piLogs[path], r.codexLogs[path], r.messageLogs[path]
	r.messagesMu.Unlock()
	if pi != nil {
		pi.mu.Lock()
		defer pi.mu.Unlock()
		if pi.epoch == cursor.epoch && pi.incarnation == cursor.incarnation {
			return usageOf(pi.usage, pi.window)
		}
	} else if codex != nil {
		codex.mu.Lock()
		defer codex.mu.Unlock()
		if codex.epoch == cursor.epoch && codex.incarnation == cursor.incarnation {
			return usageOf(codex.usage, codex.usage.ContextWindow)
		}
	} else if claude != nil {
		claude.mu.Lock()
		defer claude.mu.Unlock()
		if claude.epoch == cursor.epoch && claude.incarnation == cursor.incarnation && claude.from == 0 {
			return usageOf(claude.usage, claude.window)
		}
	}
	return nil
}

// MessagesSince reads the conversation at path on and returns every message
// that began or changed after cursor, with the waiting prompts and cursor
// from the same locked snapshot, so concurrent streams cannot separate a
// delivered row from the end of its wait.
// isAnew says the conversation was read anew since cursor, as one replaced
// is, so nothing a page before it held still stands: the reader takes a page
// again.
func (r *Reader) MessagesSince(path string, cursor MessageCursor) (messages []Message, waiting []WaitingPrompt, now MessageCursor, isAnew bool, err error) {
	if isPiSession(path) {
		log, err := r.piLog(context.Background(), path)
		if err != nil {
			return nil, nil, MessageCursor{}, false, err
		}
		defer log.mu.Unlock()
		messages, now, isAnew = log.since(cursor)
		return messages, nil, now, isAnew, nil
	}
	if isCodexRollout(path) {
		log, err := r.codexLog(context.Background(), path)
		if err != nil {
			return nil, nil, MessageCursor{}, false, err
		}
		defer log.mu.Unlock()
		messages, now, isAnew = log.since(cursor)
		return messages, nil, now, isAnew, nil
	}
	log, err := r.messageLog(context.Background(), path, messageTailReach)
	if err != nil {
		return nil, nil, MessageCursor{}, false, err
	}
	defer log.mu.Unlock()
	now = MessageCursor{log.epoch, log.revision, log.incarnation}
	if cursor.epoch != log.epoch || cursor.incarnation != log.incarnation {
		return nil, nil, now, true, nil
	}
	for i, changed := range log.changed {
		if changed > cursor.revision {
			messages = append(messages, shared(log.messages[i]))
		}
	}
	return messages, log.waitingPrompts(), now, false, nil
}

// usageOf is what a log holds as a page's usage, nil while it records none.
func usageOf(held Usage, window int64) *Usage {
	if held == (Usage{}) {
		return nil
	}
	held.ContextWindow = window
	return &held
}

// MessagePicture is the picture id of the conversation at path, as a read of
// its messages found it: its media type and its bytes, read back from its
// entry. It is ErrNoRecord for a picture no read found.
func (r *Reader) MessagePicture(path, conversation, id string) (string, []byte, error) {
	return r.messagePicture(path, conversation, id, openMessageFile)
}

// messagePicture is MessagePicture reading the picture's entry through open.
// Each reader holds its conversation's lock only to bring the log up to date
// and find the picture, and again to check the conversation is still the one
// it was found in, never while the entry is read and decoded, so the
// conversation's stream never waits on a picture, however large its entry.
func (r *Reader) messagePicture(path, conversation, id string, open func(string) (messageReadFile, error)) (string, []byte, error) {
	if isCodexRollout(path) {
		return r.codexMessagePicture(path, conversation, id, open)
	}
	if isPiSession(path) {
		return r.piMessagePicture(path, conversation, id, open)
	}
	r.messagesMu.Lock()
	log := r.messageLogs[path]
	r.messagesMu.Unlock()
	if log == nil {
		return "", nil, ErrNoRecord
	}
	log.mu.Lock()
	err := log.update(context.Background(), func() uint64 { return messageEpoch.Add(1) }, messageTailReach)
	isCurrent := conversation != "" && conversation == (MessageCursor{epoch: log.epoch}).Conversation()
	at, isKept := log.pictures[id]
	state := log.fileState
	log.mu.Unlock()
	if err != nil {
		return "", nil, err
	}
	if !isCurrent || !isKept {
		return "", nil, ErrNoRecord
	}
	file, err := open(path)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	media, data, err := resolvePicture(file, path, state, at.offset, func(line []byte) (string, string, bool) {
		var entry messageEntry
		var blocks []messageBlock
		if json.Unmarshal(line, &entry) != nil || json.Unmarshal(entry.Message.Content, &blocks) != nil || at.block >= len(blocks) {
			return "", "", false
		}
		block := blocks[at.block]
		if at.part >= 0 {
			var parts []messageBlock
			if json.Unmarshal(block.Content, &parts) != nil || at.part >= len(parts) {
				return "", "", false
			}
			block = parts[at.part]
		}
		return block.Source.MediaType, block.Source.Data, block.Type == "image" && block.Source.Type == "base64" && slices.Contains(pictureTypes, block.Source.MediaType)
	})
	if err != nil {
		return "", nil, err
	}
	log.mu.Lock()
	err = log.update(context.Background(), func() uint64 { return messageEpoch.Add(1) }, messageTailReach)
	isCurrent = conversation == (MessageCursor{epoch: log.epoch}).Conversation()
	log.mu.Unlock()
	if err != nil {
		return "", nil, err
	}
	if !isCurrent {
		return "", nil, ErrNoRecord
	}
	return media, data, nil
}

type messageReadFile interface {
	pictureFile
	io.Closer
}

func openMessageFile(path string) (messageReadFile, error) {
	return fsx.Open(path)
}

// openMessageFileContext opens a conversation's file as openMessageFile
// does, with its retries bounded by ctx.
func openMessageFileContext(ctx context.Context) func(string) (messageReadFile, error) {
	return func(path string) (messageReadFile, error) { return fsx.OpenContext(ctx, path) }
}

type pictureFile interface {
	io.ReaderAt
	Stat() (os.FileInfo, error)
}

func resolvePicture(file pictureFile, path string, state messageFileState, offset int64, extract func([]byte) (string, string, bool)) (string, []byte, error) {
	validate := func() error {
		info, err := file.Stat()
		if err != nil {
			return err
		}
		current, err := os.Stat(path)
		if err != nil {
			return err
		}
		changed, err := state.changed(file, info, state.prefixOffset)
		if err != nil {
			return err
		}
		if changed || !os.SameFile(info, current) {
			return ErrNoRecord
		}
		return nil
	}
	if err := validate(); err != nil {
		return "", nil, err
	}
	line, err := bufio.NewReader(io.NewSectionReader(file, offset, pictureEntryReach)).ReadBytes('\n')
	if err != nil {
		return "", nil, err
	}
	if err := validate(); err != nil {
		return "", nil, err
	}
	media, encoded, ok := extract(line)
	if !ok {
		return "", nil, ErrNoRecord
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return "", nil, ErrNoRecord
	}
	return media, data, nil
}
