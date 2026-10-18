// Package install wires the CFO into a machine so a CFO started from any
// folder is supervised, not just one opened inside the code-goblins checkout.
//
// Two things pinned the CFO to its own repo. The hooks lived in the repo's
// `.claude/settings.json` and resolved `$CLAUDE_PROJECT_DIR/cfo.exe`, so a
// session anywhere else tripped the `|| exit 0` guard and every hook went
// silently inert; and `cfo.exe` was only on PATH if the adopter put it there.
// Install sets `CFO_HOME` and PATH at user scope, and the hooks point at the
// home's own cfo.exe. They are the CFO's terminal's alone: its launch hands
// them to Claude Code (CFOSettings), and no settings file of the user holds
// them, so no other session on the machine runs one.
package install

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/guard"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
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
// defined. Adding a hook here is all it takes for the CFO's terminal to start
// with it, and for an install or `--uninstall` to remove one an older build
// wrote into the user's settings.
//
// Every pre-tool hook starts a process before its tool runs, so each tool
// call runs as few as the guards allow: a Bash call runs pretool-bash alone,
// which applies both shell guards, a PowerShell call runs pretool-powershell
// alone, which applies the same two as PowerShell writes a command, and
// pretool-subagent runs only for the tools its guard can refuse, never for
// the reading and editing tools a session spends its time in.
//
// The two fields carried verbatim from the repo-scoped wiring are
// SessionStart's 120s timeout and stop-autoarm's asyncRewake with its 8h
// timeout: the auto-arm hook is a resident watcher, not a one-shot.
func Hooks(root string) []Hook {
	hooks := []Hook{
		{Name: "session-start", Event: "SessionStart", Timeout: 120},
		{Name: "pre-compact", Event: "PreCompact"},
		{Name: "pretool-bash", Event: "PreToolUse", Matcher: "Bash"},
		{Name: "pretool-powershell", Event: "PreToolUse", Matcher: "PowerShell"},
		{Name: "pretool-subagent", Event: "PreToolUse", Matcher: guard.HookMatcher()},
		{Name: "turnend-guard", Event: "Stop"},
		{Name: "stop-autoarm", Event: "Stop", Timeout: 28800, AsyncRewake: true},
	}
	for i := range hooks {
		hooks[i].Command = filepath.Join(root, home.BinDir, "cfo.exe")
		hooks[i].Args = []string{"hook", hooks[i].Name}
	}
	return hooks
}

// NativeHooks are the native session hooks of a Claude Code session the
// fleet starts, of the home at root with its state in stateDir: each
// lifecycle event runs `cfo native-hook claude`, which reports the session to
// the supervisor as Codex's and Pi's native hooks report theirs. They ride
// the session's own settings rather than the user's, which every Claude Code
// session on the machine loads, the Overlord's own outside the fleet
// included.
func NativeHooks(root, stateDir string) []Hook {
	var hooks []Hook
	for _, event := range nativehook.Events("claude") {
		hooks = append(hooks, Hook{Name: "native-hook", Event: event, Command: filepath.Join(root, home.BinDir, "cfo.exe"), Args: []string{"native-hook", "claude", "--home", root, "--state", stateDir}, Timeout: 3})
	}
	return hooks
}

// CFOAutoCompactWindow is the window, in tokens, the CFO compacts in. Claude
// Code takes it as its autoCompactWindow setting, takes its 20K output
// reserve off it and compacts 13K short of what is left, or at the percent of
// that a CLAUDE_AUTOCOMPACT_PCT_OVERRIDE names where that is lower: at 342K
// under the Overlord's override of 90, 367K without one. On the model's own
// 1M window the CFO compacted at 882K, and a restart came first in its
// conversation of 2026-10-18. Measured on the CFO's 16 ended conversations of
// 2026-09-16 to 10-18: a compaction left it 68K to 96K tokens, and it grew by
// at most 88K in one request and 368K in one hour, so 342K leaves it at least
// 246K tokens between compactions, which no request fills: one compaction
// every 40 minutes in its busiest hour, and every two to seven hours at the
// pace of its days between compactions.
const CFOAutoCompactWindow = 400_000

// CFOSettings is the Claude Code settings document the CFO's terminal starts
// with: the CFO's hooks and its native session hooks, and its auto-compact
// window. Claude Code adds what `--settings` names to the user's own
// settings, for that session alone, and its window wins over one theirs set,
// /autocompact's included; only CLAUDE_CODE_AUTO_COMPACT_WINDOW in the
// environment beats it.
func CFOSettings(root, stateDir string) ([]byte, error) {
	file := &settingsFile{values: map[string]any{"autoCompactWindow": CFOAutoCompactWindow}}
	if err := file.addHooks(append(Hooks(root), NativeHooks(root, stateDir)...)); err != nil {
		return nil, err
	}
	return file.render()
}

// GoblinHooks are the hooks every Claude Code goblin starts with, of the
// home at root, and the single place they are defined. goblin-seal refuses
// the goblin's browser commands while a key fetch reads in its browser, and
// so runs only before a shell call. goblin-key-alarm wakes the CFO when a
// tool's output holds a secret key, and so runs after every tool call: a
// key read from a file is as exposed as one a command printed.
func GoblinHooks(root string) []Hook {
	hooks := []Hook{
		{Name: "goblin-seal", Event: "PreToolUse", Matcher: "Bash|PowerShell"},
		{Name: "goblin-key-alarm", Event: "PostToolUse", Matcher: "*"},
	}
	for i := range hooks {
		hooks[i].Command = filepath.Join(root, home.BinDir, "cfo.exe")
		hooks[i].Args = []string{"hook", hooks[i].Name}
	}
	return hooks
}

// GoblinSettings is the Claude Code settings document a goblin starts with:
// the goblin hooks and its native session hooks and nothing else, which
// Claude Code adds to the user's own settings for that session alone.
func GoblinSettings(root, stateDir string) ([]byte, error) {
	file := &settingsFile{values: map[string]any{}}
	if err := file.addHooks(append(GoblinHooks(root), NativeHooks(root, stateDir)...)); err != nil {
		return nil, err
	}
	return file.render()
}

// UserHooks is the `cfo hook <name>` of every CFO hook entry the settings file
// at path holds, which an install by a build before 2026-10 wrote there and
// every Claude Code session of the user runs. A missing file holds none.
func UserHooks(path string) ([]string, error) {
	file, err := loadSettings(path)
	if err != nil {
		return nil, err
	}
	events, err := file.hookEvents()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, raw := range events {
		groups, _ := raw.([]any)
		for _, rawGroup := range groups {
			group, _ := rawGroup.(map[string]any)
			entries, _ := group["hooks"].([]any)
			for _, rawEntry := range entries {
				if entry, ok := rawEntry.(map[string]any); ok {
					if name, ok := HookName(entry); ok {
						names = append(names, name)
					}
				}
			}
		}
	}
	slices.Sort(names)
	return names, nil
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

// hookGroups renders hooks as the settings-file groups they are written as:
// hooks sharing an event and matcher become one group, in the order listed.
func hookGroups(hooks []Hook) []hookGroup {
	groups := []hookGroup{}
	index := map[string]int{}
	for _, hook := range hooks {
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
