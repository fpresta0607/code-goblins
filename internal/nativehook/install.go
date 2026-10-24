package nativehook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// These conservative floors are the installed contracts verified for this
// integration. Older builds require verification before enabling native hooks.
func CheckCapability(harness, version, help string) error {
	floors := map[string][3]int{"codex": {0, 154, 0}, "claude": {2, 1, 278}, "pi": {0, 85, 1}}
	floor, ok := floors[harness]
	if !ok {
		return errors.New("native hooks support claude, codex, and pi")
	}
	match := regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\b`).FindStringSubmatch(version)
	if len(match) != 4 {
		return fmt.Errorf("%s version is unavailable or unrecognized", harness)
	}
	for i := 0; i < 3; i++ {
		n, _ := strconv.Atoi(match[i+1])
		if n < floor[i] {
			return fmt.Errorf("%s native hooks require verified contract >= %d.%d.%d", harness, floor[0], floor[1], floor[2])
		}
		if n > floor[i] {
			break
		}
	}
	if harness == "codex" && !strings.Contains(help, "--dangerously-bypass-hook-trust") {
		return errors.New("installed Codex does not advertise native hook trust support")
	}
	return nil
}

type InstallConfig struct{ Harness, ConfigDir, Executable, Home, State string }

// TimeoutSeconds is how long Claude Code, Codex and pi let a native hook run
// before they end it, which loses its event. The hook's own work, reading the
// payload and writing one file into the spool, took 2 ms at the median and
// 0.33 s at most on a loaded machine, so what the limit must cover is the
// start of the process: the harness creates it, Windows maps the program and
// Defender clears it, each waiting its turn behind the fleet's own builds and
// tests. Of the 36,191 native hook runs Claude Code recorded on a busy fleet
// from 2026-10-16 to 10-20, 33 ran past the 3 seconds the hooks had, one for
// 19 seconds, and the 24 the harness ended lost their events, among them the
// CFO's own prompts (issue #1211). A hook that finishes sooner ends sooner,
// so the limit costs time only while the machine itself is stalled.
const TimeoutSeconds = 30

const ownedMarker = "# CFO native hooks v1"

// Install owns only its exact hook command and helper files. Unrelated JSON
// members, matcher groups and hook handlers remain byte-preserved RawMessages.
// It does not edit model settings, hook trust, or the shared gate policy.
func Install(c InstallConfig) (string, error) {
	for _, path := range []string{c.ConfigDir, c.Executable, c.Home, c.State} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n\x00\"") {
			return "", errors.New("native hook setup needs absolute paths without quotes or control characters")
		}
	}
	if c.Harness != "claude" && c.Harness != "codex" && c.Harness != "pi" {
		return "", errors.New("unsupported native hook harness")
	}
	if c.Harness == "pi" {
		path := filepath.Join(c.ConfigDir, "extensions", "cfo-native.ts")
		return path, writeOwned(path, piExtension(c))
	}
	path := settingsPath(c.Harness, c.ConfigDir)
	original, doc, hooks, err := readHooks(path)
	if err != nil {
		return "", err
	}
	helper, command := helperCommand(c.ConfigDir)
	direct, isDirect := directCommand(c)
	if isDirect {
		command = direct
	}
	for _, event := range Events(c.Harness) {
		kept, stood, err := withoutOwned(hooks[event], c.Harness, c.ConfigDir)
		if err != nil {
			return "", err
		}
		if stood < 0 {
			stood = len(kept)
		}
		entry := struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		}{}
		entry.Hooks = append(entry.Hooks, struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		}{"command", command, TimeoutSeconds})
		encoded, _ := json.Marshal(entry)
		hooks[event] = slices.Insert(kept, stood, json.RawMessage(encoded))
	}
	doc["hooks"], err = json.Marshal(hooks)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(c.ConfigDir, 0700); err != nil {
		return "", err
	}
	if len(original) > 0 && !bytes.Equal(original, append(data, '\n')) {
		backup, err := os.OpenFile(path+".cfo-native.backup", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			_, werr := backup.Write(original)
			if err := errors.Join(werr, backup.Close()); err != nil {
				return "", err
			}
		} else if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	if isDirect {
		// The hooks no longer run a helper an earlier install wrote.
		if _, err := removeOwned(helper); err != nil {
			return "", err
		}
	} else {
		script := ownedMarker + "\n$OutputEncoding = [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)\n[Console]::In.ReadToEnd() | & " + ps(c.Executable) + " 'native-hook' " + ps(c.Harness) + " '--home' " + ps(c.Home) + " '--state' " + ps(c.State) + "\nexit $LASTEXITCODE\n"
		if err := writeOwned(helper, script); err != nil {
			return "", err
		}
	}
	if err := fsx.AtomicWriteFile(path, append(data, '\n')); err != nil {
		return "", err
	}
	return path, nil
}

// Events are the hook events of harness, Claude Code or Codex, whose native
// hooks report a session's lifecycle.
func Events(harness string) []string {
	events := []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd", "SubagentStart", "SubagentStop"}
	if harness == "codex" {
		events = append(events, "Interrupt", "PreCompact", "PostCompact")
	}
	return events
}

func writeOwned(path, content string) error {
	prior, err := fsx.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(prior) > 0 && !owned(prior) {
		return fmt.Errorf("refusing to replace unowned helper %s", path)
	}
	if string(prior) == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, []byte(content))
}

// owned reports whether a helper file's content is one Install wrote.
func owned(content []byte) bool {
	return bytes.HasPrefix(content, []byte(ownedMarker)) || bytes.HasPrefix(content, []byte("// CFO native hooks v1"))
}

// Uninstall removes what Install wrote for harness under configDir: its hook
// command from every event, the groups and events that leaves empty, and the
// helper script or Pi extension while it is still the one Install owns.
// Everything else in the settings is left as it is. It reports whether it
// removed anything.
func Uninstall(harness, configDir string) (bool, error) {
	switch harness {
	case "pi":
		return removeOwned(filepath.Join(configDir, "extensions", "cfo-native.ts"))
	case "claude", "codex":
	default:
		return false, errors.New("unsupported native hook harness")
	}
	path := settingsPath(harness, configDir)
	_, doc, hooks, err := readHooks(path)
	if err != nil {
		return false, err
	}
	helper, _ := helperCommand(configDir)
	removed := false
	for event, groups := range hooks {
		kept, stood, err := withoutOwned(groups, harness, configDir)
		if err != nil {
			return false, err
		}
		switch {
		case stood < 0:
			continue
		case len(kept) == 0:
			delete(hooks, event)
		default:
			hooks[event] = kept
		}
		removed = true
	}
	if removed {
		if len(hooks) == 0 {
			delete(doc, "hooks")
		} else if doc["hooks"], err = json.Marshal(hooks); err != nil {
			return false, err
		}
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return false, err
		}
		if err := fsx.AtomicWriteFile(path, append(data, '\n')); err != nil {
			return false, err
		}
	}
	removedHelper, err := removeOwned(helper)
	return removed || removedHelper, err
}

// Installed reports whether harness's configuration under configDir still
// holds anything Install wrote: a hook command of its own, or its helper
// script or Pi extension. It reads and changes nothing, which is how an
// uninstall reads its removal back.
func Installed(harness, configDir string) (bool, error) {
	switch harness {
	case "pi":
		return ownedThere(filepath.Join(configDir, "extensions", "cfo-native.ts"))
	case "claude", "codex":
	default:
		return false, errors.New("unsupported native hook harness")
	}
	_, _, hooks, err := readHooks(settingsPath(harness, configDir))
	if err != nil {
		return false, err
	}
	for _, groups := range hooks {
		_, stood, err := withoutOwned(groups, harness, configDir)
		if err != nil {
			return false, err
		}
		if stood >= 0 {
			return true, nil
		}
	}
	helper, _ := helperCommand(configDir)
	return ownedThere(helper)
}

// ownedThere reports whether path holds a file Install wrote.
func ownedThere(path string) (bool, error) {
	content, err := fsx.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil && owned(content), err
}

// settingsPath is the file holding harness's hooks under configDir.
func settingsPath(harness, configDir string) string {
	if harness == "claude" {
		return filepath.Join(configDir, "settings.json")
	}
	return filepath.Join(configDir, "hooks.json")
}

// helperCommand is the helper script Install writes under configDir and the
// exact hook command that runs it, which is how its entries are recognised.
func helperCommand(configDir string) (helper, command string) {
	helper = filepath.Join(configDir, "cfo-native-hook.ps1")
	return helper, `powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "` + filepath.ToSlash(helper) + `"`
}

// unquotedPath matches a path PowerShell, cmd and bash all read as one word
// with no quotes, written with forward slashes.
const unquotedPath = `[A-Za-z0-9:/._~-]+`

// directCommand is the Codex hook command that runs cfo itself, and whether
// Codex hooks use it. Codex runs each hook command inside the session's own
// shell, powershell.exe -NoProfile -Command on Windows, so the helper's
// command starts a second PowerShell for every event; live on 2026-10-08
// that took longer than the 3-second limit and every hook timed out. Without
// quotes it means the same to PowerShell, cmd and bash, so a path that would
// need quoting keeps the helper. Claude Code keeps the helper.
func directCommand(c InstallConfig) (string, bool) {
	executable, home, state := filepath.ToSlash(c.Executable), filepath.ToSlash(c.Home), filepath.ToSlash(c.State)
	whole := regexp.MustCompile(`^` + unquotedPath + `$`)
	if c.Harness != "codex" || !whole.MatchString(executable) || !whole.MatchString(home) || !whole.MatchString(state) {
		return "", false
	}
	return executable + " native-hook codex --home " + home + " --state " + state, true
}

// CodexArguments are the Codex arguments that give one session its native
// hooks, with the hooks feature on: a -c override for each lifecycle event,
// running cfo native-hook codex directly as Install's hooks do, in the
// session's own configuration rather than the user's hooks.json, which every
// Codex session on the machine loads, the Overlord's own outside the fleet
// included. Codex keys a hook's trust by where it is defined, which for a -c
// override is the same for every session, and by what it runs, so the
// Overlord trusts them once, in /hooks, for every session of the home. A path
// that would need quoting cannot be written in one.
func CodexArguments(executable, home, state string) ([]string, error) {
	command, ok := directCommand(InstallConfig{Harness: "codex", Executable: executable, Home: home, State: state})
	if !ok {
		return nil, fmt.Errorf("native hooks: Codex cannot run cfo from %s with home %s and state %s without quoting them", executable, home, state)
	}
	args := []string{"--enable", "hooks"}
	for _, event := range Events("codex") {
		args = append(args, "-c", "hooks."+event+"=[{hooks=[{type='command',command='"+command+"',timeout="+strconv.Itoa(TimeoutSeconds)+"}]}]")
	}
	return args, nil
}

// CodexGoblinArguments are a Codex goblin's CodexArguments with its goblin
// seal, which a Claude Code goblin's settings run before each command
// (install.GoblinHooks): a PreToolUse override for Bash, the name Codex gives
// a shell command and an exec_command call alike, running cfo hook
// goblin-seal-codex directly with Codex's own limit for a hook, as Claude
// Code's is for the seal. The Codex CFO's launch carries no seal, as the
// Claude Code CFO's settings carry none.
func CodexGoblinArguments(executable, home, state string) ([]string, error) {
	args, err := CodexArguments(executable, home, state)
	if err != nil {
		return nil, err
	}
	return append(args, "-c", "hooks.PreToolUse=[{matcher='Bash',hooks=[{type='command',command='"+filepath.ToSlash(executable)+" hook goblin-seal-codex'}]}]"), nil
}

// ownsCommand reports whether a hook command is one Install wrote for harness
// under configDir: the helper's, or cfo run directly for harness from any
// place it was installed, so a reinstall after the binary moved replaces it.
func ownsCommand(harness, configDir, command string) bool {
	_, helper := helperCommand(configDir)
	direct := regexp.MustCompile(`^` + unquotedPath + ` native-hook ` + regexp.QuoteMeta(harness) + ` --home ` + unquotedPath + ` --state ` + unquotedPath + `$`)
	return command == helper || direct.MatchString(command)
}

// readHooks reads a harness settings file: its bytes as found, the document,
// and its hooks by event. A missing file is an empty document.
func readHooks(path string) ([]byte, map[string]json.RawMessage, map[string][]json.RawMessage, error) {
	original, err := fsx.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, err
	}
	doc := map[string]json.RawMessage{}
	if len(original) > 0 {
		if err := json.Unmarshal(bytes.TrimPrefix(original, []byte{0xef, 0xbb, 0xbf}), &doc); err != nil || doc == nil {
			return nil, nil, nil, errors.New("native setup: invalid settings JSON")
		}
	}
	hooks := map[string][]json.RawMessage{}
	if raw, ok := doc["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil || hooks == nil {
			return nil, nil, nil, errors.New("native setup: invalid hooks object")
		}
	}
	return original, doc, hooks, nil
}

// withoutOwned returns an event's matcher groups with every handler Install
// wrote for harness under configDir removed, dropping the groups left empty,
// and where the first group that ran one stood among the groups kept, or -1
// when none did: Install puts its group back there, so a rerun moves nothing.
// Groups it does not touch keep their bytes.
func withoutOwned(groups []json.RawMessage, harness, configDir string) ([]json.RawMessage, int, error) {
	kept := make([]json.RawMessage, 0, len(groups)+1)
	stood := -1
	for _, raw := range groups {
		var group map[string]json.RawMessage
		if err := json.Unmarshal(raw, &group); err != nil || group == nil {
			return nil, 0, errors.New("native setup: invalid hook group")
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(group["hooks"], &entries); err != nil {
			return nil, 0, errors.New("native setup: invalid hook handlers")
		}
		remaining := make([]json.RawMessage, 0, len(entries))
		for _, entry := range entries {
			var handler struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal(entry, &handler); err != nil {
				return nil, 0, err
			}
			if !ownsCommand(harness, configDir, handler.Command) {
				remaining = append(remaining, entry)
			}
		}
		if stood < 0 && len(remaining) < len(entries) {
			stood = len(kept)
		}
		if len(remaining) == len(entries) {
			kept = append(kept, raw)
		} else if len(remaining) > 0 {
			group["hooks"], _ = json.Marshal(remaining)
			updated, _ := json.Marshal(group)
			kept = append(kept, updated)
		}
	}
	return kept, stood, nil
}

// removeOwned deletes path when it is a file Install wrote, and reports
// whether it did; a file with other content is left in place.
func removeOwned(path string) (bool, error) {
	content, err := fsx.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !owned(content) {
		return false, nil
	}
	return true, os.Remove(path)
}

func ps(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func piExtension(c InstallConfig) string {
	exe, _ := json.Marshal(c.Executable)
	args, _ := json.Marshal([]string{"native-hook", "pi", "--home", c.Home, "--state", c.State})
	return `// CFO native hooks v1
import { spawnSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import type { ExtensionAPI, ExtensionContext, ToolExecutionEndEvent } from "@earendil-works/pi-coding-agent";
export default function (pi: ExtensionAPI) {
  let turn = 0;
  const instance = randomUUID();
  const fire = (name: string, ctx: ExtensionContext, tool?: ToolExecutionEndEvent) => {
    if (name === "agent_settled" && ctx.hasPendingMessages()) return;
    const payload = {hook_event_name: name, session_id: ctx.sessionManager.getSessionId(), cwd: ctx.cwd,
      turn_id: instance + ":" + turn, model: ctx.model?.id, pending_messages: ctx.hasPendingMessages(),
      ...(tool && process.env.CFO_ROLE === "goblin" ? {tool_name: tool.toolName, tool_use_id: tool.toolCallId, tool_response: tool.result} : {})};
    let input: string;
    try {
      input = JSON.stringify(payload);
    } catch {
      console.error("CFO native event could not be encoded");
      return;
    }
    if (Buffer.byteLength(input, "utf8") > ` + strconv.Itoa(MaxInputBytes) + `) {
      console.error("CFO native event exceeds 64 MiB");
      return;
    }
    const result = spawnSync(` + string(exe) + `, ` + string(args) + `, {
      input, encoding: "utf8", windowsHide: true, timeout: ` + strconv.Itoa(TimeoutSeconds*1000) + `,
    });
    // Each outcome says only what is known, and names the fixed event so a
    // later failure can be correlated. A timeout or a signal can follow a
    // child that already recorded its event, so neither claims otherwise. The
    // child's output is never repeated, since a tool result may carry private
    // data.
    const code = result.error && result.error.code;
    const outcome =
      code === "ETIMEDOUT" ? "timed out. It may have started and recorded the event." :
      code === "ENOENT" || code === "EACCES" || code === "EPERM" || code === "ENOTDIR" ? "could not start (" + code + "). The event was not recorded." :
      result.signal ? "was terminated by " + result.signal + ". It may have recorded the event." :
      typeof result.status === "number" && result.status !== 0 ? "exited with status " + result.status + ". It may have recorded the event." :
      result.error ? "could not complete (" + (code || "unknown error") + "). It may have recorded the event." : "";
    if (outcome) console.error("CFO native hook for " + name + " " + outcome);
  };
  pi.on("session_start", (_event, ctx) => fire("session_start", ctx));
  pi.on("session_before_compact", (_event, ctx) => fire("session_before_compact", ctx));
  pi.on("session_compact", (_event, ctx) => fire("session_compact", ctx));
  pi.on("agent_start", (_event, ctx) => { turn++; fire("agent_start", ctx); });
  pi.on("tool_execution_end", (event, ctx) => fire("tool_execution_end", ctx, event));
  pi.on("agent_settled", (_event, ctx) => fire("agent_settled", ctx));
  pi.on("session_shutdown", (_event, ctx) => fire("session_shutdown", ctx));
}
`
}
