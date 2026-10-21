package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func runNativeHook(args []string, input io.Reader, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "usage: cfo native-hook <claude|codex|pi> [--home <dir> --state <dir>]")
		return 2
	}
	f := flag.NewFlagSet("native-hook", flag.ContinueOnError)
	f.SetOutput(stderr)
	root := f.String("home", "", "fleet home")
	dir := f.String("state", "", "fleet state")
	isKeyAlarm := f.Bool("key-alarm", false, "also wake the CFO when a goblin's tool output holds a secret key, as cfo hook goblin-key-alarm does")
	if !parseFlags(f, args[1:]) {
		return 2
	}
	if *root == "" || *dir == "" {
		h, err := runtime.resolveHome()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if *root == "" {
			*root = h.Root
		}
		if *dir == "" {
			*dir = h.State
		}
	}
	if !filepath.IsAbs(*root) || !filepath.IsAbs(*dir) {
		fmt.Fprintln(stderr, "hook home and state must be absolute")
		return 2
	}
	// A goblin's tool call ran the native hook and the key alarm as two
	// hooks, two cfo.exe processes, each with a console host of its own; one
	// process runs both now, each part still held to its own time limit.
	if *isKeyAlarm {
		payload, err := io.ReadAll(input)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return runHookParts(stdout, stderr,
			hookPart{name: "the native hook", limit: nativeHookLimit, run: func(stdout, stderr io.Writer) int {
				return nativeHookEvent(args[0], *root, *dir, bytes.NewReader(payload), stdout, stderr)
			}},
			hookPart{name: "the key alarm", limit: keyAlarmLimit, run: func(stdout, stderr io.Writer) int {
				return runHook("goblin-key-alarm", bytes.NewReader(payload), stdout, stderr)
			}},
		)
	}
	return nativeHookEvent(args[0], *root, *dir, input, stdout, stderr)
}

