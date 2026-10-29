package claudestream

import (
	"encoding/json"
	"fmt"
	"slices"
)

// Session is what a stream session's lines say while it runs: what runs it,
// whether a turn is working, the reply being written, the tool calls waiting
// for an answer, how the last turn ended, and what the reader did not know.
type Session struct {
	ID             string
	Model          string
	PermissionMode string
	Version        string
	Capabilities   []string
	// IsWorking is set from the moment Claude Code takes one of his lines,
	// which it echoes then, to the result that ends the turn.
	IsWorking bool
	// Live is the text and thinking of the reply being written, each block
	// until Claude Code writes it whole.
	Live []LiveBlock
	// Asks are the tool calls waiting for an answer, oldest first.
	Asks []Ask
	// Result is how the last turn ended.
	Result *Result
	// Unknown names, once each, the kinds of line Claude Code wrote that the
	// reader does not know. A request among them waits for an answer the
	// board cannot give.
	Unknown []string
	// IsExited is set once Claude Code ended, with its ExitCode.
	IsExited bool
	ExitCode uint32
}

// Exit says Claude Code ended with code: a turn it left, its words and its
// asks end with it.
func (s *Session) Exit(code uint32) {
	s.IsExited, s.ExitCode = true, code
	s.IsWorking, s.Live, s.Asks = false, nil, nil
}

// LiveBlock is a block of the reply being written: its message, its place in
// it, its kind, text or thinking, and its words so far.
type LiveBlock struct {
	MessageID string
	Index     int
	Kind      string
	Text      string
}

// Ask is a tool call Claude Code asks the board to allow, answered by
// AllowLine or DenyLine with its RequestID. An AskUserQuestion's Input holds
// his questions and their options.
type Ask struct {
	RequestID   string
	Tool        string
	ToolUseID   string
	Description string
	Input       json.RawMessage
}

// Result is how a turn ended, success or the kind of error, and what it
// spent.
type Result struct {
	Subtype    string
	IsError    bool
	StopReason string
	Text       string
	CostUSD    float64
	Usage      Usage
}

// Usage is the tokens a turn read and wrote.
type Usage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
}

