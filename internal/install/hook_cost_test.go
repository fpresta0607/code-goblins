package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// registeredHook is one hook entry as Claude Code reads it from settings.
type registeredHook struct {
	Event   string
	Matcher string
	Command string
	Args    []string
	Shell   bool
}

func registeredHooks(t *testing.T, path string) []registeredHook {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string    `json:"command"`
				Args    *[]string `json:"args"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("%s is not valid JSON: %v\n%s", path, err, raw)
	}
	hooks := []registeredHook{}
	for event, groups := range document.Hooks {
		for _, group := range groups {
			for _, entry := range group.Hooks {
				hook := registeredHook{Event: event, Matcher: group.Matcher, Command: entry.Command, Shell: entry.Args == nil}
				if entry.Args != nil {
					hook.Args = *entry.Args
				}
				hooks = append(hooks, hook)
			}
		}
	}
	return hooks
}

// exactNames is the matcher Claude Code reads as a list of exact tool names:
// letters, digits, _, -, spaces, commas and |. Anything else is an unanchored
// JavaScript regular expression, which Go's regexp reads the same way for the
// character classes and alternatives the installer writes.
var exactNames = regexp.MustCompile(`^[A-Za-z0-9_\-, |]*$`)

// selects applies Claude Code's matcher rules to one tool name.
func selects(t *testing.T, matcher, tool string) bool {
	t.Helper()
	switch {
	case matcher == "" || matcher == "*":
		return true
	case exactNames.MatchString(matcher):
		for _, name := range strings.FieldsFunc(matcher, func(r rune) bool { return r == '|' || r == ',' }) {
			if strings.TrimSpace(name) == tool {
				return true
			}
		}
		return false
	default:
		pattern, err := regexp.Compile(matcher)
		if err != nil {
			t.Fatalf("matcher %q is not a regular expression: %v", matcher, err)
		}
		return pattern.MatchString(tool)
	}
}

// Every pre-tool hook starts a process before its tool runs, and a shell
// command starts two more (Git Bash's launcher and bash) before cfo.exe does.
// What a tool call costs is the processes its matching hooks start: the
// reading and editing tools a session lives in start none, a Bash call one
// cfo.exe, and the delegation tools the guard can refuse one each.
func TestInstalledPreToolHooksStartOneProcessPerBashCallAndNoneForSessionTools(t *testing.T) {
	// Arrange
	f := newFixture(t, "", nil)

	// Act
	f.install()

	// Assert
	processes := func(tool string) int {
		total := 0
		for _, hook := range registeredHooks(t, f.user) {
			if hook.Event != "PreToolUse" || !selects(t, hook.Matcher, tool) {
				continue
			}
			total++
			if hook.Shell {
				total += 2
			}
		}
		return total
	}
	for _, tool := range []string{"Read", "Edit", "Write", "Grep", "Glob", "NotebookEdit", "WebFetch", "WebSearch", "Skill", "ToolSearch", "TodoWrite", "LSP"} {
		if got := processes(tool); got != 0 {
			t.Errorf("a %s call starts %d process(es) for CFO hooks, want none", tool, got)
		}
	}
	for _, tool := range []string{"Bash", "Agent", "Task", "SendMessage", "AskUserQuestion", "CronCreate", "EnterWorktree"} {
		if got := processes(tool); got != 1 {
			t.Errorf("a %s call starts %d process(es) for CFO hooks, want 1", tool, got)
		}
	}
}

// Claude Code runs a hook with args directly, with no shell, so every CFO hook
// names the home's own binary, by its full path, and the hook it runs. The
// full path is what lets a session opened in any repository be supervised.
func TestInstalledHooksRunTheHomesBinaryWithoutAShell(t *testing.T) {
	// Arrange
	f := newFixture(t, adopterSettings, nil)

	// Act
	f.install()

	// Assert
	binary := filepath.Join(f.bin, "cfo.exe")
	names := map[string]bool{}
	for _, hook := range registeredHooks(t, f.user) {
		if hook.Command != binary {
			continue
		}
		if hook.Shell || len(hook.Args) != 2 || hook.Args[0] != "hook" {
			t.Errorf("%s hook runs %q with args %q, want exec form [hook <name>]", hook.Event, hook.Command, hook.Args)
			continue
		}
		names[hook.Args[1]] = true
	}
	for _, hook := range Hooks(f.root) {
		if !names[hook.Name] {
			t.Errorf("hook %s is not registered in exec form", hook.Name)
		}
	}
}

func TestInstallRegistersPreCompactForManualAndAutomaticCompactionOnce(t *testing.T) {
	f := newFixture(t, adopterSettings, nil)
	f.install()
	f.install()

	count := 0
	for _, hook := range registeredHooks(t, f.user) {
		if hook.Event != "PreCompact" || hook.Command != filepath.Join(f.bin, "cfo.exe") {
			continue
		}
		count++
		if hook.Shell || len(hook.Args) != 2 || hook.Args[0] != "hook" || hook.Args[1] != "pre-compact" {
			t.Errorf("pre-compact hook = %#v", hook)
		}
		for _, trigger := range []string{"manual", "auto"} {
			if !selects(t, hook.Matcher, trigger) {
				t.Errorf("pre-compact matcher %q does not select %s", hook.Matcher, trigger)
			}
		}
	}
	if count != 1 {
		t.Fatalf("installed %d pre-compact hooks, want one", count)
	}
	f.uninstall()
	for _, hook := range registeredHooks(t, f.user) {
		if hook.Event == "PreCompact" && hook.Command == filepath.Join(f.bin, "cfo.exe") {
			t.Fatal("uninstall left the pre-compact hook registered")
		}
	}
}

// A machine installed before hooks ran without a shell holds the shell-form
// entries; installing again replaces them rather than running both.
func TestInstallReplacesShellFormHooks(t *testing.T) {
	// Arrange
	shellForm := `CFO_ROOT="${CFO_HOME:-$CLAUDE_PROJECT_DIR}"; [ -x "$CFO_ROOT"/cfo.exe ] || exit 0; "$CFO_ROOT"/cfo.exe hook `
	settings := `{"hooks": {
  "PreToolUse": [
    {"matcher": "Bash", "hooks": [{"type": "command", "command": "` + jsonEscape(shellForm+"pretool-arm") + `"}, {"type": "command", "command": "` + jsonEscape(shellForm+"pretool-cd") + `"}]},
    {"matcher": ".*", "hooks": [{"type": "command", "command": "` + jsonEscape(shellForm+"pretool-subagent") + `"}]},
    {"matcher": "Write", "hooks": [{"type": "command", "command": "bash -c 'markdown write guard'"}]}
  ],
  "Stop": [{"hooks": [{"type": "command", "command": "` + jsonEscape(shellForm+"turnend-guard") + `"}]}]
}}`
	f := newFixture(t, settings, nil)

	// Act
	f.install()

	// Assert
	for _, hook := range registeredHooks(t, f.user) {
		if strings.Contains(hook.Command, "cfo.exe hook") {
			t.Errorf("shell-form hook survived the install: %s", hook.Command)
		}
	}
	if !strings.Contains(strings.Join(hookCommands(t, f.user), "\n"), "bash -c 'markdown write guard'") {
		t.Error("the adopter's own hook was removed")
	}
}

func jsonEscape(s string) string {
	encoded, _ := json.Marshal(s)
	return string(encoded[1 : len(encoded)-1])
}
