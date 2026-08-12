// Package claudehook is the transport layer every `cfo hook <name>`
// subcommand builds on: it decodes Claude Code's hook JSON from stdin and
// owns the three hook output contracts (PreToolUse deny, Stop block,
// informational allow).
package claudehook

import (
	"encoding/json"
	"io"
)

// Payload is the decoded form of the hook JSON Claude Code writes to a hook
// subcommand's stdin. It ports upstream's jq-based field extraction across
// the PreToolUse, SessionStart, and Stop hook events into one shared shape.
type Payload struct {
	SessionID      string
	Source         string
	ToolName       string
	Command        string
	StopHookActive bool
}

// rawPayload mirrors Claude's hook JSON field names before the stop-hook
// two-spelling merge. ToolInput stays raw for a second, targeted unmarshal
// since it is the only nested field callers need (tool_input.command).
type rawPayload struct {
	SessionID           string          `json:"session_id"`
	Source              string          `json:"source"`
	ToolName            string          `json:"tool_name"`
	ToolInput           json.RawMessage `json:"tool_input"`
	StopHookActive      *bool           `json:"stopHookActive"`
	StopHookActiveSnake *bool           `json:"stop_hook_active"`
}

// toolInput extracts tool_input.command, the only tool_input field any hook
// consumes.
type toolInput struct {
	Command string `json:"command"`
}

// ReadPayload decodes Claude Code's hook JSON from r. Upstream's stop hooks
// carry the "did Claude already stop" flag under two spellings across Claude
// Code versions, camelCase stopHookActive and snake_case stop_hook_active;
// pointer fields distinguish "absent" from "present and false" so camelCase
// can be consulted first and the snake_case value used only as a fallback
// when camelCase is entirely absent.
//
// It reports ok=false on empty input, an unreadable stream, or JSON that is
// not an object. These are transport failures, not payload errors: they fail
// open so a hook subcommand exits 0 silently rather than erroring on
// malformed or missing stdin.
func ReadPayload(r io.Reader) (Payload, bool) {
	data, err := io.ReadAll(r)
	if err != nil || len(data) == 0 {
		return Payload{}, false
	}

	var raw rawPayload
	if err := json.Unmarshal(data, &raw); err != nil {
		return Payload{}, false
	}

	p := Payload{
		SessionID: raw.SessionID,
		Source:    raw.Source,
		ToolName:  raw.ToolName,
	}

	if len(raw.ToolInput) > 0 {
		var ti toolInput
		if err := json.Unmarshal(raw.ToolInput, &ti); err == nil {
			p.Command = ti.Command
		}
	}

	switch {
	case raw.StopHookActive != nil:
		p.StopHookActive = *raw.StopHookActive
	case raw.StopHookActiveSnake != nil:
		p.StopHookActive = *raw.StopHookActiveSnake
	}

	return p, true
}
