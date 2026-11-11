// Package install wires the CEO into a machine so a CEO started from any
// folder is supervised, not just one opened inside the code-goblins checkout.
//
// Two things pinned the CEO to its own repo. The hooks lived in the repo's
// `.claude/settings.json` and resolved `$CLAUDE_PROJECT_DIR/goblins.exe`, so a
// session anywhere else tripped the `|| exit 0` guard and every hook went
// silently inert; and `goblins.exe` was only on PATH if the adopter put it there.
// Install sets `GOBLINS_HOME` and PATH at user scope, and the hooks point at the
// home's own goblins.exe. They are the CEO's terminal's alone: its launch hands
// them to Claude Code (CEOSettings), and no settings file of the user holds
// them, so no other session on the machine runs one.
package install

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/guard"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

// shellFormPrefix opened every hook command installs wrote before the hooks
// ran without a shell. An install or uninstall still recognises an entry by
// it, so a machine installed then has those entries replaced or removed
// rather than left running beside the new ones.
const shellFormPrefix = `GOBLINS_ROOT="${GOBLINS_HOME:-$CLAUDE_PROJECT_DIR}"; `

// Hook is one registered Claude Code hook entry: where it is registered and
// what a session runs.
type Hook struct {
	// Name is the `goblins hook <name>` this entry invokes.
	Name string
	// Event is the Claude Code hook event, and Matcher the tool pattern it
	// is registered under (empty for events that take no matcher).
	Event   string
	Matcher string
	// Command is the home's goblins.exe and Args the arguments it runs with.
	// Claude Code starts a hook that has args directly, with no shell: a
	// shell command costs each run Git Bash's launcher and bash before
	// goblins.exe starts at all.
	Command string
	Args    []string
	// Timeout is the entry's timeout in seconds; zero means unset.
	Timeout int
	// AsyncRewake keeps a Stop hook alive in the background across the
	// CEO's idle time.
	AsyncRewake bool
	// URL, when set, makes the entry an HTTP hook Claude Code posts to the
	// board at, starting no process, in place of Command and Args. It
	// carries HookVariables from the session's environment in their
	// headers, and ShouldBlockOnFailure refuses the tool call when the board
	// does not answer, as a seal that cannot be read refuses.
	URL                  string
	ShouldBlockOnFailure bool
}

// The board serves a goblin's hooks that run at every tool call at these
// paths under GoblinHookPrefix: the seal before a command and the native
// hook, with the key alarm, after every tool call.
const (
	GoblinHookPrefix = "/api/goblin-hook/"
	GoblinSealPath   = GoblinHookPrefix + "seal"
	GoblinToolPath   = GoblinHookPrefix + "tool"
)

// HookVariables are the variables of a goblin's environment its hooks read.
// A command hook inherits them; an HTTP hook carries each in the header
// HookHeader names, and the board reads them there. The terminal's id and
// proof also prove to the board that the request came from that terminal.
var HookVariables = []string{harness.RoleVariable, "GOBLINS_TASK_ID", "GOBLINS_SPAWN_GEN", "GOBLINS_PARENT_SESSION_ID", "GOBLINS_PARENT_HARNESS", "GOBLINS_ROOT_SESSION_ID", host.IDVariable, host.ProofVariable}

// HookHeader is the header an HTTP hook carries variable in.
func HookHeader(variable string) string {
	return "X-" + strings.ReplaceAll(variable, "_", "-")
}

// Hooks is the CEO hook set of the home at root, and the single place it is
// defined. Adding a hook here is all it takes for the CEO's terminal to start
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
		hooks[i].Command = filepath.Join(root, home.BinDir, "goblins.exe")
		hooks[i].Args = []string{"hook", hooks[i].Name}
	}
	return hooks
}

// NativeHooks are the native session hooks of a Claude Code session the
// fleet starts, of the home at root with its state in stateDir: each
// lifecycle event runs `goblins native-hook claude`, which reports the session to
// the supervisor as Codex's and Pi's native hooks report theirs. They ride
// the session's own settings rather than the user's, which every Claude Code
// session on the machine loads, the Overlord's own outside the fleet
// included.
func NativeHooks(root, stateDir string) []Hook {
	var hooks []Hook
	for _, event := range nativehook.Events("claude") {
		hooks = append(hooks, Hook{Name: "native-hook", Event: event, Command: filepath.Join(root, home.BinDir, "goblins.exe"), Args: []string{"native-hook", "claude", "--home", root, "--state", stateDir}, Timeout: nativehook.TimeoutSeconds})
	}
	return hooks
}

