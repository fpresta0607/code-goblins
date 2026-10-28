package fleettree

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/routing"
)

// Message is one message of a Claude Code session as the board's message
// view shows it: a prompt, a reply, a compaction or an interrupt. Seq is its
// place in the session, counted from 0 in the order the messages began, and
// ID stays the same for its life: Claude Code's id of the reply, or the id
// of the entry that holds the rest.
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
	// Codex's update_plan write the whole of it each time, and none for a
	// call that failed, was denied or was interrupted, or that Codex refused.
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
	add := func(text, status string) {
		if text = firstLine(text); text != "" && len(steps) < planStepLimit {
			steps = append(steps, PlanStep{Text: bounded(text, agentLineLimit), Status: status})
		}
	}
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

// planStatus is a todo's state, pending for any Claude Code names that the
// board does not draw.
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
	// FromCFO is the CFO's brief or a line the CFO sent.
	FromCFO = "cfo"
	// FromAnswer is the Overlord's answer from the Command Center, and
	// FromPage is one he gave on a session's Scrawl page.
	FromAnswer = "answer"
	FromPage   = "page"
	// FromNotice is the fleet's own word to a session rather than the
	// Overlord's: Claude Code's notice that a background task or sub-agent
	// ended or a monitor reported, and the wake line the supervisor types
	// into a Codex or pi CFO's terminal.
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
)

// MessagePage is a page of a session's messages, oldest first, and whether
// older messages come before its first, with what the session's usage says
// as far as the conversation records it.
type MessagePage struct {
	Messages []Message `json:"messages"`
	More     bool      `json:"more"`
	Usage    *Usage    `json:"usage,omitempty"`
}

// Usage is what a session has spent, read from its own conversation: its
// input, output and cached tokens, the dollars it recorded, the context its
// last request carried against the model's window, that model, and the
// effort the session last set. A field a conversation does not record is left
// zero, and the board leaves that stat out rather than inventing it.
type Usage struct {
	InputTokens     int64   `json:"input_tokens,omitempty"`
	OutputTokens    int64   `json:"output_tokens,omitempty"`
	CacheReadTokens int64   `json:"cache_read_tokens,omitempty"`
	CostUSD         float64 `json:"cost_usd,omitempty"`
	ContextTokens   int64   `json:"context_tokens,omitempty"`
	ContextWindow   int64   `json:"context_window,omitempty"`
	Model           string  `json:"model,omitempty"`
	Effort          string  `json:"effort,omitempty"`
}

// syntheticModel is what Claude Code names as the model of an entry it writes
// itself rather than the model, such as a usage limit's notice.
const syntheticModel = "<synthetic>"

// add folds one request's usage into the session's.
func (u *Usage) add(input, output, cacheRead int64, model string) {
	u.InputTokens += input
	u.OutputTokens += output
	u.CacheReadTokens += cacheRead
	u.ContextTokens = input + output + cacheRead
	if model != "" {
		u.Model = model
	}
}

// MessageCursor is where a reader of a session's messages stands: the read
// of the conversation it last had messages from and how many changes it had
// seen by then.
type MessageCursor struct {
	epoch, revision uint64
}

