package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
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
	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !*uninstall {
		executable, executableErr := os.Executable()
		if executableErr != nil {
			fmt.Fprintln(stderr, executableErr)
			return 1
		}
		if err = prepareRuntimeHome(root, h, executable); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	service := install.Service{
		Root:         h.Root,
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

// Only generic instructions, policy and the executable seed a fresh runtime
// home. Existing operator data is neither imported from source nor overwritten.
func prepareRuntimeHome(source string, h home.Home, executable string) error {
	for _, dir := range []string{h.State, h.Data, filepath.Join(h.Root, "config")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", filepath.Join("config", "pipeline.json")} {
		dest := filepath.Join(h.Root, name)
		if _, err := os.Stat(dest); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			return err
		}
		if err = fsx.AtomicWriteFile(dest, data); err != nil {
			return err
		}
	}
	for _, subdir := range []string{filepath.Join(".agents", "skills"), "docs"} {
		err := filepath.WalkDir(filepath.Join(source, subdir), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			_, linkErr := os.Readlink(path)
			if entry.Type()&os.ModeSymlink != 0 || linkErr == nil {
				return fmt.Errorf("install: instruction bundle contains a link: %s", path)
			}
			rel, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			dest := filepath.Join(h.Root, rel)
			if _, linkErr = os.Readlink(dest); linkErr == nil {
				return fmt.Errorf("install: existing instruction path is a link; reconcile it explicitly: %s", dest)
			}
			if entry.IsDir() {
				return os.MkdirAll(dest, 0700)
			}
			if _, err = os.Stat(dest); err == nil {
				return nil
			} else if !os.IsNotExist(err) {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return fsx.AtomicWriteFile(dest, data)
		})
		if err != nil {
			return err
		}
	}
	dest := filepath.Join(h.Root, "cfo.exe")
	if !fsx.SamePath(executable, dest) {
		data, err := os.ReadFile(executable)
		if err != nil {
			return err
		}
		if err = fsx.AtomicWriteFile(dest, data); err != nil {
			return err
		}
	}
	return fsx.AtomicWriteFile(filepath.Join(h.Root, ".cfo-home"), []byte("cfo-home.v1\n"))
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
