package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/install"
)

// runInstall wires this checkout into the machine so a Claude Code session
// opened in any repository is supervised by it.
//
// It is deliberately separate from `cfo doctor`: doctor reports, install
// repairs, and a command that silently changes a machine while claiming to
// inspect it is the kind of surprise that costs an adopter their trust.
//
//	cfo install
//	cfo install --uninstall
func runInstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	uninstall := fs.Bool("uninstall", false, "remove what cfo install added")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "cfo install: unexpected arguments")
		return 2
	}

	root, err := installRoot()
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

	if *uninstall {
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

// installRoot is the checkout install wires in: the working directory, not
// whatever CFO_HOME already says. An adopter runs this from the clone they
// want to use, and honoring a stale CFO_HOME here would make the one command
// that is supposed to repair the machine quietly confirm the broken value.
//
// It refuses anything that is not recognisably a code-goblins checkout,
// because the failure it prevents - CFO_HOME pointed at a directory with no
// fleet in it - is silent, and every hook downstream would just go inert.
func installRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cfo install: resolve the working directory: %w", err)
	}
	root, err := fsx.AbsClean(wd)
	if err != nil {
		return "", fmt.Errorf("cfo install: resolve the working directory: %w", err)
	}
	for _, marker := range []string{"AGENTS.md", filepath.Join("cmd", "cfo")} {
		if _, err := os.Stat(filepath.Join(root, marker)); err != nil {
			return "", fmt.Errorf("cfo install: %s is not a code-goblins checkout (no %s); run it from your clone", root, marker)
		}
	}
	return root, nil
}
