// Package doctor verifies the tools cfo shells out to, with install hints.
package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
)

// Check is one tool's verdict. Err empty means usable. Floor is the minimum
// version when the tool has one. A Presentation check is reported but never
// makes the environment unhealthy: without it, visual review falls back to
// plain text and nonvisual work proceeds. An Installer check is reported the
// same way: only install.ps1 uses the tool, and cfo never does. So is an
// Optional one, which says who needs the tool.
type Check struct {
	Name         string
	Version      string
	Err          string
	Hint         string
	Floor        string
	Presentation bool
	Installer    bool
	Optional     string
}

// LavishRelease is the Code Goblins build of lavish-axi, from the fork at
// github.com/fpresta0607/lavish-axi: upstream's review page named and styled
// for Code Goblins, its server and CLI unchanged.
const LavishRelease = "https://github.com/fpresta0607/lavish-axi/releases/download/v0.1.79-codegoblins.1/lavish-axi-0.1.79-codegoblins.1.tgz"

var tools = []struct {
	name  string
	hint  string
	floor string
	// build is the build a tool installed from a fork names in its version,
	// which the upstream package of the same name does not.
	build        string
	presentation bool
	installer    bool
	optional     string
}{
	{name: "git", hint: "winget install Git.Git"},
	{name: "gh", hint: "winget install GitHub.cli, then gh auth login"},
	{name: "herdr", hint: "irm https://herdr.dev/install.ps1 | iex", optional: "only a goblin or CFO started in Herdr needs it"},
	{name: "tasks-axi", hint: "npm install -g tasks-axi"},
	{name: "quota-axi", hint: "npm install -g quota-axi"},
	{name: "no-mistakes", hint: "irm https://raw.githubusercontent.com/kunchenguid/no-mistakes/main/docs/install.ps1 | iex"},
	{name: "gh-axi", hint: "npm install -g gh-axi"},
	{name: "chrome-devtools-axi", hint: "npm install -g chrome-devtools-axi"},
	{name: "lavish-axi", hint: "npm install -g " + LavishRelease, floor: "0.1.79", build: "codegoblins", presentation: true},
	{name: "winget", hint: "App Installer from the Microsoft Store, https://apps.microsoft.com/detail/9NBLGGH4NNS1", installer: true},
}

// claudeInstall installs the native build of Claude Code, claude.exe, the one
// a native terminal can start.
const claudeInstall = "irm https://claude.ai/install.ps1 | iex"

// harnessTools are the interactive harnesses a spawn can select. They are
// checked only through the single probed --version path in ProbeHarnesses, so
// each harness runs exactly one bounded sanity probe.
var harnessTools = []struct {
	name string
	hint string
}{
	{name: "claude", hint: claudeInstall},
	{name: "codex", hint: "npm install -g @openai/codex"},
	{name: "pi", hint: "npm install -g @earendil-works/pi-coding-agent"},
	{name: "kimi", hint: "install the Kimi Code CLI (kimi.com)"},
}

// Run checks every required tool in a fixed order, plus the turnend-guard /
// stop-autoarm hook pairing.
func Run() []Check {
	checks := make([]Check, 0, len(tools)+1)
	for _, tool := range tools {
		check := Check{Name: tool.name, Hint: tool.hint, Floor: tool.floor, Presentation: tool.presentation, Installer: tool.installer, Optional: tool.optional}
		path, err := exec.LookPath(tool.name)
		if err != nil {
			check.Err = "not found on PATH"
			checks = append(checks, check)
			continue
		}
		out, err := execx.Command(path, "--version").Output()
		if err != nil {
			check.Err = tool.name + " --version failed"
			checks = append(checks, check)
			continue
		}
		version, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
		check.Version = strings.TrimSpace(version)
		if tool.floor != "" && !meetsFloor(check.Version, tool.floor) {
			check.Err = "version " + check.Version + " is below the floor"
		} else if tool.build != "" && !strings.Contains(check.Version, tool.build) {
			check.Err = "version " + check.Version + " is not the " + tool.build + " build"
		}
		checks = append(checks, check)
	}
	checks = append(checks, checkConPTY(), checkHookPairing())
	return checks
}

// pseudoConsole is the Windows call every native terminal runs in.
var pseudoConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("CreatePseudoConsole")

