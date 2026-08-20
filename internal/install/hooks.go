// Package install wires the CFO into a machine so a Claude Code session
// opened in any repository is supervised, not just one opened inside the
// code-goblins checkout.
//
// Two things pin the CFO to its own repo. The hooks live in the repo's
// `.claude/settings.json` and resolve `$CLAUDE_PROJECT_DIR/cfo.exe`, so a
// session anywhere else trips the `|| exit 0` guard and every hook goes
// silently inert; and `cfo.exe` is only on PATH if the adopter put it there.
// Install moves the hooks to user scope, points them at `$CFO_HOME`, and
// sets `CFO_HOME` and PATH at user scope.
package install

import (
	"encoding/json"
	"strconv"
)

// rootPrefix opens every hook command this package writes. It is also the
// marker that makes a hook entry ours: an uninstall and the idempotence
// check both identify CFO hooks by this prefix, so nothing else in an
// adopter's settings can be mistaken for one and removed.
//
// `${CFO_HOME:-$CLAUDE_PROJECT_DIR}` keeps today's behavior for an adopter
// who has cloned the repo but not run install yet: inside the checkout,
// `$CLAUDE_PROJECT_DIR` still resolves the binary.
const rootPrefix = `CFO_ROOT="${CFO_HOME:-$CLAUDE_PROJECT_DIR}"; `

// Hook is one registered Claude Code hook entry: where it is registered and
// the exact command string a session runs.
type Hook struct {
	// Name is the `cfo hook <name>` this entry invokes.
	Name string
	// Event is the Claude Code hook event, and Matcher the tool pattern it
	// is registered under (empty for events that take no matcher).
	Event   string
	Matcher string
	// Command is the shell command Claude Code runs.
	Command string
	// Timeout is the entry's timeout in seconds; zero means unset.
	Timeout int
	// AsyncRewake keeps a Stop hook alive in the background across the
	// CFO's idle time.
	AsyncRewake bool
}

// Hooks is the CFO hook set, and the single place it is defined. Adding a
// hook here is all it takes for `cfo install` to write it, for a rerun to
// leave it alone, and for `--uninstall` to remove it.
//
// The two fields carried verbatim from the repo-scoped wiring are
// SessionStart's 120s timeout and stop-autoarm's asyncRewake with its 8h
// timeout: the auto-arm hook is a resident watcher, not a one-shot.
func Hooks() []Hook {
	hooks := []Hook{
		{Name: "session-start", Event: "SessionStart", Timeout: 120},
		{Name: "pretool-arm", Event: "PreToolUse", Matcher: "Bash"},
		{Name: "pretool-cd", Event: "PreToolUse", Matcher: "Bash"},
		{Name: "pretool-subagent", Event: "PreToolUse", Matcher: ".*"},
		{Name: "turnend-guard", Event: "Stop"},
		{Name: "stop-autoarm", Event: "Stop", Timeout: 28800, AsyncRewake: true},
	}
	for i := range hooks {
		hooks[i].Command = hookCommand(hooks[i].Name)
	}
	return hooks
}

// hookCommand renders the shell command Claude Code runs for one hook. The
// executable test is kept from the repo-scoped form: a machine without a
// built binary must leave the tool call alone rather than fail it.
func hookCommand(name string) string {
	return rootPrefix + `[ -x "$CFO_ROOT"/cfo.exe ] || exit 0; "$CFO_ROOT"/cfo.exe hook ` + name
}

// hookGroup is one matcher group inside one Claude Code hook event.
type hookGroup struct {
	event   string
	matcher string
	entries []map[string]any
}

// cfoHookGroups renders Hooks as the settings-file groups they are written
// as: hooks sharing an event and matcher become one group, in the order
// Hooks lists them.
func cfoHookGroups() []hookGroup {
	groups := []hookGroup{}
	index := map[string]int{}
	for _, hook := range Hooks() {
		entry := map[string]any{"type": "command", "command": hook.Command}
		if hook.Timeout > 0 {
			entry["timeout"] = json.Number(strconv.Itoa(hook.Timeout))
		}
		if hook.AsyncRewake {
			entry["asyncRewake"] = true
		}
		key := hook.Event + "\x00" + hook.Matcher
		at, seen := index[key]
		if !seen {
			groups = append(groups, hookGroup{event: hook.Event, matcher: hook.Matcher})
			at = len(groups) - 1
			index[key] = at
		}
		groups[at].entries = append(groups[at].entries, entry)
	}
	return groups
}
