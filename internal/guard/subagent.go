// Package guard classifies Claude Code tool calls the primary agent must
// not be allowed to make. The first guard is pretool-subagent: it stops the
// CFO primary from delegating through the harness's own tools (Agent, Task,
// SendMessage, and the rest) instead of the fleet dispatch path.
package guard

import "strings"

// observeOnlyTools and planOnlyTools are exact-name allowlists checked
// before the delegation-stem substring match. Observe-only tools only read
// fleet or shell state; plan-only tools only enqueue future work; neither
// hands work outside the fleet the way Agent, Task, or SendMessage do.
// Ported verbatim from upstream and kept as inline constants.
var observeOnlyTools = map[string]bool{
	"taskoutput": true,
	"taskstop":   true,
	"taskget":    true,
	"tasklist":   true,
	"cronlist":   true,
	"bashoutput": true,
	"killshell":  true,
}

var planOnlyTools = map[string]bool{
	"taskcreate": true,
	"taskupdate": true,
}

// delegationStems are substrings that mark a tool name as delegation-shaped:
// it lets the primary hand work to something other than the fleet dispatch
// path. Ported verbatim from upstream, in match-priority order.
var delegationStems = []string{
	"agent", "subagent", "task", "workflow", "cron", "schedul", "worktree",
	"delegate", "spawn", "dispatch", "handoff", "remote", "sendmessage", "monitor",
}

// ClassifySubagent decides whether tool is a harness-native delegation tool
// the pretool-subagent guard must block. MCP tools (mcp__ prefix) never
// deny. Otherwise the name is lowercased and stripped to [a-z0-9], checked
// against the two exact-name allowlists (which pass first), then checked
// for a delegation stem as a substring; the first matching stem is returned
// alongside deny=true.
func ClassifySubagent(tool string) (stem string, deny bool) {
	if strings.HasPrefix(tool, "mcp__") {
		return "", false
	}

	var b strings.Builder
	for _, r := range strings.ToLower(tool) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	name := b.String()

	if observeOnlyTools[name] || planOnlyTools[name] {
		return "", false
	}

	for _, s := range delegationStems {
		if strings.Contains(name, s) {
			return s, true
		}
	}
	return "", false
}
