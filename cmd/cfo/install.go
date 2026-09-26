package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/install"
)

// runInstall wires a CFO home into the machine so a Claude Code session
// opened in any repository is supervised by it: this checkout, or outside
// one a per-user home the binary sets up itself.
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

	root, checkout, err := installRoot()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	settings, err := install.UserSettingsPath()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	service := install.Service{
		Root:         root,
		UserSettings: settings,
		RepoSettings: filepath.Join(root, ".claude", "settings.json"),
		Env:          install.NewEnvStore(execx.OSRunner{}),
	}
	if !checkout {
		service.Contract, service.Policy = codegoblins.Contract, codegoblins.Policy
		if service.Binary, err = os.Executable(); err != nil {
			fmt.Fprintf(stderr, "cfo install: find the running binary: %v\n", err)
			return 1
		}
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
		fmt.Fprintf(stdout, "cfo install --uninstall: removing %s from this machine\n", root)
		if err := service.Uninstall(stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "Open a new terminal for the environment change to take effect.")
		return 0
	}

	fmt.Fprintf(stdout, "cfo install: wiring %s into this machine\n", root)
	if err := service.Install(stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "Open a new terminal for the environment change to take effect.")
	return 0
}

// installRoot is the home install wires in, decided by the working
// directory and never by whatever CFO_HOME already says: honoring a stale
// CFO_HOME here would make the one command that is supposed to repair the
// machine quietly confirm the broken value. Run from a code-goblins checkout
// it is that checkout; run anywhere else it is the per-user home under
// LOCALAPPDATA, which install sets up in full, so CFO_HOME never names a
// directory with no fleet in it and the hooks never go silently inert.
func installRoot() (root string, checkout bool, err error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", false, fmt.Errorf("cfo install: resolve the working directory: %w", err)
	}
	if root, err = fsx.AbsClean(wd); err != nil {
		return "", false, fmt.Errorf("cfo install: resolve the working directory: %w", err)
	}
	checkout = true
	for _, marker := range []string{"AGENTS.md", filepath.Join("cmd", "cfo")} {
		if _, err := os.Stat(filepath.Join(root, marker)); err != nil {
			checkout = false
		}
	}
	if checkout {
		return root, true, nil
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return "", false, fmt.Errorf("cfo install: %s is not a code-goblins checkout and LOCALAPPDATA is not set, so there is no per-user folder for a CFO home", root)
	}
	if root, err = fsx.AbsClean(filepath.Join(local, "CodeGoblins")); err != nil {
		return "", false, fmt.Errorf("cfo install: resolve the per-user home: %w", err)
	}
	return root, false, nil
}
