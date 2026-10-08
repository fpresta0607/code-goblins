package fleettree

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// claudeLog is what one Claude Code conversation records of the children it
// started, kept between reads so each read parses only the entries written
// since the last.
//
// Claude Code 2.1 records each child in the conversation itself: the Agent
// (formerly Task) call that starts a sub-agent, with its type and
// description; the Bash or PowerShell call whose result names a background
// task id when the command was left running; the Monitor call whose result
// names its task id; a TaskStop that ends one; and a task-notification, once
// queued and once delivered, when a background task or sub-agent completes,
// fails, is killed or stopped, or a monitor reports an event. Each sub-agent
// keeps its own transcript under <session>/subagents/agent-<id>.jsonl.
type claudeLog struct {
	path string
	// read is how many bytes of whole entries were parsed, written the
	// conversation's write time when last read, and lastEntry the newest
	// entry's own timestamp.
	read      int64
	written   time.Time
	lastEntry time.Time
	// children are the children by the id of the call that started them;
	// tasks names that call for each harness task id (an agent's id, a
	// background shell's or monitor's task id).
	children map[string]*Node
	tasks    map[string]string
	// pending are the calls that become children only when their result
	// says they kept running, by call id.
	pending map[string]pendingCall
	// agents and outputs are each sub-agent's id and each shell's output
	// file, by call id, and commands each background shell's command, by
	// its node's id.
	agents   map[string]string
	outputs  map[string]string
	commands map[string]string
}

type pendingCall struct {
	tool    string
	label   string
	command string
	target  string
	started time.Time
}

type claudeEntry struct {
	Type      string          `json:"type"`
	Operation string          `json:"operation"`
	Content   json.RawMessage `json:"content"`
	Timestamp time.Time       `json:"timestamp"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

func newClaudeLog(path string) *claudeLog {
	return &claudeLog{path: path, children: map[string]*Node{}, tasks: map[string]string{}, pending: map[string]pendingCall{}, agents: map[string]string{}, outputs: map[string]string{}, commands: map[string]string{}}
}

// claudeReadChunk bounds one read of new entries.
const claudeReadChunk = 4 << 20

// update parses the entries written since the last read. A conversation that
// shrank was replaced, and is read again from its start.
func (l *claudeLog) update() error {
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
		*l = *newClaudeLog(l.path)
	}
	l.written = info.ModTime().UTC()
	buffer := make([]byte, claudeReadChunk)
	var carry []byte
	for offset := l.read; offset < info.Size(); {
		count, err := file.ReadAt(buffer[:min(int64(len(buffer)), info.Size()-offset)], offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if count == 0 {
			break
		}
		offset += int64(count)
		data := append(carry, buffer[:count]...)
		last := bytes.LastIndexByte(data, '\n')
		if last < 0 {
			carry = data
			continue
		}
		lines := bytes.Split(data[:last], []byte("\n"))
		for _, line := range lines {
			l.entry(line)
		}
		l.stamp(lines)
		l.read = offset - int64(len(data)-last-1)
		carry = append([]byte(nil), data[last+1:]...)
	}
	return nil
}

// stamp keeps the newest entry's own timestamp among lines, read from the
// last that carries one.
func (l *claudeLog) stamp(lines [][]byte) {
	for i := len(lines) - 1; i >= 0; i-- {
		var entry struct {
			Timestamp time.Time `json:"timestamp"`
		}
		if json.Unmarshal(lines[i], &entry) == nil && !entry.Timestamp.IsZero() {
			l.lastEntry = later(l.lastEntry, entry.Timestamp.UTC())
			return
		}
	}
}

// writtenAt is when the conversation was last written: the later of its
// write time and its newest entry's timestamp.
func (l *claudeLog) writtenAt() time.Time {
	return later(l.written, l.lastEntry)
}

// entry folds one transcript entry into the log. Only an entry that can
// carry a child is decoded at all.
func (l *claudeLog) entry(line []byte) {
	if !bytes.Contains(line, []byte(`"tool_use`)) && !bytes.Contains(line, []byte("task-notification")) {
		return
	}
	var entry claudeEntry
	if json.Unmarshal(line, &entry) != nil {
		return
	}
	if entry.Type == "queue-operation" {
		var content string
		if entry.Operation == "enqueue" && json.Unmarshal(entry.Content, &content) == nil {
			l.notification(content, entry.Timestamp)
		}
		return
	}
	var text string
	if json.Unmarshal(entry.Message.Content, &text) == nil {
		l.notification(text, entry.Timestamp)
		return
	}
	var blocks []claudeBlock
	if json.Unmarshal(entry.Message.Content, &blocks) != nil {
		return
	}
	for _, block := range blocks {
		switch block.Type {
		case "tool_use":
			l.call(block, entry.Timestamp)
		case "tool_result":
			l.result(block, entry.ToolUseResult, entry.Timestamp)
		}
	}
}

