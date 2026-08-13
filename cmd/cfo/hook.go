package main

import (
	"fmt"
	"io"
	"os"

	"github.com/fpresta0607/code-goblins/internal/claudehook"
	"github.com/fpresta0607/code-goblins/internal/guard"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// runHook is the single dispatcher every `cfo hook <name>` case routes
// through. An unknown hook name is not a caller error: a future Claude Code
// version invoking a hook name this build does not know about must not
// break the tool call, so it exits 0 with a one-line stderr diagnostic
// instead of denying or failing the request.
func runHook(name string, stdin io.Reader, stdout, stderr io.Writer) int {
	switch name {
	case "pretool-subagent":
		return hookPretoolSubagent(stdin, stdout, stderr)
	case "pretool-arm":
		return hookPretoolArm(stdin, stdout, stderr)
	case "pretool-cd":
		return hookPretoolCd(stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "cfo hook: unknown hook %q\n", name)
		return 0
	}
}

// hookPretoolSubagent stops the CFO primary from delegating through the
// harness's own tools (Agent, Task, SendMessage, and the rest) instead of
// the fleet dispatch path: work started that way has no durable fleet
// record and dies with the session. Every early exit fails open (exit 0,
// silent) so the guard stays inert outside a genuine primary fleet home.
func hookPretoolSubagent(stdin io.Reader, stdout, stderr io.Writer) int {
	payload, ok := claudehook.ReadPayload(stdin)
	if !ok {
		return 0
	}
	h, err := home.Resolve()
	if err != nil {
		return 0
	}
	if !home.IsPrimary(h) {
		return 0
	}
	if os.Getenv("CFO_ALLOW_SUBAGENT") == "1" {
		return 0
	}
	stem, deny := guard.ClassifySubagent(payload.ToolName)
	if !deny {
		return 0
	}
	message := fmt.Sprintf(
		"[subagent-dispatch] the CFO primary dispatches through the fleet, not the harness's own delegation tools: work started that way has no durable fleet record and dies with this session. Use the fleet dispatch path once Plan 3 lands it (blocked tool: %s, delegation-shaped on %q). Launch the session with CFO_ALLOW_SUBAGENT=1 for a deliberate exception.",
		payload.ToolName, stem,
	)
	return claudehook.DenyPreTool(stderr, message)
}

// hookPretoolArm stops the agent shell from invoking the watcher directly:
// the watcher is supposed to be armed by the Stop-owned auto-arm hook, and
// running it (or killing it, backgrounding it, piping it, and the rest)
// from a Bash call bypasses that supervision. Every early exit fails open
// (exit 0, silent).
func hookPretoolArm(stdin io.Reader, stdout, stderr io.Writer) int {
	payload, ok := claudehook.ReadPayload(stdin)
	if !ok {
		return 0
	}
	h, err := home.Resolve()
	if err != nil {
		return 0
	}
	if !home.IsPrimary(h) {
		return 0
	}
	code, reason, deny := guard.ClassifyArm(payload.Command)
	if !deny {
		return 0
	}
	return claudehook.DenyPreTool(stderr, fmt.Sprintf("[%s] %s", code, reason))
}

// hookPretoolCd stops the agent shell from relocating its working directory:
// Claude Code's Bash tool keeps its working directory across calls, so a
// relocation anywhere in the command outlives the tool call. Upstream's
// cd-guard predicate is looser than IsPrimary; it is deliberately tightened
// to IsPrimary here, same as pretool-arm, so both guards share the
// inert-in-dev guarantee. This is a sanctioned deviation from upstream.
// Every early exit fails open (exit 0, silent).
func hookPretoolCd(stdin io.Reader, stdout, stderr io.Writer) int {
	payload, ok := claudehook.ReadPayload(stdin)
	if !ok {
		return 0
	}
	h, err := home.Resolve()
	if err != nil {
		return 0
	}
	if !home.IsPrimary(h) {
		return 0
	}
	code, reason, deny := guard.ClassifyCd(payload.Command)
	if !deny {
		return 0
	}
	return claudehook.DenyPreTool(stderr, fmt.Sprintf("[%s] %s", code, reason))
}