// messageLog is one Claude Code conversation read as messages, kept between
// reads so each read parses only the entries written since the last. asked,
// when a reader last asked for it, is guarded by the Reader's messagesMu, and
// everything else by mu, so a first read of a long conversation holds up no
// other conversation's.
type messageLog struct {
	path  string
	asked time.Time

	mu   sync.Mutex
	read int64
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
	// usage is what the conversation says the session spent, and window the
	// context window of the model it last named, when its catalog is readable.
	usage  Usage
	window int64
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
	l.read, l.epoch = 0, epoch
	l.messages, l.changed, l.revision = nil, nil, 0
	l.replies, l.calls, l.pictures = map[string]int{}, map[string]blockAt{}, map[string]pictureAt{}
	l.usage, l.window = Usage{}, 0
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

// update parses the entries written since the last read. A conversation that
// shrank was replaced, and is read again from its start as a read of its own.
// The caller holds l.mu.
func (l *messageLog) update(epoch func() uint64) error {
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
	id := entry.UUID
	if id == "" {
		id = "@" + strconv.FormatInt(offset, 10)
	}
	switch {
	case entry.Type == "assistant":
		l.reply(entry, id)
	case entry.Type == "user" && entry.IsCompactSummary:
		l.summary(entry, id)
	case entry.Type == "user" && !entry.IsMeta:
		l.user(entry, id, offset)
	case entry.Type == "attachment" && entry.Attachment.Type == "queued_command":
		// A queued prompt is its text, or its blocks when a picture was
		// pasted into it, whose text alone is read.
		l.prompt(Message{ID: id, At: entry.Timestamp, IsQueued: true}, resultText(entry.Attachment.Prompt))
	case entry.Type == "system" && entry.Subtype == "compact_boundary":
		l.add(Message{ID: id, Kind: MessageCompaction, At: entry.Timestamp, Trigger: entry.CompactMetadata.Trigger})
	}
}

// add appends message to the log and returns where it is.
func (l *messageLog) add(message Message) int {
	message.Seq = len(l.messages)
	l.revision++
	l.messages = append(l.messages, message)
	l.changed = append(l.changed, l.revision)
	return message.Seq
}

// touch marks the message at index changed.
func (l *messageLog) touch(index int) {
	l.revision++
	l.changed[index] = l.revision
}

// reply folds one block of a reply into it: Claude Code writes each block of
// an answer as an entry of its own under the answer's id, a tool's result
// sometimes between them. Thinking it kept only the signature of shows
// nothing.
func (l *messageLog) reply(entry messageEntry, id string) {
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
		index = l.add(Message{ID: id, Kind: MessageReply, At: entry.Timestamp, Model: entry.Message.Model})
		l.replies[id] = index
		// Claude Code writes one entry for each block of an answer, and
		// repeats the answer's usage on them: only the first counts it. A
		// notice Claude Code writes itself, such as a usage limit's, names
		// no model ("<synthetic>") and used nothing, so it changes neither
		// the session's model nor how full its context is.
		if entry.Message.Model != syntheticModel {
			usage := entry.Message.Usage
			l.usage.add(usage.InputTokens+usage.CacheCreationInputTokens, usage.OutputTokens, usage.CacheReadInputTokens, entry.Message.Model)
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
			l.add(Message{ID: id, Kind: MessageInterrupt, At: entry.Timestamp, Text: notice})
		case !l.commandOutput(text):
			l.prompt(Message{ID: id, At: entry.Timestamp, IsQueued: entry.PromptSource == "queued"}, text)
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
				l.add(Message{ID: id, Kind: MessageInterrupt, At: entry.Timestamp, Text: notice})
				return
			}
			texts = append(texts, block.Text)
		case "image":
			if picture, ok := l.picture(block.Source, pictureAt{offset, i, -1}); ok {
				pictures = append(pictures, picture)
			}
		}
	}
	if len(texts) > 0 || len(pictures) > 0 {
		l.prompt(Message{ID: id, At: entry.Timestamp, IsQueued: entry.PromptSource == "queued", Pictures: pictures}, strings.Join(texts, "\n"))
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
	commandName   = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	commandArgs   = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	bashInput     = regexp.MustCompile(`(?s)^<bash-input>(.*?)</bash-input>`)
	commandStdout = regexp.MustCompile(`(?s)<(local-command|bash)-(stdout|stderr)>(.*?)</(?:local-command|bash)-(?:stdout|stderr)>`)
	// effortSet is the level Claude Code's /effort says it set, from its
	// picker or from the level typed after it, read only from the start of its
	// success line ("Set effort level to xhigh: ..." or "Effort level set to
	// auto"), so a cancelled /effort, or a refusal that lists every level as
	// "Invalid argument: hgih. Valid options are: low, ...", sets none.
	effortSet = regexp.MustCompile(`(?i)^(?:set effort level to|effort level set to) (low|medium|high|xhigh|max|auto)\b`)
	// boardAnswer begins the words the board types for the Overlord's answer
	// from the Command Center (internal/supervisor/questions.go and the
	// items beside it), to a goblin after the CFO's stamp.
	boardAnswer = regexp.MustCompile(`^(?:The Overlord answered |The Overlord changed the answer |User answer to CFO question |\d+ user answers to CFO questions)`)
	// These are the complete shapes emitted by gate_run_end.go and notify.go,
	// after fleet.Stamp, rather than arbitrary sentences beginning with CFO.
	runEndedNotice = regexp.MustCompile(`^Your run .+ ended with exit -?\d+ after [^;]+; read its output and carry on from it\.$`)
	helperNotice   = regexp.MustCompile(`(?s)^Your helper [A-Za-z0-9_-]+ (?:is done: its work is committed on its branch, .+\. Merge it into your branch with: cfo helper merge [A-Za-z0-9_-]+|asks: .+ Answer it with: cfo send [A-Za-z0-9_-]+ "<answer>"|failed: .+\. Tell it what to do with: cfo send [A-Za-z0-9_-]+ "<what>", or stop it with: cfo kill [A-Za-z0-9_-]+ --reason "<why>")$`)
	wakeDigest     = regexp.MustCompile(`^WAKE QUEUE: (?:\d+ pending\n|empty$)`)
)