// call records a tool call that starts or ends a child.
func (l *claudeLog) call(block claudeBlock, at time.Time) {
	var input struct {
		Description  string `json:"description"`
		SubagentType string `json:"subagent_type"`
		Command      string `json:"command"`
		TaskID       string `json:"task_id"`
		ShellID      string `json:"shell_id"`
		To           string `json:"to"`
	}
	_ = json.Unmarshal(block.Input, &input)
	switch block.Name {
	case "Agent", "Task":
		detail := input.SubagentType
		if detail == "" {
			detail = "general-purpose"
		}
		l.children[block.ID] = &Node{ID: "subagent:" + block.ID, Kind: KindSubagent, Label: label(input.Description, "Sub-agent"), Detail: detail, State: Working, Started: at, LastActivity: at}
	case "Bash", "PowerShell":
		l.pending[block.ID] = pendingCall{tool: block.Name, label: label(input.Description, firstLine(input.Command)), command: input.Command, started: at}
	case "Monitor":
		l.pending[block.ID] = pendingCall{tool: block.Name, label: label(input.Description, "Monitor"), command: input.Command, started: at}
	case "TaskStop", "KillShell":
		l.pending[block.ID] = pendingCall{tool: block.Name, target: input.TaskID + input.ShellID, started: at}
	case "SendMessage":
		// A message to a finished sub-agent resumes it.
		if id, ok := l.tasks[input.To]; ok {
			if child := l.children[id]; child != nil && child.Kind == KindSubagent {
				child.State, child.Finished, child.LastActivity = Working, time.Time{}, later(child.LastActivity, at)
			}
		}
	}
}

// outputFile is where Claude Code says a background command writes.
var outputFile = regexp.MustCompile(`Output is being written to: (\S+?\.output)`)

// result records what a call's result says of the child it started.
func (l *claudeLog) result(block claudeBlock, raw json.RawMessage, at time.Time) {
	var result struct {
		Status           string `json:"status"`
		AgentID          string `json:"agentId"`
		BackgroundTaskID string `json:"backgroundTaskId"`
		TaskID           string `json:"taskId"`
		StoppedTaskID    string `json:"task_id"`
	}
	_ = json.Unmarshal(raw, &result)
	if child := l.children[block.ToolUseID]; child != nil && child.Kind == KindSubagent {
		if result.AgentID != "" {
			l.agents[block.ToolUseID] = result.AgentID
			l.tasks[result.AgentID] = block.ToolUseID
		}
		switch {
		case block.IsError:
			finish(child, Failed, at)
		case result.Status == "completed":
			finish(child, Done, at)
		case result.Status != "" && result.Status != "async_launched":
			finish(child, Failed, at)
			child.LastLine = result.Status
		}
		return
	}
	call, ok := l.pending[block.ToolUseID]
	if !ok {
		return
	}
	delete(l.pending, block.ToolUseID)
	if block.IsError {
		return
	}
	switch call.tool {
	case "Bash", "PowerShell":
		if result.BackgroundTaskID == "" {
			return
		}
		l.children[block.ToolUseID] = &Node{ID: "shell:" + result.BackgroundTaskID, Kind: KindShell, Label: call.label, Detail: "background " + strings.ToLower(call.tool), State: Working, Started: call.started, LastActivity: at}
		l.tasks[result.BackgroundTaskID] = block.ToolUseID
		l.commands["shell:"+result.BackgroundTaskID] = call.command
		if match := outputFile.FindStringSubmatch(string(block.Content)); match != nil {
			var path string
			if json.Unmarshal([]byte(`"`+match[1]+`"`), &path) != nil {
				path = match[1]
			}
			l.outputs[block.ToolUseID] = path
		}
	case "Monitor":
		if result.TaskID == "" {
			return
		}
		l.children[block.ToolUseID] = &Node{ID: "monitor:" + result.TaskID, Kind: KindMonitor, Label: call.label, Detail: "monitor", State: Working, Started: call.started, LastActivity: at}
		l.tasks[result.TaskID] = block.ToolUseID
	case "TaskStop", "KillShell":
		target := result.StoppedTaskID
		if target == "" {
			target = call.target
		}
		if child := l.children[l.tasks[target]]; child != nil && (child.State == Working || child.State == Silent) {
			finish(child, Done, at)
			child.LastLine = "stopped by the goblin"
		}
	}
}

