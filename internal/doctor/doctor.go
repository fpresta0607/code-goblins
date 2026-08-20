// Package doctor verifies the tools cfo shells out to, with install hints.
package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
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
	{name: "herdr", hint: "irm https://herdr.dev/install.ps1 | iex"},
	{name: "treehouse", hint: "irm https://kunchenguid.github.io/treehouse/install.ps1 | iex"},
	{name: "tasks-axi", hint: "npm install -g tasks-axi"},
	{name: "quota-axi", hint: "npm install -g quota-axi"},
	{name: "no-mistakes", hint: "irm https://raw.githubusercontent.com/kunchenguid/no-mistakes/main/docs/install.ps1 | iex"},
	{name: "gh-axi", hint: "npm install -g gh-axi"},
	{name: "chrome-devtools-axi", hint: "npm install -g chrome-devtools-axi"},
}

// harnessTools are the interactive harnesses a spawn can select. They are
// checked only through the single probed --version path in ProbeHarnesses, so
// each harness runs exactly one bounded sanity probe.
var harnessTools = []struct {
	name string
	hint string
}{
	{name: "claude", hint: "npm install -g @anthropic-ai/claude-code"},
	{name: "codex", hint: "npm install -g @openai/codex"},
	{name: "pi", hint: "npm install -g @earendil-works/pi-coding-agent"},
	{name: "kimi", hint: "install the Kimi Code CLI (kimi.com)"},
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

// checkHookPairing reads the settings files a session's hooks can come from
// and reports unhealthy only when the turnend-guard hook is registered
// without the stop-autoarm hook also present: a guard with nothing to
// prove recovery is under way will eventually run its own escalation ladder
// to the hard ceiling on every blocked turn instead of the auto-arm ever
// recovering the watcher. Presence-only: it does not parse which hook event
// either command is registered under, only that each appears in some string
// value of the parsed settings. Each hook is recognized in both the exact
// command `cfo install` writes (taken from install.Hooks, never a second
// hand-kept copy) and the hand-written `cfo hook <name>` form.
//
// Both scopes are read, because `cfo install` moves the CFO hooks to the
// user settings and a check that only ever looked in the checkout would go
// quiet exactly when the hooks were working. It passes silently (no Err)
// when the home cannot be resolved, neither file is present or readable,
// the JSON is malformed, or neither hook command appears - malformed JSON is
// deliberately never a hard failure, since a broken settings.json is Claude
// Code's problem to surface, not this check's.
func checkHookPairing() Check {
	paths := []string{}
	if h, err := home.Resolve(); err == nil {
		paths = append(paths, filepath.Join(h.Root, ".claude", "settings.json"))
	}
	if userSettings, err := install.UserSettingsPath(); err == nil {
		paths = append(paths, userSettings)
	}
	values := []string{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var parsed any
		if err := json.Unmarshal(data, &parsed); err != nil {
			continue
		}
		values = append(values, jsonStrings(parsed)...)
	}
	hasGuard := registersHook(values, "turnend-guard")
	hasAutoarm := registersHook(values, "stop-autoarm")
	if hasGuard && !hasAutoarm {
		return Check{
			Name: "hook-pairing",
			Err:  `"cfo hook turnend-guard" is registered without "cfo hook stop-autoarm"`,
			Hint: hookPairingHint,
		}
	}
	return Check{Name: "hook-pairing"}
}

// jsonStrings collects every string value in a decoded JSON document, so
// hook commands are matched in their decoded form rather than against raw
// file text, where the quotes inside an installed command appear as `\"`
// escape sequences and never match.
func jsonStrings(node any) []string {
	switch value := node.(type) {
	case string:
		return []string{value}
	case []any:
		values := []string{}
		for _, item := range value {
			values = append(values, jsonStrings(item)...)
		}
		return values
	case map[string]any:
		values := []string{}
		for _, item := range value {
			values = append(values, jsonStrings(item)...)
		}
		return values
	}
	return nil
}

// registersHook reports whether any settings string invokes `cfo hook <name>`,
// either as the exact command `cfo install` writes or in the hand-written
// `cfo hook <name>` form.
func registersHook(values []string, name string) bool {
	needles := []string{"cfo hook " + name}
	for _, hook := range install.Hooks() {
		if hook.Name == name {
			needles = append(needles, hook.Command)
		}
	}
	for _, value := range values {
		for _, needle := range needles {
			if strings.Contains(value, needle) {
				return true
			}
		}
	}
	return false
}

// ProbeTimeout bounds one harness --version spawn sanity probe: a harness
// that cannot answer cheaply would hang or waste every pipeline attempt.
const ProbeTimeout = 15 * time.Second

// HarnessProbe is one supported harness's spawn sanity verdict. OK false
// means the harness is broken: every pipeline attempt on it is wasted time.
type HarnessProbe struct {
	Name   string
	Detail string
	OK     bool
}

// ProbeHarnesses runs a cheap spawn sanity check (<harness> --version under a
// short timeout) for each supported harness, surfacing breakage like a
// process that cannot start (exit 0xc0000142) before a run discovers it.
// A harness absent from PATH is reported broken with its install hint, so the
// doctor exit code still fails closed for a missing harness.
func ProbeHarnesses(ctx context.Context) []HarnessProbe {
	probes := make([]HarnessProbe, 0, len(harnessTools))
	for _, tool := range harnessTools {
		path, err := exec.LookPath(tool.name)
		if err != nil {
			probes = append(probes, HarnessProbe{Name: tool.name, Detail: "not found on PATH (install: " + tool.hint + ")"})
			continue
		}
		probes = append(probes, probeHarness(ctx, tool.name, path))
	}
	return probes
}

func probeHarness(ctx context.Context, name, path string) HarnessProbe {
	probeCtx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, path, "--version").Output()
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return HarnessProbe{Name: name, Detail: name + " --version timed out"}
	}
	if err != nil {
		detail := err.Error()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
				detail = detail + ": " + stderr
			}
		}
		return HarnessProbe{Name: name, Detail: detail}
	}
	version, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return HarnessProbe{Name: name, Detail: strings.TrimSpace(version), OK: true}
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
