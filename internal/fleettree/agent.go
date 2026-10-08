package fleettree

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// agentRecords are a goblin's sub-agents' own records, by node id, and the
// harness that keeps them.
type agentRecords struct {
	harness string
	paths   map[string]string
}

// keepAgents keeps task's sub-agents' records as a read of its tree found
// them. The caller holds the reader's lock.
func (r *Reader) keepAgents(task, harness string, paths map[string]string) {
	if r.agents == nil {
		r.agents = map[string]agentRecords{}
	}
	r.agents[task] = agentRecords{harness: harness, paths: paths}
}

// AgentTranscript is the record the harness keeps of the sub-agent node of
// task's goblin, as the reader last read that goblin's tree, and the harness
// that keeps it: Claude Code's transcript of the sub-agent, or a Codex child
// agent's rollout. It is empty for a node the last read found no record of.
func (r *Reader) AgentTranscript(task, node string) (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	records := r.agents[task]
	if path := records.paths[node]; path != "" {
		return path, records.harness
	}
	return "", ""
}

// AgentLinesKept is how many of a sub-agent's newest lines its terminal
// holds, and agentLineLimit how long each may run.
const (
	AgentLinesKept = 400
	agentLineLimit = 400
)

// AgentLines are the newest lines of a sub-agent's work as its own record
// holds it, drawn as Claude Code draws a conversation: the task it was given
// after >, what it said and each tool it called after ●, and under a call the
// first line of what came back after ⎿, with how many lines more. Its
// thinking, and anything else the record keeps, is left out. harness is the
// harness that wrote the record, claude or codex.
func AgentLines(path, harness string) ([]string, error) {
	written, entries := tail(path)
	if written.IsZero() {
		return nil, errors.New("the sub-agent's record cannot be read")
	}
	draw := claudeAgentEntry
	if strings.EqualFold(harness, "codex") {
		draw = codexAgentEntry
	}
	var lines []string
	for _, entry := range entries {
		for _, line := range draw(entry) {
			lines = append(lines, bounded(line, agentLineLimit))
		}
	}
	return lines[max(0, len(lines)-AgentLinesKept):], nil
}

// claudeAgentEntry draws one entry of a Claude Code transcript.
func claudeAgentEntry(data []byte) []string {
	var entry struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(data, &entry) != nil || entry.Type != "user" && entry.Type != "assistant" {
		return nil
	}
	var text string
	if json.Unmarshal(entry.Message.Content, &text) == nil {
		return marked(">", text)
	}
	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Name    string          `json:"name"`
		Input   json.RawMessage `json:"input"`
		Content json.RawMessage `json:"content"`
		IsError bool            `json:"is_error"`
	}
	if json.Unmarshal(entry.Message.Content, &blocks) != nil {
		return nil
	}
	var lines []string
	for _, block := range blocks {
		switch {
		case block.Type == "text" && entry.Type == "user":
			lines = append(lines, marked(">", block.Text)...)
		case block.Type == "text":
			lines = append(lines, marked("●", block.Text)...)
		case block.Type == "tool_use":
			lines = append(lines, "● "+block.Name+"("+callSubject(block.Input)+")")
		case block.Type == "tool_result":
			lines = append(lines, cameBack(resultText(block.Content), block.IsError)...)
		}
	}
	return lines
}

// codexAgentEntry draws one item of a Codex rollout. The rollout's own
// instructions to the agent, which it records as developer messages and as
// user messages in tags, are left out.
func codexAgentEntry(data []byte) []string {
	var entry struct {
		Type    string `json:"type"`
		Payload struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Name      string          `json:"name"`
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
		return nil
	}
	item := entry.Payload
	switch item.Type {
	case "message":
		var lines []string
		for _, part := range item.Content {
			switch {
			case item.Role == "user" && part.Type == "input_text" && !strings.HasPrefix(strings.TrimSpace(part.Text), "<"):
				lines = append(lines, marked(">", part.Text)...)
			case item.Role == "assistant" && part.Type == "output_text":
				lines = append(lines, marked("●", part.Text)...)
			}
		}
		return lines
	case "function_call", "custom_tool_call":
		return []string{"● " + item.Name + "(" + firstLine(item.Arguments+item.Input) + ")"}
	case "function_call_output", "custom_tool_call_output":
		return cameBack(resultText(item.Output), false)
	}
	return nil
}

// marked draws text after mark, each further line under it.
func marked(mark, text string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line = strings.TrimRight(line, " \t\r"); strings.TrimSpace(line) == "" {
			continue
		}
		if lines == nil {
			lines = append(lines, mark+" "+strings.TrimSpace(line))
		} else {
			lines = append(lines, "  "+line)
		}
	}
	return lines
}

// cameBack draws what a call returned: its first line, an error marked, and
// how many lines more it holds.
func cameBack(text string, isError bool) []string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			kept = append(kept, strings.TrimSpace(line))
		}
	}
	if len(kept) == 0 {
		return nil
	}
	first := kept[0]
	if isError {
		first = "Error: " + first
	}
	lines := []string{"  ⎿ " + first}
	switch more := len(kept) - 1; {
	case more == 1:
		lines = append(lines, "     … 1 more line")
	case more > 1:
		lines = append(lines, fmt.Sprintf("     … %d more lines", more))
	}
	return lines
}

// callSubject is what a tool was called on, as its input says it: its
// description, else the pattern, file, address, query or command it was
// given.
func callSubject(input json.RawMessage) string {
	var fields struct {
		Description string `json:"description"`
		Pattern     string `json:"pattern"`
		FilePath    string `json:"file_path"`
		Path        string `json:"path"`
		URL         string `json:"url"`
		Query       string `json:"query"`
		Command     string `json:"command"`
		Prompt      string `json:"prompt"`
	}
	_ = json.Unmarshal(input, &fields)
	for _, subject := range []string{fields.Description, fields.Pattern, fields.FilePath, fields.Path, fields.URL, fields.Query, fields.Command, fields.Prompt} {
		if line := firstLine(subject); line != "" {
			return line
		}
	}
	return ""
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