// checkConPTY reports whether this Windows has the pseudo console a native
// terminal runs its program in.
func checkConPTY() Check {
	check := Check{Name: "conpty", Version: "pseudo console available", Hint: "Windows 10 version 1809 or later, which native terminals run on"}
	if err := pseudoConsole.Find(); err != nil {
		check.Version, check.Err = "", "this Windows has no pseudo console"
	}
	return check
}

// meetsFloor reports whether the last field of a --version line is a dotted
// numeric version at or above floor, before any pre-release suffix such as a
// fork's build name. A component the version does not carry counts as 0. An
// unparseable version fails the floor.
func meetsFloor(versionLine, floor string) bool {
	fields := strings.Fields(versionLine)
	if len(fields) == 0 {
		return false
	}
	number, _, _ := strings.Cut(strings.TrimPrefix(fields[len(fields)-1], "v"), "-")
	have := strings.Split(number, ".")
	for i, wantPart := range strings.Split(floor, ".") {
		want, _ := strconv.Atoi(wantPart)
		got := 0
		if i < len(have) {
			parsed, err := strconv.Atoi(have[i])
			if err != nil {
				return false
			}
			got = parsed
		}
		if got != want {
			return got > want
		}
	}
	return true
}

// hookPairingHint is checkHookPairing's remedy for a guard registered alone.
const hookPairingHint = `register "cfo hook stop-autoarm" as a Stop hook with asyncRewake, or the turn-end guard will block without anything restoring the watcher`

// checkHookPairing reads the settings files a session's hooks can come from
// and reports unhealthy only when the turnend-guard hook is registered
// without the stop-autoarm hook also present: a guard with nothing to
// prove recovery is under way will eventually run its own escalation ladder
// to the hard ceiling on every blocked turn instead of the auto-arm ever
// recovering the watcher. Presence-only: it does not parse which hook event
// either hook is registered under, only that each is registered somewhere in
// the parsed settings. Each hook is recognized both as an entry `cfo install`
// writes (install.HookName) and in the hand-written `cfo hook <name>` form.
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
	installed := map[string]bool{}
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
		installedHookNames(parsed, installed)
	}
	hasGuard := installed["turnend-guard"] || registersHook(values, "turnend-guard")
	hasAutoarm := installed["stop-autoarm"] || registersHook(values, "stop-autoarm")
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

// installedHookNames adds to names every `cfo hook <name>` a hook entry that
// `cfo install` wrote runs, recognised as the installer recognises its own
// (install.HookName), never by a second hand-kept copy of its form.
func installedHookNames(node any, names map[string]bool) {
	switch value := node.(type) {
	case []any:
		for _, item := range value {
			installedHookNames(item, names)
		}
	case map[string]any:
		if name, ok := install.HookName(value); ok {
			names[name] = true
		}
		for _, item := range value {
			installedHookNames(item, names)
		}
	}
}

// registersHook reports whether any settings string invokes `cfo hook <name>`
// in the hand-written form.
func registersHook(values []string, name string) bool {
	for _, value := range values {
		if strings.Contains(value, "cfo hook "+name) {
			return true
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
		// A native terminal starts Claude Code as a program, with no shell
		// to run a script shim such as npm's claude.cmd.
		if tool.name == "claude" && !strings.EqualFold(filepath.Ext(path), ".exe") {
			fix := "install the native build: " + claudeInstall
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), "node_modules", "@anthropic-ai", "claude-code")); err == nil {
				fix += ", then remove npm's copy: npm uninstall -g @anthropic-ai/claude-code"
			}
			probes = append(probes, HarnessProbe{Name: tool.name, Detail: "resolves to " + path + ", a script a native terminal cannot start (" + fix + ")"})
			continue
		}
		probes = append(probes, probeHarness(ctx, tool.name, path))
	}
	return probes
}

func probeHarness(ctx context.Context, name, path string) HarnessProbe {
	probeCtx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	out, err := execx.CommandContext(probeCtx, path, "--version").Output()
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

// Healthy reports whether every check passed, ignoring presentation,
// installer and optional checks.
func Healthy(checks []Check) bool {
	for _, c := range checks {
		if c.Err != "" && !c.Presentation && !c.Installer && c.Optional == "" {
			return false
		}
	}
	return true
}