var notificationTag = regexp.MustCompile(`(?s)<(task-id|tool-use-id|status|summary|event|output-file)>(.*?)</(?:task-id|tool-use-id|status|summary|event|output-file)>`)

// notification records a task-notification: a child's end, or a monitor's
// event.
func (l *claudeLog) notification(text string, at time.Time) {
	if !strings.Contains(text, "<task-notification>") {
		return
	}
	tags := map[string]string{}
	for _, match := range notificationTag.FindAllStringSubmatch(text, -1) {
		if _, seen := tags[match[1]]; !seen {
			tags[match[1]] = strings.TrimSpace(match[2])
		}
	}
	id, ok := l.tasks[tags["task-id"]]
	if !ok {
		id = tags["tool-use-id"]
	}
	child := l.children[id]
	if child == nil {
		return
	}
	if path := tags["output-file"]; path != "" {
		l.outputs[id] = path
	}
	switch tags["status"] {
	case "completed":
		finish(child, Done, at)
	case "failed", "killed", "stopped":
		if finish(child, Failed, at) {
			child.LastLine = bounded(tags["summary"], 200)
		}
	case "":
		if event := tags["event"]; event != "" {
			child.LastActivity = later(child.LastActivity, at)
			child.LastLine = bounded(firstLine(event), 200)
		}
	}
}

// finish ends a child at, the first time it ends: the delivered copy of a
// notification arrives after the queued one. A child already done stays
// done, since the killed notice that follows a stop the goblin made does not
// undo it. It reports whether the child's state changed.
func finish(child *Node, state State, at time.Time) bool {
	switch {
	case child.State == state || child.State == Done:
		return false
	case child.State == Failed:
		child.State = state
		return true
	}
	child.State, child.Finished, child.LastActivity = state, at, later(child.LastActivity, at)
	return true
}

// claudeChildrenKept is how many children a conversation's tree shows: every
// working one and the newest finished ones.
const claudeChildrenKept = 40

// nodes returns the conversation's children, with what each one's own files
// say: a sub-agent's transcript and a shell's output, each read at most once
// per call.
func (l *claudeLog) nodes(now time.Time) []Node {
	session := strings.TrimSuffix(l.path, filepath.Ext(l.path))
	agentsByCall := l.agentFiles(session)
	var out []Node
	for id, child := range l.children {
		node := *child
		if node.Kind == KindSubagent {
			agent := l.agents[id]
			if agent == "" {
				agent = agentsByCall[id]
			}
			if agent != "" {
				transcript := filepath.Join(session, "subagents", "agent-"+agent+".jsonl")
				if written, line := claudeTail(transcript); !written.IsZero() {
					node.SourceUpdatedAt = written
					node.LastActivity = later(node.LastActivity, written)
					if line != "" {
						node.LastLine = line
					}
				}
			}
		}
		if node.Kind == KindShell && node.State != Done {
			if written, line := outputTail(l.outputs[id]); !written.IsZero() {
				node.SourceUpdatedAt = written
				node.LastActivity = later(node.LastActivity, written)
				// A running shell shows what it last wrote; a failed one
				// keeps why it ended, unless that was not said.
				if node.State == Working || node.LastLine == "" {
					node.LastLine = line
				}
			}
		}
		if node.SourceUpdatedAt.IsZero() {
			node.SourceUpdatedAt = node.LastActivity
		}
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	var kept []Node
	finished := 0
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].State == Done || out[i].State == Failed {
			if finished++; finished > claudeChildrenKept {
				continue
			}
		}
		kept = append([]Node{out[i]}, kept...)
	}
	return kept
}