// streamLine is the members of every kind of line the reader reads.
type streamLine struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	SessionID       string          `json:"session_id"`
	Model           string          `json:"model"`
	PermissionMode  string          `json:"permissionMode"`
	Version         string          `json:"claude_code_version"`
	Capabilities    []string        `json:"capabilities"`
	IsReplay        bool            `json:"isReplay"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	Message         json.RawMessage `json:"message"`
	APIMessageID    string          `json:"api_message_id"`
	Event           struct {
		Type         string `json:"type"`
		Index        int    `json:"index"`
		ContentBlock struct {
			Type string `json:"type"`
		} `json:"content_block"`
		Delta struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
	} `json:"event"`
	RequestID string `json:"request_id"`
	Request   struct {
		Subtype     string          `json:"subtype"`
		ToolName    string          `json:"tool_name"`
		ToolUseID   string          `json:"tool_use_id"`
		Description string          `json:"description"`
		Input       json.RawMessage `json:"input"`
	} `json:"request"`
	Response struct {
		RequestID string `json:"request_id"`
	} `json:"response"`
	IsError    bool    `json:"is_error"`
	StopReason string  `json:"stop_reason"`
	Text       string  `json:"result"`
	CostUSD    float64 `json:"total_cost_usd"`
	Usage      Usage   `json:"usage"`
}

// Apply folds one line of the session into what it says: a line Claude Code
// wrote, or with isWritten one the board wrote to it.
func (s *Session) Apply(line json.RawMessage, isWritten bool) error {
	var read streamLine
	if err := json.Unmarshal(line, &read); err != nil {
		return fmt.Errorf("claudestream: read a %d-byte line: %w", len(line), err)
	}
	if isWritten {
		// The board's own lines say only which ask it answered.
		if read.Type == "control_response" {
			s.answered(read.Response.RequestID)
		}
		return nil
	}
	isMain := read.ParentToolUseID == nil
	switch read.Type {
	case "system":
		// Hooks, status, background tasks and any later notice are the
		// transcript's or nobody's.
		if read.Subtype == "init" {
			s.ID, s.Model, s.PermissionMode, s.Version, s.Capabilities = read.SessionID, read.Model, read.PermissionMode, read.Version, read.Capabilities
		}
	case "user":
		// His line echoed is the moment Claude Code took it. Tool results
		// and notices are the transcript's.
		if read.IsReplay {
			s.IsWorking = true
		}
	case "stream_event":
		if isMain {
			s.write(read)
		}
	case "assistant":
		if isMain {
			return s.written(read.Message)
		}
	case "control_request":
		if read.Request.Subtype != "can_use_tool" {
			s.unknown("control_request " + read.Request.Subtype)
			return nil
		}
		s.Asks = append(s.Asks, Ask{RequestID: read.RequestID, Tool: read.Request.ToolName, ToolUseID: read.Request.ToolUseID, Description: read.Request.Description, Input: read.Request.Input})
	case "control_cancel_request":
		s.answered(read.RequestID)
	case "result":
		s.IsWorking, s.Live, s.Asks = false, nil, nil
		s.Result = &Result{Subtype: read.Subtype, IsError: read.IsError, StopReason: read.StopReason, Text: read.Text, CostUSD: read.CostUSD, Usage: read.Usage}
	case "control_response", "tool_progress", "rate_limit_event":
		// Answers to the board's own requests, a running tool's heartbeat and
		// the plan's allowance say nothing the chat shows here.
	default:
		s.unknown(read.Type)
	}
	return nil
}

// write adds a reply's words as they come.
func (s *Session) write(read streamLine) {
	event := read.Event
	switch {
	case event.Type == "content_block_start" && (event.ContentBlock.Type == "text" || event.ContentBlock.Type == "thinking"):
		s.Live = append(s.Live, LiveBlock{MessageID: read.APIMessageID, Index: event.Index, Kind: event.ContentBlock.Type})
	case event.Type == "content_block_delta" && event.Delta.Type == "text_delta":
		s.block(read.APIMessageID, event.Index, "text").Text += event.Delta.Text
	case event.Type == "content_block_delta" && event.Delta.Type == "thinking_delta":
		s.block(read.APIMessageID, event.Index, "thinking").Text += event.Delta.Thinking
	}
}

// block is the live block of message at index, added if its start was not
// read.
func (s *Session) block(messageID string, index int, kind string) *LiveBlock {
	at := slices.IndexFunc(s.Live, func(block LiveBlock) bool { return block.MessageID == messageID && block.Index == index })
	if at < 0 {
		s.Live = append(s.Live, LiveBlock{MessageID: messageID, Index: index, Kind: kind})
		at = len(s.Live) - 1
	}
	return &s.Live[at]
}

// written drops the live blocks Claude Code wrote whole: it writes a reply's
// blocks in order, one a line, so each of a kind is that message's first live
// block of that kind.
func (s *Session) written(message json.RawMessage) error {
	var reply struct {
		ID      string `json:"id"`
		Content []struct {
			Type string `json:"type"`
		} `json:"content"`
	}
	if err := json.Unmarshal(message, &reply); err != nil {
		return fmt.Errorf("claudestream: read a reply: %w", err)
	}
	for _, content := range reply.Content {
		at := slices.IndexFunc(s.Live, func(block LiveBlock) bool { return block.MessageID == reply.ID && block.Kind == content.Type })
		if at >= 0 {
			s.Live = slices.Delete(s.Live, at, at+1)
		}
	}
	return nil
}

func (s *Session) answered(requestID string) {
	s.Asks = slices.DeleteFunc(s.Asks, func(ask Ask) bool { return ask.RequestID == requestID })
}

func (s *Session) unknown(kind string) {
	if !slices.Contains(s.Unknown, kind) {
		s.Unknown = append(s.Unknown, kind)
	}
}