// nativeHookEvent reports one event of a harness's native hook, read from
// input, to the supervisor of the home at root, whose state is in dir. It
// spools the event and returns: what a Codex or pi CFO's turn end or
// compaction asks of the fleet, the supervisor does as it ingests the event
// (supervisor.Service.followNativeCFO), so the harness never waits on it.
func nativeHookEvent(harnessName, root, dir string, input io.Reader, stdout, stderr io.Writer) int {
	e, err := nativehook.Normalize(input, nativehook.Context{Harness: harnessName, Role: os.Getenv("CFO_ROLE"), TaskID: os.Getenv("CFO_TASK_ID"), Generation: os.Getenv("CFO_SPAWN_GEN"), ParentSessionID: os.Getenv("CFO_PARENT_SESSION_ID"), ParentHarness: os.Getenv("CFO_PARENT_HARNESS"), RootSessionID: os.Getenv("CFO_ROOT_SESSION_ID"), HostID: os.Getenv(host.IDVariable)})
	if errors.Is(err, nativehook.ErrUnstartedSubagent) {
		fmt.Fprintln(stdout, "{}")
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := nativehook.Spool(dir, e); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// Codex Stop requires a JSON reply. This is also accepted by Claude and
	// ignored by the Pi notification extension. It never blocks continuation.
	// The role cfo says only that the session is no goblin's, so the CFO's
	// continuation goes to the CFO's own session alone: a Codex session the
	// Overlord opens himself is told nothing of the fleet.
	if e.Harness == "codex" && e.Kind == "started" && e.Source == "compact" && e.Role == "cfo" && e.TaskID == "" && isOwnSession(dir) {
		var reply struct {
			Output struct {
				Event   string `json:"hookEventName"`
				Context string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		reply.Output.Event = "SessionStart"
		reply.Output.Context = fmt.Sprintf("CFO compact continuation: before further fleet action, read %s, %s and %s. Read the saved handoff for each active item and use cfo fleet-view to reconcile current work. Preserve held and answered states; recover only durable on-disk state. Run cfo drain and ack only wakes actually read. Use the existing stow contract before another context reset. This hook has not read those files; conversational holds absent from disk cannot be recovered.", filepath.Join(root, "AGENTS.md"), filepath.Join(root, "data", "overlord.md"), filepath.Join(root, "data", "memory", "MEMORY.md"))
		if err := json.NewEncoder(stdout).Encode(reply); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	} else {
		fmt.Fprintln(stdout, "{}")
	}
	// Claude registers from its own SessionStart hook, after the digest has
	// settled custody. Codex and Pi have only this one, and a failure here
	// shows on the board as the registration state rather than in the reply.
	// It stays in the hook: only a process in the CFO's own session can prove
	// it is one.
	if e.Kind == "started" && e.Role == "cfo" && e.TaskID == "" && (e.Harness == "codex" || e.Harness == "pi") && os.Getenv(host.IDVariable) != "" {
		_, _ = supervisor.Register(dir, e.Harness, e.SessionID)
	}
	return 0
}

// isOwnSession reports whether this process runs in the CFO's own session of
// the home whose state folder is stateDir.
func isOwnSession(stateDir string) bool {
	_, err := supervisor.OwnSession(stateDir)
	return err == nil
}

// compactNativeCFO gives a Codex or pi CFO what Claude Code's pre-compact hook
// and SessionStart digest give a Claude CFO: a checkpoint written as the
// compaction begins, then one wake through the queue pointing at it. cfo serve
// hands it to the supervisor, which calls it for the compaction events of the
// registered CFO's own session while that CFO holds the home, so a goblin's or
// another session's compaction neither overwrites the CFO's checkpoint nor
// wakes it.
func compactNativeCFO(h home.Home, e nativehook.Event, now time.Time) error {
	if e.Kind == "compacting" {
		// One that cannot be written leaves the wake after the compaction
		// saying none is fresh.
		return digest.WriteCheckpoint(h, now)
	}
	// The checkpoint's write time tells a replayed event from the next
	// compaction, which writes a new one first.
	path, written, isFresh := digest.FreshCheckpoint(h.State, now)
	identity := fmt.Sprintf("compact/%s/%s/%d", e.Harness, e.SessionID, written.UnixNano())
	detail := fmt.Sprintf("compacted: the %s CFO's context was just compacted; read %s, written %s as it began, then cfo fleet-view and cfo drain before any fleet action", e.Harness, path, written.UTC().Format(time.RFC3339))
	if !isFresh {
		identity = fmt.Sprintf("compact/%s/%s/%s", e.Harness, e.SessionID, e.TurnID)
		detail = fmt.Sprintf("compacted: the %s CFO's context was just compacted and no fresh checkpoint was written for it; reconcile from cfo fleet-view and cfo drain before any fleet action", e.Harness)
	}
	if _, err := wake.AppendOnce(h.State, identity, "check", "compact", detail); err != nil {
		return err
	}
	_, err := wake.PublishEpisode(h.State)
	return err
}

func runNativeSetup(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) < 2 || (args[0] != "check" && args[0] != "install") {
		fmt.Fprintln(stderr, "usage: cfo hooks check|install <claude|codex|pi> [--config-dir <dir>]")
		return 2
	}
	name := args[1]
	if name != "claude" && name != "codex" && name != "pi" {
		fmt.Fprintln(stderr, "unsupported native harness")
		return 2
	}
	f := flag.NewFlagSet("hooks", flag.ContinueOnError)
	f.SetOutput(stderr)
	dir := f.String("config-dir", "", "harness user configuration directory")
	if !parseFlags(f, args[2:]) {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	probe := func(arg string) (string, error) {
		// Only fixed, whitelisted harness names and probe flags enter this
		// shell. PowerShell resolves both native executables and npm shims,
		// and a shim is a script that a Restricted policy would refuse.
		cmd := execx.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "& "+name+" "+arg+"; exit $LASTEXITCODE")
		data, err := cmd.Output()
		return string(data), err
	}
	v, err := probe("--version")
	if err != nil {
		fmt.Fprintf(stderr, "%s is unavailable: %v\n", name, err)
		return 1
	}
	help := ""
	if name == "codex" {
		help, err = probe("--help")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if err := nativehook.CheckCapability(name, v, help); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s native contract verified (%s)\n", name, strings.TrimSpace(v))
	if args[0] == "check" {
		return 0
	}
	if *dir == "" {
		if *dir, err = harnessConfigDir(name); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	path, err := nativehook.Install(nativehook.InstallConfig{Harness: name, ConfigDir: *dir, Executable: exe, Home: h.Root, State: h.State})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "installed %s\n", path)
	if name == "codex" {
		fmt.Fprintln(stdout, "Review these exact hook definitions in Codex /hooks before they can run. Hook trust is unchanged.")
	}
	return 0
}

// harnessConfigDir is where `cfo hooks install` writes a harness's native
// hooks unless --config-dir says otherwise, and so where an uninstall looks.
func harnessConfigDir(name string) (string, error) {
	user, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch name {
	case "codex":
		return filepath.Join(user, ".codex"), nil
	case "claude":
		return filepath.Join(user, ".claude"), nil
	}
	return filepath.Join(user, ".pi", "agent"), nil
}
