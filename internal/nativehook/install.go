package nativehook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	events := []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd", "SubagentStart", "SubagentStop"}
	if c.Harness == "codex" {
		events = append(events, "Interrupt")
	}
	for _, event := range events {
		kept, _, err := withoutCommand(hooks[event], command)
		if err != nil {
			return "", err
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
		}{"command", command, 3})
		encoded, _ := json.Marshal(entry)
		hooks[event] = append(kept, encoded)
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
	script := ownedMarker + "\n$OutputEncoding = [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)\n[Console]::In.ReadToEnd() | & " + ps(c.Executable) + " 'native-hook' " + ps(c.Harness) + " '--home' " + ps(c.Home) + " '--state' " + ps(c.State) + "\nexit $LASTEXITCODE\n"
	if err := writeOwned(helper, script); err != nil {
		return "", err
	}
	if err := fsx.AtomicWriteFile(path, append(data, '\n')); err != nil {
		return "", err
	}
	return path, nil
}

func writeOwned(path, content string) error {
	prior, err := os.ReadFile(path)
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
	helper, command := helperCommand(configDir)
	total := 0
	for event, groups := range hooks {
		kept, removed, err := withoutCommand(groups, command)
		if err != nil {
			return false, err
		}
		total += removed
		switch {
		case removed == 0:
		case len(kept) == 0:
			delete(hooks, event)
		default:
			hooks[event] = kept
		}
	}
	if total > 0 {
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
	return total > 0 || removedHelper, err
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

// readHooks reads a harness settings file: its bytes as found, the document,
// and its hooks by event. A missing file is an empty document.
func readHooks(path string) ([]byte, map[string]json.RawMessage, map[string][]json.RawMessage, error) {
	original, err := os.ReadFile(path)
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

// withoutCommand returns an event's matcher groups with every handler that
// runs command removed, dropping the groups left empty, and how many handlers
// it removed. Groups it does not touch keep their bytes.
func withoutCommand(groups []json.RawMessage, command string) ([]json.RawMessage, int, error) {
	kept := make([]json.RawMessage, 0, len(groups)+1)
	removed := 0
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
			if handler.Command != command {
				remaining = append(remaining, entry)
			}
		}
		removed += len(entries) - len(remaining)
		if len(remaining) == len(entries) {
			kept = append(kept, raw)
		} else if len(remaining) > 0 {
			group["hooks"], _ = json.Marshal(remaining)
			updated, _ := json.Marshal(group)
			kept = append(kept, updated)
		}
	}
	return kept, removed, nil
}

// removeOwned deletes path when it is a file Install wrote, and reports
// whether it did; a file with other content is left in place.
func removeOwned(path string) (bool, error) {
	content, err := os.ReadFile(path)
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
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
export default function (pi: ExtensionAPI) {
  let turn = 0;
  const instance = randomUUID();
  const fire = (name: string, ctx: ExtensionContext) => {
    if (name === "agent_settled" && ctx.hasPendingMessages()) return;
    const payload = {hook_event_name: name, session_id: ctx.sessionManager.getSessionId(), cwd: ctx.cwd,
      turn_id: instance + ":" + turn, model: ctx.model?.id, pending_messages: ctx.hasPendingMessages()};
    const result = spawnSync(` + string(exe) + `, ` + string(args) + `, {
      input: JSON.stringify(payload), encoding: "utf8", windowsHide: true, timeout: 2500,
    });
    if (result.error || result.status !== 0) console.error("CFO native event could not be spooled");
  };
  pi.on("session_start", (_event, ctx) => fire("session_start", ctx));
  pi.on("agent_start", (_event, ctx) => { turn++; fire("agent_start", ctx); });
  pi.on("tool_execution_end", (_event, ctx) => fire("tool_execution_end", ctx));
  pi.on("agent_settled", (_event, ctx) => fire("agent_settled", ctx));
  pi.on("session_shutdown", (_event, ctx) => fire("session_shutdown", ctx));
}
`
}
