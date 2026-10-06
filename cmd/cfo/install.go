package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harnessmap"
	"github.com/fpresta0607/code-goblins/internal/install"
)

// runInstall wires a CFO home into the machine so a Claude Code session
// opened in any repository is supervised by it: the per-user home the binary
// sets up itself, the same one wherever it runs, a code-goblins checkout
// included.
//
// It is deliberately separate from `cfo doctor`: doctor reports, install
// repairs, and a command that silently changes a machine while claiming to
// inspect it is the kind of surprise that costs an adopter their trust.
//
//	cfo install
//	cfo install --projects-root <dir>
//	cfo install --uninstall
func runInstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	uninstall := fs.Bool("uninstall", false, "remove what cfo install added")
	projectsRoot := fs.String("projects-root", "", "the folder that holds your checkouts, so --project can take a bare name")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "cfo install: unexpected arguments")
		return 2
	}
	if *uninstall && *projectsRoot != "" {
		fmt.Fprintln(stderr, "cfo install: --projects-root cannot be combined with --uninstall")
		return 2
	}

	root, err := installRoot()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	service, err := installService(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *projectsRoot != "" {
		if service.ProjectsRoot, err = fsx.AbsClean(*projectsRoot); err != nil {
			fmt.Fprintf(stderr, "cfo install: resolve --projects-root: %v\n", err)
			return 1
		}
	}

	if file := os.Getenv(install.UserEnvFileVariable); file != "" {
		fmt.Fprintf(stdout, "cfo install: the user environment is %s, not this machine's (%s is set)\n", file, install.UserEnvFileVariable)
	}
	if *uninstall {
		service.HarnessDirs = map[string]string{}
		for _, harness := range []string{"claude", "codex", "pi"} {
			if service.HarnessDirs[harness], err = harnessConfigDir(harness); err != nil {
				fmt.Fprintf(stderr, "cfo install --uninstall: find the %s configuration: %v\n", harness, err)
				return 1
			}
		}
		service.StartMenuShortcut = install.StartMenuShortcutPath()
		fmt.Fprintf(stdout, "cfo install --uninstall: removing %s from this machine\n", root)
		if err := service.Uninstall(stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "Open a new terminal for the environment change to take effect.")
		return 0
	}

	service.EarlierWindow = install.EarlierWindowDir()
	fmt.Fprintf(stdout, "cfo install: wiring %s into this machine\n", root)
	if err := service.Install(stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "Open a new terminal for the environment change to take effect.")
	return 0
}

// installService is the install of this binary into the home at root, against
// this machine's user environment, Claude Code settings and harness folders.
func installService(root string) (install.Service, error) {
	settings, err := install.UserSettingsPath()
	if err != nil {
		return install.Service{}, err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return install.Service{}, fmt.Errorf("cfo install: find the user's profile folder: %w", err)
	}
	skills, err := iofs.Sub(codegoblins.Skills, ".agents/skills")
	if err != nil {
		return install.Service{}, fmt.Errorf("cfo install: read the skills this build ships: %w", err)
	}
	binary, err := os.Executable()
	if err != nil {
		return install.Service{}, fmt.Errorf("cfo install: find the running binary: %w", err)
	}
	commands := execx.OSRunner{}
	return install.Service{
		Root:         root,
		UserSettings: settings,
		RepoSettings: filepath.Join(root, ".claude", "settings.json"),
		Env:          install.NewEnvStore(commands),
		Contract:     codegoblins.Contract,
		Policy:       codegoblins.Policy,
		Skills:       skills,
		Binary:       binary,
		Harnesses:    harnessmap.Find(os.Getenv, userHome),
		Link:         junction(commands),
		// The desktop window's Start at login entry, which an uninstall
		// removes and an install takes over from an earlier copy.
		StartAtLoginKey: install.StartAtLoginKey,
	}, nil
}

// installRoot is the home install wires in: CFO_HOME's own value is never
// consulted, because honoring a stale CFO_HOME here would make the one command
// that is supposed to repair the machine quietly confirm the broken value, and
// the working directory never decides it either: a code-goblins checkout is
// source, never a home, so an install run from one sets up the same per-user
// home the desktop installer does.
func installRoot() (string, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return "", fmt.Errorf("cfo install: LOCALAPPDATA is not set, so there is no per-user folder for a CFO home")
	}
	root, err := fsx.AbsClean(filepath.Join(local, "CodeGoblins"))
	if err != nil {
		return "", fmt.Errorf("cfo install: resolve the per-user home: %w", err)
	}
	return root, nil
}

// junction links a directory through cmd's mklink /J, which needs no
// privilege where a symbolic link would.
func junction(commands execx.Runner) harnessmap.Linker {
	return func(link, target string) error {
		result, err := commands.Run(context.Background(), execx.Request{Name: "cmd", Args: []string{"/c", "mklink", "/J", link, target}})
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("mklink /J exited with code %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stdout)+string(result.Stderr)))
		}
		return nil
	}
}