// cfoBrief ends the line a goblin starts on, which names its brief
// (internal/spawn/native_launch.go).
const cfoBrief = ": it is your instruction from the CFO."

// prompt adds a prompt of text to the log, with where it came from.
func (l *messageLog) prompt(message Message, text string) {
	message.Kind = MessagePrompt
	message.Source, message.Text = promptSource(strings.TrimSpace(pastedContent.ReplaceAllString(systemReminder.ReplaceAllString(text, ""), "")))
	message.Text = bounded(message.Text, messageTextLimit)
	if message.Text != "" || len(message.Pictures) > 0 {
		l.add(message)
	}
}

// WakePrefix begins the wake line the supervisor types into a Codex or pi
// CFO's native terminal (internal/supervisor/cfo_wake.go's wakeLine builds its
// line from here), so the board reads that line as the fleet's own and the two
// can never drift apart.
const WakePrefix = "cfo watcher wake: "

// promptSource is where a prompt of text came from and the words it shows:
// a task notice's summary, the fleet's own typed wake line, a command as it
// was typed, and the CFO's words without the stamp the CFO's lines carry.
func promptSource(text string) (string, string) {
	if strings.HasPrefix(text, "<task-notification>") {
		return FromNotice, notice(text)
	}
	// The supervisor's wake line is the fleet speaking, not the Overlord: he
	// never typed it, and it is never copied as his words. Only this exact
	// opening is the fleet's, so anything else he types, "cfo" included,
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
	if match := bashInput.FindStringSubmatch(text); match != nil {
		return FromShell, strings.TrimSpace(match[1])
	}
	words, isStamped := strings.CutPrefix(text, routing.SteerPrefix)
	if isStamped {
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
	case isStamped, strings.HasPrefix(text, "Read ") && strings.HasSuffix(text, cfoBrief):
		return FromCFO, words
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
			if level := effortSet.FindStringSubmatch(printed); level != nil && (command.Text == "/effort" || strings.HasPrefix(command.Text, "/effort ")) {
				l.usage.Effort = strings.ToLower(level[1])
			}
		} else {
			command.Errors = printed
		}
	}
	l.touch(command.Seq)
	return true
}

