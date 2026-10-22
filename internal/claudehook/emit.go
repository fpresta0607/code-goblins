package claudehook

import (
	"encoding/json"
	"fmt"
	"io"
)

// denyEnvelope is the PreToolUse deny payload Claude Code expects on stderr.
// Its shape is fixed by upstream's hook contract:
// {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"},"systemMessage":"..."}
type denyEnvelope struct {
	HookSpecificOutput struct {
		HookEventName      string `json:"hookEventName"`
		PermissionDecision string `json:"permissionDecision"`
	} `json:"hookSpecificOutput"`
	SystemMessage string `json:"systemMessage"`
}

// stdoutDenyEnvelope is the PreToolUse deny payload Codex reads on stdout:
// {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"..."}}
type stdoutDenyEnvelope struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// infoEnvelope is the informational-allow payload: {"systemMessage":"..."}.
type infoEnvelope struct {
	SystemMessage string `json:"systemMessage"`
}

// DenyPreTool implements the PreToolUse deny outcome from upstream's hook
// exit-code contract: exit 2, empty stdout, and exactly one line of JSON on
// stderr carrying message as systemMessage. The envelope is built with
// json.Marshal over a struct, never string concatenation, so message is
// always correctly JSON-escaped regardless of quotes or embedded newlines.
func DenyPreTool(stderr io.Writer, message string) int {
	var env denyEnvelope
	env.HookSpecificOutput.HookEventName = "PreToolUse"
	env.HookSpecificOutput.PermissionDecision = "deny"
	env.SystemMessage = message

	data, err := json.Marshal(env)
	if err != nil {
		return 2
	}
	fmt.Fprintln(stderr, string(data))
	return 2
}

// DenyPreToolOnStdout is the PreToolUse deny outcome Codex reads: exit 0 and
// one line of JSON on stdout carrying message as permissionDecisionReason.
// Codex runs a hook's command in the session's Windows PowerShell, which
// ends with 1 for every exit code but 0, so DenyPreTool's exit 2 never
// reaches it. The goblin seal's pi extension reads the same line.
func DenyPreToolOnStdout(stdout io.Writer, message string) int {
	var env stdoutDenyEnvelope
	env.HookSpecificOutput.HookEventName = "PreToolUse"
	env.HookSpecificOutput.PermissionDecision = "deny"
	env.HookSpecificOutput.PermissionDecisionReason = message

	data, err := json.Marshal(env)
	if err != nil {
		return 0
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

// BlockStop implements the Stop block/rewake outcome from upstream's hook
// exit-code contract: exit 2 with the plain-text banner written verbatim to
// stderr and empty stdout.
func BlockStop(stderr io.Writer, banner string) int {
	fmt.Fprintln(stderr, banner)
	return 2
}

// InfoAllow implements the informational-allow outcome from upstream's hook
// exit-code contract: exit 0 with a stdout JSON object carrying message as
// systemMessage.
func InfoAllow(stdout io.Writer, message string) int {
	env := infoEnvelope{SystemMessage: message}
	data, err := json.Marshal(env)
	if err != nil {
		return 0
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}
