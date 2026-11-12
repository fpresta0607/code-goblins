package fleettree

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// childRecords are a goblin's children's own outputs, by node id, and the
// harness that keeps its sub-agents' records.
type childRecords struct {
	harness  string
	children map[string]childRecord
}

// childRecord is where one child's own output is: the record its harness
// keeps of a sub-agent, or the lines the read itself holds of any other
// child, followed for a background shell by the newest lines of its output
// file.
type childRecord struct {
	transcript string
	lines      []string
	output     string
}

// ErrNoRecord says the last read of a goblin's tree kept no output of the
// child named.
var ErrNoRecord = errors.New("no output of that child is kept")

// Output is a child's own output as its terminal shows it: a sub-agent's
// work as the entries of its record, or any other child's lines.
type Output struct {
	Entries []AgentEntry `json:"entries,omitempty"`
	Lines   []string     `json:"lines"`
}

// ChildOutput is a child of task's goblin's own output, as the reader last
// read that goblin's tree: a sub-agent's work as the record its harness keeps
// of it holds it, Claude Code's transcript of the sub-agent or a Codex child
// agent's rollout (AgentEntries), and otherwise its newest lines: a
// background shell's command and what it wrote, a monitor's command and its
// events, the programs a job of processes runs, or a gate run's steps. Never
// the goblin's, and never another child's.
func (r *Reader) ChildOutput(task, node string) (Output, error) {
	r.mu.Lock()
	records := r.records[task]
	record, isKept := records.children[node]
	r.mu.Unlock()
	switch {
	case !isKept:
		return Output{}, ErrNoRecord
	case record.transcript != "":
		entries, err := r.agentEntries(task, record.transcript, records.harness)
		return Output{Entries: entries}, err
	}
	lines := slices.Clone(record.lines)
	if record.output != "" {
		output, err := outputFileLines(record.output)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Output{}, err
		}
		lines = append(lines, output...)
		if errors.Is(err, fs.ErrNotExist) {
			lines = append(lines, "Waiting for the child's output file")
		}
	}
	return Output{Lines: lines[max(0, len(lines)-ChildLinesKept):]}, nil
}

// agentOutput is the last reading of a sub-agent's record a child's terminal
// showed, and the size and write time the record had when it was read.
type agentOutput struct {
	path     string
	size     int64
	modified time.Time
	entries  []AgentEntry
}

// agentEntries is AgentEntries for a child of task's goblin, read again only
// once the record's size or write time changed. A child's terminal asks for
// its output every half second, and each read took up to 4 MiB of the record
// (#1716). One reading is kept for each goblin, of the child its terminal
// last showed.
func (r *Reader) agentEntries(task, path, harness string) ([]AgentEntry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return AgentEntries(path, harness)
	}
	r.mu.Lock()
	kept, isKept := r.agentOutputs[task]
	r.mu.Unlock()
	if isKept && kept.path == path && kept.size == info.Size() && kept.modified.Equal(info.ModTime()) {
		return kept.entries, nil
	}
	entries, err := AgentEntries(path, harness)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.agentOutputs == nil {
		r.agentOutputs = map[string]agentOutput{}
	}
	r.agentOutputs[task] = agentOutput{path: path, size: info.Size(), modified: info.ModTime(), entries: entries}
	r.mu.Unlock()
	return entries, nil
}

// outputFileLines are the newest lines a background shell wrote to its
// output file.
func outputFileLines(path string) ([]string, error) {
	file, err := fsx.Open(path)
	if err != nil {
		return nil, fmt.Errorf("the shell's output cannot be read: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("the shell's output cannot be read: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("the shell's output is not a regular file")
	}
	start := max(0, info.Size()-outputFileReach)
	data := make([]byte, info.Size()-start)
	count, err := file.ReadAt(data, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("the shell's output cannot be read: %w", err)
	}
	data = data[:count]
	if start > 0 {
		if end := strings.IndexByte(string(data), '\n'); end >= 0 {
			data = data[end+1:]
		}
	}
	lines := strings.Split(string(data), "\n")
	return lines[max(0, len(lines)-ChildLinesKept):], nil
}

// outputFileReach bounds how much of an output file's end its terminal
// reads.
const outputFileReach = 256 << 10

// outputLines are text's lines as a terminal shows them: its colors and
// cursor moves dropped, a carriage return starting a line, blank lines left
// out and each line bounded.
func outputLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(ansi.ReplaceAllString(text, ""), "\r", "\n"), "\n") {
		if line = strings.TrimRight(line, " \t"); strings.TrimSpace(line) != "" {
			lines = append(lines, bounded(line, agentLineLimit))
		}
	}
	return lines
}