// summary gives the compaction before it the summary Claude Code goes on
// from.
func (l *messageLog) summary(entry messageEntry, id string) {
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
	l.add(Message{ID: id, Kind: MessageCompaction, At: entry.Timestamp, Text: text})
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
	case block.IsError:
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
			if picture, ok := l.picture(content.Source, pictureAt{offset, part, i}); ok {
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
	l.touch(at.message)
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

// picture keeps where an image of source is, as a picture of the log.
func (l *messageLog) picture(source pictureSource, at pictureAt) (Picture, bool) {
	if source.Type != "base64" || !slices.Contains(pictureTypes, source.MediaType) {
		return Picture{}, false
	}
	id := strconv.Itoa(len(l.pictures) + 1)
	l.pictures[id] = at
	return Picture{ID: id, MediaType: source.MediaType, Bytes: len(source.Data)/4*3 - (len(source.Data) - len(strings.TrimRight(source.Data, "=")))}, true
}

// page is the messages before seq before, the newest page of them; a
// negative before reads the newest.
func (l *messageLog) page(before int) MessagePage {
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

// messageLog is the log of the conversation at path, read on to its end, with
// its mu held for the caller to unlock.
func (r *Reader) messageLog(path string) (*messageLog, error) {
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	epoch := func() uint64 { return r.messageEpoch.Add(1) }
	r.messagesMu.Lock()
	if r.messageLogs == nil {
		r.messageLogs = map[string]*messageLog{}
	}
	log := r.messageLogs[path]
	if log == nil {
		log = &messageLog{path: path}
		log.reset(epoch())
		r.messageLogs[path] = log
	}
	log.asked = now
	r.forgetIdleMessages(now)
	r.messagesMu.Unlock()
	log.mu.Lock()
	if err := log.update(epoch); err != nil {
		log.mu.Unlock()
		r.messagesMu.Lock()
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
	if isPiSession(path) {
		log, err := r.piLog(path)
		if err != nil {
			return MessagePage{}, MessageCursor{}, err
		}
		defer log.mu.Unlock()
		return log.page(before), MessageCursor{log.epoch, log.revision}, nil
	}
	if isCodexRollout(path) {
		log, err := r.codexLog(path)
		if err != nil {
			return MessagePage{}, MessageCursor{}, err
		}
		defer log.mu.Unlock()
		return log.page(before), MessageCursor{log.epoch, log.revision}, nil
	}
	log, err := r.messageLog(path)
	if err != nil {
		return MessagePage{}, MessageCursor{}, err
	}
	defer log.mu.Unlock()
	return log.page(before), MessageCursor{log.epoch, log.revision}, nil
}

// MessagesSince reads the Claude Code conversation at path on and returns
// every message that began or changed after cursor, with where the reader
// then stands. isAnew says the conversation was read anew since cursor, as
// one replaced is, so nothing a page before it held still stands: the
// reader takes a page again.
func (r *Reader) MessagesSince(path string, cursor MessageCursor) (messages []Message, now MessageCursor, isAnew bool, err error) {
	if isPiSession(path) {
		log, err := r.piLog(path)
		if err != nil {
			return nil, MessageCursor{}, false, err
		}
		defer log.mu.Unlock()
		messages, now, isAnew = log.since(cursor)
		return messages, now, isAnew, nil
	}
	if isCodexRollout(path) {
		log, err := r.codexLog(path)
		if err != nil {
			return nil, MessageCursor{}, false, err
		}
		defer log.mu.Unlock()
		messages, now, isAnew = log.since(cursor)
		return messages, now, isAnew, nil
	}
	log, err := r.messageLog(path)
	if err != nil {
		return nil, MessageCursor{}, false, err
	}
	defer log.mu.Unlock()
	now = MessageCursor{log.epoch, log.revision}
	if cursor.epoch != log.epoch {
		return nil, now, true, nil
	}
	for i, changed := range log.changed {
		if changed > cursor.revision {
			messages = append(messages, shared(log.messages[i]))
		}
	}
	return messages, now, false, nil
}

// Usage is what the conversation at path says the session spent, as its last
// read found it: nil while no read has found any usage. It reads nothing anew,
// so a stream that already read the messages can tell the board what the
// session has spent without parsing the file again.
func (r *Reader) Usage(path string) *Usage {
	if isCodexRollout(path) {
		r.messagesMu.Lock()
		log := r.codexLogs[path]
		r.messagesMu.Unlock()
		if log == nil {
			return nil
		}
		log.mu.Lock()
		defer log.mu.Unlock()
		return usageOf(log.usage, log.usage.ContextWindow)
	}
	if isPiSession(path) {
		r.messagesMu.Lock()
		log := r.piLogs[path]
		r.messagesMu.Unlock()
		if log == nil {
			return nil
		}
		log.mu.Lock()
		defer log.mu.Unlock()
		return usageOf(log.usage, log.window)
	}
	r.messagesMu.Lock()
	log := r.messageLogs[path]
	r.messagesMu.Unlock()
	if log == nil {
		return nil
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	return usageOf(log.usage, log.window)
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
func (r *Reader) MessagePicture(path, id string) (string, []byte, error) {
	if isCodexRollout(path) {
		return r.codexMessagePicture(path, id)
	}
	if isPiSession(path) {
		return r.piMessagePicture(path, id)
	}
	r.messagesMu.Lock()
	log := r.messageLogs[path]
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
		return "", nil, fmt.Errorf("the picture's entry cannot be read: %w", err)
	}
	var entry messageEntry
	var blocks []messageBlock
	if json.Unmarshal(line, &entry) != nil || json.Unmarshal(entry.Message.Content, &blocks) != nil || at.block >= len(blocks) {
		return "", nil, ErrNoRecord
	}
	block := blocks[at.block]
	if at.part >= 0 {
		var parts []messageBlock
		if json.Unmarshal(block.Content, &parts) != nil || at.part >= len(parts) {
			return "", nil, ErrNoRecord
		}
		block = parts[at.part]
	}
	if block.Type != "image" || block.Source.Type != "base64" || !slices.Contains(pictureTypes, block.Source.MediaType) {
		return "", nil, ErrNoRecord
	}
	data, err := base64.StdEncoding.DecodeString(block.Source.Data)
	if err != nil {
		return "", nil, fmt.Errorf("the picture's data cannot be read: %w", err)
	}
	return block.Source.MediaType, data, nil
}