// KeyAlarmSeconds is how long Claude Code lets the key alarm and a goblin's
// seal run, its default for a command hook, which each ran under as a
// command hook of its own.
const KeyAlarmSeconds = 600

// AutoCompactWindow is the window, in tokens, the CEO and every Claude Code
// goblin compact in. Claude Code takes it as its autoCompactWindow setting,
// takes its 20K output reserve off it and compacts 13K short of what is left,
// or at the percent of that a CLAUDE_AUTOCOMPACT_PCT_OVERRIDE names where
// that is lower: at 342K under the Overlord's override of 90, 367K without
// one. On the model's own 1M window the CEO compacted at 882K, and a restart
// came first in its conversation of 2026-10-18. Measured on the CEO's 16
// ended conversations of 2026-09-16 to 10-18: a compaction left it 68K to 96K
// tokens, and it grew by at most 88K in one request and 368K in one hour, so
// 342K leaves it at least 246K tokens between compactions, which no request
// fills: one compaction every 40 minutes in its busiest hour, and every two
// to seven hours at the pace of its days between compactions.
//
// Every request reads the whole conversation again, so what a goblin spends
// grows with the context it carries. Measured on 2026-10-27 over the week
// before, Claude Code goblins made 36,450 requests at a median of 302K tokens
// of context and up to 882K, and reading that context again was 83 percent
// of what they spent; compacting at this window would have cut their spend
// by about a third.
const AutoCompactWindow = 400_000

// CEOSettings is the Claude Code settings document the CEO's terminal starts
// with: the CEO's hooks and its native session hooks, its auto-compact
// window, and no prompt suggestions. Claude Code adds what `--settings` names
// to the user's own settings, for that session alone, and its window wins
// over one theirs set, /autocompact's included; only
// CLAUDE_CODE_AUTO_COMPACT_WINDOW in the environment beats it. A suggestion
// is drawn on the CEO's empty input line as each turn ends, where the board
// reads it as words left unsent and so typed none of his messages until the
// CEO worked again, as a goblin's, started without them, never does.
func CEOSettings(root, stateDir string) ([]byte, error) {
	file := &settingsFile{values: map[string]any{"autoCompactWindow": AutoCompactWindow, "promptSuggestionEnabled": false}}
	if err := file.addHooks(append(Hooks(root), NativeHooks(root, stateDir)...)); err != nil {
		return nil, err
	}
	return file.render()
}

// GoblinSealMatcher names the tools a Claude Code goblin's seal runs
// before: those that run a command and those that read or write a file by
// its path.
const GoblinSealMatcher = "Bash|PowerShell|Monitor|Read|Write|Edit|MultiEdit|NotebookEdit|Glob|Grep"

// GoblinHooks are every hook a Claude Code goblin of the home at root, with
// its state in stateDir, starts with, and the single place they are defined.
// goblin-seal refuses the goblin's browser commands while a key fetch reads
// in its browser, and every command while its worktree's Python
// environments hold code that runs at every Python start that no known
// package ships, which it checks only before a call that runs a command: Bash,
// PowerShell, and Monitor, which watches one in the background. It runs before
// the file tools too, GoblinSealMatcher, since every call it is shown is held
// to the task's path boundary (internal/pathguard). Then come its native
// session hooks, and the one after every tool call also raises the key
// alarm, which wakes the CEO when the tool's output holds a secret key: a key
// read from a file is as exposed as one a command printed.
//
// The two that run at every tool call post to the board at board, the
// supervisor's address, which does their work in its own process: each was
// a goblins process of its own, 157 of the 940 processes one goblin started in
// 11 minutes on 2026-10-27 (#1610). The seal blocks the call when the board
// does not answer. The hooks of a session's start, prompts, turns, end and
// subagents stay commands: they run seldom, and SessionStart takes no HTTP
// hook.
func GoblinHooks(root, stateDir, board string) []Hook {
	hooks := []Hook{{Name: "goblin-seal", Event: "PreToolUse", Matcher: GoblinSealMatcher, URL: board + GoblinSealPath, Timeout: KeyAlarmSeconds, ShouldBlockOnFailure: true}}
	for _, hook := range NativeHooks(root, stateDir) {
		if hook.Event == "PostToolUse" {
			hook = Hook{Name: "native-hook", Event: "PostToolUse", URL: board + GoblinToolPath, Timeout: KeyAlarmSeconds}
		}
		hooks = append(hooks, hook)
	}
	return hooks
}