// ChildLinesKept is how many of a child's newest lines its terminal holds,
// and agentLineLimit how long each may run.
const (
	ChildLinesKept = 400
	agentLineLimit = 400
)

// AgentEntry is one step of a sub-agent's work as its own record holds it,
// which the board draws as Claude Code draws a conversation: a prompt it was
// given, a reply it wrote, or a tool it called, with what it called it on,
// whether the call is running, done or failed, and the first lines of what
// came back with how many lines more.
type AgentEntry struct {
	Kind    string   `json:"kind"`
	Text    string   `json:"text,omitempty"`
	Tool    string   `json:"tool,omitempty"`
	Subject string   `json:"subject,omitempty"`
	State   string   `json:"state,omitempty"`
	Result  []string `json:"result,omitempty"`
	More    int      `json:"more,omitempty"`
}

// The kinds of an AgentEntry, and the states of a call.
const (
	EntryPrompt = "prompt"
	EntryReply  = "reply"
	EntryCall   = "call"

	CallRunning = "running"
	CallDone    = "done"
	CallFailed  = "failed"
)

const (
	// AgentEntriesKept is how many of a sub-agent's newest entries its
	// terminal holds, and agentTextLimit how long a prompt or reply may run.
	AgentEntriesKept = 200
	agentTextLimit   = 8000
	// resultLinesShown is how many lines of what came back Claude Code shows
	// under a call before it folds the rest into "+N lines".
	resultLinesShown = 3
)

// AgentEntries are the newest entries of a sub-agent's work as its own
// record holds it. Its thinking, and anything else the record keeps, is left
// out. harness is the harness that wrote the record, claude or codex.
func AgentEntries(path, harness string) ([]AgentEntry, error) {
	written, records := tail(path)
	if written.IsZero() {
		return nil, errors.New("the sub-agent's record cannot be read")
	}
	work := agentWork{calls: map[string]int{}}
	read := work.claude
	if strings.EqualFold(harness, "codex") {
		read = work.codex
	}
	for _, record := range records {
		read(record)
	}
	return work.entries[max(0, len(work.entries)-AgentEntriesKept):], nil
}

// agentWork gathers a sub-agent's entries, and where each call it made is
// among them by its id, so what came back finds its call.
type agentWork struct {
	entries []AgentEntry
	calls   map[string]int
}

func (w *agentWork) said(kind, text string) {
	if text = strings.TrimSpace(text); text != "" {
		w.entries = append(w.entries, AgentEntry{Kind: kind, Text: bounded(text, agentTextLimit)})
	}
}

func (w *agentWork) called(id, tool, subject string) {
	w.calls[id] = len(w.entries)
	w.entries = append(w.entries, AgentEntry{Kind: EntryCall, Tool: tool, Subject: bounded(subject, agentLineLimit), State: CallRunning})
}

// cameBack settles a call with what it returned. What came back for a call
// the read did not reach is left out.
func (w *agentWork) cameBack(id, text string, isError bool) {
	index, isCalled := w.calls[id]
	if !isCalled {
		return
	}
	call := &w.entries[index]
	call.State = CallDone
	if isError {
		call.State = CallFailed
	}
	call.Result, call.More = resultLines(call.Tool, text, isError)
}

