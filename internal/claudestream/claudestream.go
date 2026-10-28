// Package claudestream speaks Claude Code's stream-json protocol, the one its
// own VS Code extension runs on: the flags and environment that run a session
// on it, the lines the board writes into it, and a Session that folds the
// lines Claude Code writes into what the chat shows while a turn runs. The
// conversation itself stays in the transcript Claude Code writes, which the
// board reads as it does for a terminal.
package claudestream

import (
	"encoding/json"
	"strings"
)

// Arguments are Claude Code's flags for a session on its stream: it reads his
// lines and the board's control requests as JSON lines, writes each event as
// one, word by word, echoes each of his lines once it takes it, and asks the
// board before it runs a tool. A resumed session takes up the conversation
// its id names, as the terminal's --resume does.
func Arguments(sessionID string, isResumed bool) []string {
	session := "--session-id"
	if isResumed {
		session = "--resume"
	}
	return []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--replay-user-messages", "--permission-prompt-tool", "stdio", session, sessionID}
}

// apiKeyVariable is the key Claude Code bills instead of his plan: with -p it
// uses the key whenever one is set, where the terminal asks first.
const apiKeyVariable = "ANTHROPIC_API_KEY"

// Environment is env without apiKeyVariable, so a stream session runs on his
// plan login as his terminal does. Windows reads names in any case.
func Environment(env []string) []string {
	var kept []string
	for _, entry := range env {
		if !strings.HasPrefix(strings.ToUpper(entry), apiKeyVariable+"=") {
			kept = append(kept, entry)
		}
	}
	return kept
}

// UserLine is his words as one line for Claude Code, exactly as he wrote
// them. Written mid-turn, Claude Code takes it at its next step.
func UserLine(text string) json.RawMessage {
	line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]string{"role": "user", "content": text}, "parent_tool_use_id": nil, "session_id": ""})
	return line
}

// RequestLine is a control request: subtype names it (interrupt, set_model,
// set_permission_mode, get_context_usage), fields are its other members, and
// Claude Code's control_response carries requestID back.
func RequestLine(requestID, subtype string, fields map[string]string) json.RawMessage {
	request := map[string]string{"subtype": subtype}
	for name, value := range fields {
		request[name] = value
	}
	line, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": requestID, "request": request})
	return line
}

// AllowLine answers the can_use_tool request requestID: the tool runs with
// input, the input it asked with or one the board changed, as an
// AskUserQuestion's input carries his answers.
func AllowLine(requestID string, input json.RawMessage) (json.RawMessage, error) {
	return answerLine(requestID, map[string]any{"behavior": "allow", "updatedInput": input})
}

// DenyLine answers the can_use_tool request requestID: the tool does not run,
// and Claude Code reads why.
func DenyLine(requestID, why string) json.RawMessage {
	line, _ := answerLine(requestID, map[string]any{"behavior": "deny", "message": why})
	return line
}

func answerLine(requestID string, response map[string]any) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": requestID, "response": response}})
}
