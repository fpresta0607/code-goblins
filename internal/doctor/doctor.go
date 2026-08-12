// Package doctor verifies the tools cfo shells out to, with install hints.
package doctor

import (
	"os/exec"
	"strings"
)

// Check is one tool's verdict. Err empty means usable.
type Check struct {
	Name    string
	Version string
	Err     string
	Hint    string
}

var tools = []struct {
	name         string
	presenceOnly bool
	hint         string
}{
	{name: "git", hint: "winget install Git.Git"},
	{name: "gh", hint: "winget install GitHub.cli, then gh auth login"},
	{name: "claude", hint: "npm install -g @anthropic-ai/claude-code"},
	{name: "herdr", hint: "irm https://herdr.dev/install.ps1 | iex"},
	// Presence-only until Plan 3 verifies treehouse --version on Windows.
	{name: "treehouse", presenceOnly: true, hint: "see github.com/kunchenguid/treehouse"},
}

// Run checks every required tool in a fixed order.
func Run() []Check {
	checks := make([]Check, 0, len(tools))
	for _, tool := range tools {
		path, err := exec.LookPath(tool.name)
		if err != nil {
			checks = append(checks, Check{Name: tool.name, Err: "not found on PATH", Hint: tool.hint})
			continue
		}
		if tool.presenceOnly {
			checks = append(checks, Check{Name: tool.name, Version: "present at " + path})
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
	return checks
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