// claude reads one entry of a Claude Code transcript.
func (w *agentWork) claude(data []byte) {
	var entry struct {
		Type    string `json:"type"`
		Cwd     string `json:"cwd"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(data, &entry) != nil || entry.Type != "user" && entry.Type != "assistant" {
		return
	}
	var text string
	if json.Unmarshal(entry.Message.Content, &text) == nil {
		w.said(EntryPrompt, text)
		return
	}
	var blocks []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
		IsError   bool            `json:"is_error"`
	}
	if json.Unmarshal(entry.Message.Content, &blocks) != nil {
		return
	}
	for _, block := range blocks {
		switch {
		case block.Type == "text" && entry.Type == "user":
			w.said(EntryPrompt, block.Text)
		case block.Type == "text":
			w.said(EntryReply, block.Text)
		case block.Type == "tool_use":
			w.called(block.ID, toolName(block.Name), callSubject(block.Input, entry.Cwd))
		case block.Type == "tool_result":
			w.cameBack(block.ToolUseID, resultText(block.Content), block.IsError)
		}
	}
}

// codex reads one item of a Codex rollout. The rollout's own instructions to
// the agent, which it records as developer messages and as user messages in
// tags, are left out.
func (w *agentWork) codex(data []byte) {
	var entry struct {
		Type    string `json:"type"`
		Payload struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Name      string          `json:"name"`
			CallID    string          `json:"call_id"`
			Arguments string          `json:"arguments"`
			Input     string          `json:"input"`
			Output    json.RawMessage `json:"output"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"payload"`
	}
	if json.Unmarshal(data, &entry) != nil || entry.Type != "response_item" {
		return
	}
	item := entry.Payload
	switch item.Type {
	case "message":
		for _, part := range item.Content {
			switch {
			case item.Role == "user" && part.Type == "input_text" && !strings.HasPrefix(strings.TrimSpace(part.Text), "<"):
				w.said(EntryPrompt, part.Text)
			case item.Role == "assistant" && part.Type == "output_text":
				w.said(EntryReply, part.Text)
			}
		}
	case "function_call", "custom_tool_call":
		w.called(item.CallID, item.Name, firstLine(item.Arguments+item.Input))
	case "function_call_output", "custom_tool_call_output":
		w.cameBack(item.CallID, resultText(item.Output), false)
	}
}

// toolNames are the names Claude Code shows for tools its record names
// otherwise.
var toolNames = map[string]string{
	"Edit": "Update", "MultiEdit": "Update", "Grep": "Search", "Glob": "Search",
	"WebFetch": "Fetch", "WebSearch": "Web Search", "TodoWrite": "Update Todos",
}

func toolName(name string) string {
	if shown, isRenamed := toolNames[name]; isRenamed {
		return shown
	}
	return name
}

// callSubject is what a tool was called on, as Claude Code shows it: the
// command, file, pattern, address, query or description it was given, a file
// under the folder the sub-agent works in relative to it.
func callSubject(input json.RawMessage, cwd string) string {
	var fields struct {
		Command     string `json:"command"`
		FilePath    string `json:"file_path"`
		Pattern     string `json:"pattern"`
		URL         string `json:"url"`
		Query       string `json:"query"`
		Description string `json:"description"`
		Path        string `json:"path"`
		Prompt      string `json:"prompt"`
	}
	_ = json.Unmarshal(input, &fields)
	if cwd != "" && len(fields.FilePath) > len(cwd)+1 && strings.EqualFold(fields.FilePath[:len(cwd)], cwd) && strings.ContainsRune(`\/`, rune(fields.FilePath[len(cwd)])) {
		fields.FilePath = fields.FilePath[len(cwd)+1:]
	}
	for _, subject := range []string{fields.Command, fields.FilePath, fields.Pattern, fields.URL, fields.Query, fields.Description, fields.Path, fields.Prompt} {
		if line := firstLine(subject); line != "" {
			return line
		}
	}
	return ""
}

// toolError is the tag Claude Code wraps a refused call's error in.
var toolError = regexp.MustCompile(`</?tool_use_error>`)

// resultLines are the lines of what a call returned that Claude Code shows
// under it, and how many lines more it holds: a read's line count, a file
// search's file count, the first line of a search or an edit, an error
// marked, and otherwise the first lines.
func resultLines(tool, text string, isError bool) ([]string, int) {
	var kept []string
	for _, line := range strings.Split(toolError.ReplaceAllString(text, ""), "\n") {
		if line = strings.TrimRight(line, " \t\r"); strings.TrimSpace(line) != "" {
			kept = append(kept, bounded(line, agentLineLimit))
		}
	}
	if len(kept) == 0 {
		return nil, 0
	}
	switch {
	case isError:
		kept[0] = "Error: " + strings.TrimSpace(kept[0])
	case tool == "Read":
		return []string{fmt.Sprintf("Read %d %s", len(kept), plural(len(kept), "line"))}, 0
	case tool == "Search" && !strings.HasPrefix(kept[0], "Found ") && !strings.HasPrefix(kept[0], "No "):
		return []string{fmt.Sprintf("Found %d %s", len(kept), plural(len(kept), "file"))}, 0
	case tool == "Search" || tool == "Update" || tool == "Write":
		return kept[:1], 0
	}
	shown := min(len(kept), resultLinesShown)
	return kept[:shown], len(kept) - shown
}

func plural(count int, word string) string {
	if count == 1 {
		return word
	}
	return word + "s"
}

// resultText is a call's result as text: a string, or the text of each of
// its parts.
func resultText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(content, &parts)
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		texts = append(texts, part.Text)
	}
	return strings.Join(texts, "\n")
}
