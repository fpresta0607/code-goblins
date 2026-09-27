// Package install wires the CFO into a machine so a Claude Code session
// opened in any repository is supervised, not just one opened inside the
// code-goblins checkout.
//
// Two things pin the CFO to its own repo. The hooks live in the repo's
// `.claude/settings.json` and resolve `$CLAUDE_PROJECT_DIR/cfo.exe`, so a
// session anywhere else trips the `|| exit 0` guard and every hook goes
// silently inert; and `cfo.exe` is only on PATH if the adopter put it there.
// Install moves the hooks to user scope, points them at the home's own
// cfo.exe, and sets `CFO_HOME` and PATH at user scope.
package install

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/guard"
)

// shellFormPrefix opened every hook command installs wrote before the hooks
// ran without a shell. An install or uninstall still recognises an entry by
// it, so a machine installed then has those entries replaced or removed
// rather than left running beside the new ones.
const shellFormPrefix = `CFO_ROOT="${CFO_HOME:-$CLAUDE_PROJECT_DIR}"; `

// Hook is one registered Claude Code hook entry: where it is registered and
// what a session runs.
type Hook struct {
	// Name is the `cfo hook <name>` this entry invokes.
	Name string
	// Event is the Claude Code hook event, and Matcher the tool pattern it
	// is registered under (empty for events that take no matcher).
	Event   string
	Matcher string
	// Command is the home's cfo.exe and Args the arguments it runs with.
	// Claude Code starts a hook that has args directly, with no shell: a
	// shell command costs each run Git Bash's launcher and bash before
	// cfo.exe starts at all.
	Command string
	Args    []string
	// Timeout is the entry's timeout in seconds; zero means unset.
	Timeout int
	// AsyncRewake keeps a Stop hook alive in the background across the
	// CFO's idle time.
	AsyncRewake bool
}

// Hooks is the CFO hook set of the home at root, and the single place it is
// defined. Adding a hook here is all it takes for `cfo install` to write it,
// for a rerun to leave it alone, and for `--uninstall` to remove it.
//
// Every pre-tool hook starts a process before its tool runs, so each tool
// call runs as few as the guards allow: a Bash call runs pretool-bash alone,
// which applies both Bash guards, and pretool-subagent runs only for the
// tools its guard can refuse, never for the reading and editing tools a
// session spends its time in.
//
// The two fields carried verbatim from the repo-scoped wiring are
// SessionStart's 120s timeout and stop-autoarm's asyncRewake with its 8h
// timeout: the auto-arm hook is a resident watcher, not a one-shot.
func Hooks(root string) []Hook {
	hooks := []Hook{
		{Name: "session-start", Event: "SessionStart", Timeout: 120},
		{Name: "pretool-bash", Event: "PreToolUse", Matcher: "Bash"},
		{Name: "pretool-subagent", Event: "PreToolUse", Matcher: guard.HookMatcher()},
		{Name: "turnend-guard", Event: "Stop"},
		{Name: "stop-autoarm", Event: "Stop", Timeout: 28800, AsyncRewake: true},
	}
	for i := range hooks {
		hooks[i].Command = filepath.Join(root, "cfo.exe")
		hooks[i].Args = []string{"hook", hooks[i].Name}
	}
	return hooks
}

// HookName is the `cfo hook <name>` a settings hook entry runs, for an entry
// in the form install writes (the home's cfo.exe with args hook and a name)
// or in the shell form it wrote before; ok is false for any other entry.
func HookName(entry map[string]any) (name string, ok bool) {
	command, _ := entry["command"].(string)
	if rest, shell := strings.CutPrefix(command, shellFormPrefix); shell {
		_, name, found := strings.Cut(rest, "cfo.exe hook ")
		return name, found
	}
	args, _ := entry["args"].([]any)
	if len(args) != 2 || args[0] != "hook" || !strings.EqualFold(filepath.Base(command), "cfo.exe") {
		return "", false
	}
	name, ok = args[1].(string)
	return name, ok
}

// hookGroup is one matcher group inside one Claude Code hook event.
type hookGroup struct {
	event   string
	matcher string
	entries []map[string]any
}

// cfoHookGroups renders the hooks of the home at root as the settings-file
// groups they are written as: hooks sharing an event and matcher become one
// group, in the order Hooks lists them.
func cfoHookGroups(root string) []hookGroup {
	groups := []hookGroup{}
	index := map[string]int{}
	for _, hook := range Hooks(root) {
		args := make([]any, 0, len(hook.Args))
		for _, arg := range hook.Args {
			args = append(args, arg)
		}
		entry := map[string]any{"type": "command", "command": hook.Command, "args": args}
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
