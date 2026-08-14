// Package doctor verifies the tools cfo shells out to, with install hints.
package doctor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// Check is one tool's verdict. Err empty means usable.
type Check struct {
	Name    string
	Version string
	Err     string
	Hint    string
}

var tools = []struct {
	name string
	hint string
}{
	{name: "git", hint: "winget install Git.Git"},
	{name: "gh", hint: "winget install GitHub.cli, then gh auth login"},
	{name: "claude", hint: "npm install -g @anthropic-ai/claude-code"},
	{name: "herdr", hint: "irm https://herdr.dev/install.ps1 | iex"},
	{name: "treehouse", hint: "see github.com/kunchenguid/treehouse"},
	{name: "codex", hint: "npm install -g @openai/codex"},
	{name: "pi", hint: "npm install -g @mariozechner/pi-coding-agent"},
	{name: "tasks-axi", hint: "npm install -g tasks-axi"},
	{name: "quota-axi", hint: "npm install -g quota-axi"},
}

// Run checks every required tool in a fixed order, plus the turnend-guard /
// stop-autoarm hook pairing.
func Run() []Check {
	checks := make([]Check, 0, len(tools)+1)
	for _, tool := range tools {
		path, err := exec.LookPath(tool.name)
		if err != nil {
			checks = append(checks, Check{Name: tool.name, Err: "not found on PATH", Hint: tool.hint})
			continue
		}
		out, err := exec.Command(path, "--version").Output()
		if err != nil {
			checks = append(checks, Check{Name: tool.name, Err: tool.name + " --version failed", Hint: tool.hint})
			continue
		}
		version, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
		checks = append(checks, Check{Name: tool.name, Version: strings.TrimSpace(version)})
	}
	checks = append(checks, checkHookPairing())
	return checks
}

// hookPairingHint is checkHookPairing's remedy for a guard registered alone.
const hookPairingHint = `register "cfo hook stop-autoarm" as a Stop hook with asyncRewake, or the turn-end guard will block without anything restoring the watcher`

// checkHookPairing reads the resolved home's .claude/settings.json and
// reports unhealthy only when "cfo hook turnend-guard" is registered
// somewhere in it without "cfo hook stop-autoarm" also present: a guard
// with nothing to prove recovery is under way will eventually run its own
// escalation ladder to the hard ceiling on every blocked turn instead of the
// auto-arm ever recovering the watcher. Presence-only: it does not parse
// which hook event either string is registered under, only that both
// strings occur somewhere in the file. It passes silently (no Err) when the
// home cannot be resolved, the file is absent or unreadable, the JSON is
// malformed, or neither hook string appears - malformed JSON is deliberately
// never a hard failure, since a broken settings.json is Claude Code's
// problem to surface, not this check's.
func checkHookPairing() Check {
	h, err := home.Resolve()
	if err != nil {
		return Check{Name: "hook-pairing"}
	}
	data, err := os.ReadFile(filepath.Join(h.Root, ".claude", "settings.json"))
	if err != nil {
		return Check{Name: "hook-pairing"}
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Check{Name: "hook-pairing"}
	}
	text := string(data)
	hasGuard := strings.Contains(text, "cfo hook turnend-guard")
	hasAutoarm := strings.Contains(text, "cfo hook stop-autoarm")
	if hasGuard && !hasAutoarm {
		return Check{
			Name: "hook-pairing",
			Err:  `"cfo hook turnend-guard" is registered without "cfo hook stop-autoarm"`,
			Hint: hookPairingHint,
		}
	}
	return Check{Name: "hook-pairing"}
}

// Healthy reports whether every check passed.
func Healthy(checks []Check) bool {
	for _, c := range checks {
		if c.Err != "" {
			return false
		}
	}
	return true
}