// GoblinSettings is the Claude Code settings document a goblin starts with:
// the goblin hooks, posting to the board at board, and the auto-compact
// window, which Claude Code adds to the user's own settings for that session
// alone. Each command hook runs the home's hook program where it is the same
// build as goblins.exe (HookCommand), which Windows starts with no console host.
func GoblinSettings(root, stateDir, board string) ([]byte, error) {
	hooks := GoblinHooks(root, stateDir, board)
	command := HookCommand(filepath.Join(root, home.BinDir))
	for index := range hooks {
		if hooks[index].URL == "" {
			hooks[index].Command = command
		}
	}
	file := &settingsFile{values: map[string]any{"autoCompactWindow": AutoCompactWindow}}
	if err := file.addHooks(hooks); err != nil {
		return nil, err
	}
	return file.render()
}

// GoblinPiExtension is the pi extension a pi goblin starts with, beside the
// native one every pi session of the machine loads: its goblin seal, which
// runs program, the home's hook program (HookCommand), as goblins hook
// goblin-seal-pi before each tool call, which checks a bash call's command
// and every call's paths, and blocks the call with the reason the seal gives
// on stdout. A seal that
// cannot run or answer within the native hooks' limit blocks the call too,
// as pi blocks one whose handler throws, since the seal refuses what it
// cannot read. It is plain JavaScript, which pi loads as it loads
// TypeScript, so a test runs it in Node as it is.
func GoblinPiExtension(program string) string {
	exe, _ := json.Marshal(program)
	return `// Code Goblins goblin seal, written anew at each pi goblin's launch
import { spawnSync } from "node:child_process";
export default function (pi) {
  pi.on("tool_call", (event, ctx) => {
    const result = spawnSync(` + string(exe) + `, ["hook", "goblin-seal-pi"], {
      input: JSON.stringify({cwd: ctx.cwd, tool_name: event.toolName, tool_input: event.input}),
      encoding: "utf8", windowsHide: true, timeout: ` + strconv.Itoa(nativehook.TimeoutSeconds*1000) + `,
    });
    if (result.error || result.status !== 0) {
      return {block: true, reason: "This command waits: the check goblins makes before each command could not run (" + (result.error ? result.error.message : "exit " + result.status) + "). Run it again in a minute."};
    }
    const answer = result.stdout.trim() ? JSON.parse(result.stdout).hookSpecificOutput : undefined;
    if (answer && answer.permissionDecision === "deny") return {block: true, reason: answer.permissionDecisionReason};
  });
}
`
}

// UserHooks is the `goblins hook <name>` of every CEO hook entry the settings file
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

// HookName is the `goblins hook <name>` a settings hook entry runs, for an entry
// in the form install writes (the home's goblins.exe with args hook and a name)
// or in the shell form it wrote before; ok is false for any other entry.
func HookName(entry map[string]any) (name string, ok bool) {
	command, _ := entry["command"].(string)
	if rest, shell := strings.CutPrefix(command, shellFormPrefix); shell {
		_, name, found := strings.Cut(rest, "goblins.exe hook ")
		return name, found
	}
	args, _ := entry["args"].([]any)
	if len(args) != 2 || args[0] != "hook" || !strings.EqualFold(filepath.Base(command), "goblins.exe") {
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
		if hook.URL != "" {
			headers, variables := map[string]any{}, make([]any, 0, len(HookVariables))
			for _, variable := range HookVariables {
				headers[HookHeader(variable)] = "${" + variable + "}"
				variables = append(variables, variable)
			}
			entry = map[string]any{"type": "http", "url": hook.URL, "headers": headers, "allowedEnvVars": variables}
		}
		if hook.ShouldBlockOnFailure {
			entry["onFailure"] = "block"
		}
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
