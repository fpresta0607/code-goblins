package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/nativehook"
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
	if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 {
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
	e, err := nativehook.Normalize(input, nativehook.Context{Harness: args[0], Role: os.Getenv("CFO_ROLE"), TaskID: os.Getenv("CFO_TASK_ID"), Generation: os.Getenv("CFO_SPAWN_GEN"), ParentSessionID: os.Getenv("CFO_PARENT_SESSION_ID"), ParentHarness: os.Getenv("CFO_PARENT_HARNESS"), RootSessionID: os.Getenv("CFO_ROOT_SESSION_ID")})
	if err == nil {
		err = nativehook.Spool(*dir, e)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// Codex Stop requires a JSON reply. This is also accepted by Claude and
	// ignored by the Pi notification extension. It never blocks continuation.
	fmt.Fprintln(stdout, "{}")
	return 0
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
	if err := f.Parse(args[2:]); err != nil || f.NArg() != 0 {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	probe := func(arg string) (string, error) {
		// Only fixed, whitelisted harness names and probe flags enter this
		// shell. PowerShell resolves both native executables and npm shims.
		cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "& "+name+" "+arg+"; exit $LASTEXITCODE")
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
		user, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		switch name {
		case "codex":
			*dir = filepath.Join(user, ".codex")
		case "claude":
			*dir = filepath.Join(user, ".claude")
		case "pi":
			*dir = filepath.Join(user, ".pi", "agent")
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