// agentFiles maps each sub-agent's starting call to its id, from the
// .meta.json Claude Code writes beside each sub-agent's transcript, for a
// sub-agent whose result named no id.
func (l *claudeLog) agentFiles(session string) map[string]string {
	byCall := map[string]string{}
	metas, _ := filepath.Glob(filepath.Join(session, "subagents", "agent-*.meta.json"))
	for _, meta := range metas {
		data, err := fsx.ReadFile(meta)
		if err != nil {
			continue
		}
		var record struct {
			ToolUseID string `json:"toolUseId"`
		}
		if json.Unmarshal(data, &record) == nil && record.ToolUseID != "" {
			byCall[record.ToolUseID] = strings.TrimSuffix(strings.TrimPrefix(filepath.Base(meta), "agent-"), ".meta.json")
		}
	}
	return byCall
}

// claudeTail reads when a Claude Code transcript was last written and the
// last thing it said or did: its last text, or the tool it last called.
func claudeTail(path string) (time.Time, string) {
	written, entries := tail(path)
	for i := len(entries) - 1; i >= 0; i-- {
		var entry claudeEntry
		if json.Unmarshal(entries[i], &entry) != nil {
			continue
		}
		var blocks []struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Name  string `json:"name"`
			Input struct {
				Description string `json:"description"`
				Command     string `json:"command"`
				Pattern     string `json:"pattern"`
				FilePath    string `json:"file_path"`
			} `json:"input"`
		}
		if json.Unmarshal(entry.Message.Content, &blocks) != nil {
			continue
		}
		for j := len(blocks) - 1; j >= 0; j-- {
			block := blocks[j]
			switch {
			case block.Type == "text" && strings.TrimSpace(block.Text) != "":
				return written, bounded(firstLine(block.Text), 200)
			case block.Type == "tool_use":
				what := label(block.Input.Description, label(block.Input.Pattern, label(block.Input.FilePath, firstLine(block.Input.Command))))
				return written, bounded(strings.TrimSpace(block.Name+" "+what), 200)
			}
		}
	}
	return written, ""
}

// outputTail reads when a background command's output file was last written
// and its last line.
func outputTail(path string) (time.Time, string) {
	if path == "" {
		return time.Time{}, ""
	}
	file, err := fsx.Open(path)
	if err != nil {
		return time.Time{}, ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return time.Time{}, ""
	}
	start := max(0, info.Size()-outputReach)
	data := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(data, start); err != nil && !errors.Is(err, io.EOF) {
		return info.ModTime().UTC(), ""
	}
	lines := strings.Split(strings.ReplaceAll(ansi.ReplaceAllString(string(data), ""), "\r", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return info.ModTime().UTC(), bounded(line, 200)
		}
	}
	return info.ModTime().UTC(), ""
}

// outputReach bounds how much of an output file's end is read for its last
// line.
const outputReach = 8 << 10

// ansi matches a terminal's color and cursor sequences.
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func label(text, fallback string) string {
	if text = strings.TrimSpace(text); text != "" {
		return bounded(firstLine(text), 120)
	}
	return bounded(fallback, 120)
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func later(one, other time.Time) time.Time {
	if other.After(one) {
		return other
	}
	return one
}
