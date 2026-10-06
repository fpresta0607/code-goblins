package main

import (
	"context"
	"errors"
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
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/update"
)

// runInstall wires a CFO home into the machine so a Claude Code session
// opened in any repository is supervised by it: the home already in use where
// there is one, kept where it is, and otherwise the per-user home the binary
// sets up itself, the same one wherever it runs, a code-goblins checkout
// included. It never stops to ask: whatever the machine holds, it ends in a
// home that works, with a fleet already there and running kept as it is.
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

	target, err := installTarget()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	root := target.Root
	service, err := installService(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	service.Checkout = target.Checkout
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
	if target.Kept {
		fmt.Fprintf(stdout, "%s You already run Code Goblins from %s, so it is updated there and your goblins and their work stay as they are.\n", notePrefix, root)
	}
	fmt.Fprintf(stdout, "cfo install: wiring %s into this machine\n", root)
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	board, serving := servingBoard(h, stdout)
	if err := service.Install(stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if serving {
		board.restart(h, stdout)
	}
	fmt.Fprintln(stdout, "Open a new terminal for the environment change to take effect.")
	return 0
}

// notePrefix starts a line the install script shows the person as it is,
// among the details it otherwise keeps in its log.
const notePrefix = "Note:"

// runningBoard is a supervisor this home already runs, with the address it
// serves on and a copy of the program it runs, kept to fall back on.
type runningBoard struct {
	process  serveProcess
	address  string
	previous string
}

// servingBoard finds the supervisor this home already runs, proved this
// home's own, before an install replaces its program, and keeps a copy of
// that program beside the copies an update keeps.
func servingBoard(h home.Home, stdout io.Writer) (runningBoard, bool) {
	running, ok := homeSupervisor(h.State)
	if !ok || provedHomeSupervisor(h, running) != nil {
		return runningBoard{}, false
	}
	identity, err := proc.Identify(running.pid, running.start)
	if err != nil {
		return runningBoard{}, false
	}
	board := runningBoard{process: running, address: boardAddress(), previous: filepath.Join(update.Dir(h.State), "previous-goblins.exe")}
	if record, err := readBoardRecord(h.State); err == nil && record.PID == running.pid {
		board.address = strings.TrimPrefix(record.URL, "http://")
	}
	// An update that stopped part way keeps its own copies there as its way
	// back, which are never written over.
	if journal, err := update.ReadJournal(h.State); err == nil && !journal.Phase.Finished() || err != nil && !errors.Is(err, os.ErrNotExist) {
		board.previous = ""
	} else if err := copyProgram(identity.Image, board.previous); err != nil {
		fmt.Fprintf(stdout, "cfo install: could not keep a copy of the running board's program %s (%v), so the board is restarted with nothing to fall back on\n", identity.Image, err)
		board.previous = ""
	}
	return board, true
}

// restart brings the board onto the build just installed as cfo update does:
// it stops only the supervisor and starts it again from bin on the address
// it served, leaving every goblin's and the CFO's terminal as it is. A build
// that does not serve gives way to the program that ran before.
func (b runningBoard) restart(h home.Home, stdout io.Writer) {
	fmt.Fprintf(stdout, "cfo install: restarting the board (pid %d) on this build\n", b.process.pid)
	// The supervisors started here serve this home whatever this process's
	// environment names, which on a machine where only the user scope holds
	// CFO_HOME is nothing.
	h, err := pinHome(h)
	if err != nil {
		fmt.Fprintf(stdout, "%s The board keeps running its earlier build until Code Goblins next starts it: %v.\n", notePrefix, err)
		return
	}
	if err := endSupervisor(h, b.process); err != nil {
		fmt.Fprintf(stdout, "%s The board keeps running its earlier build until Code Goblins next starts it: %v.\n", notePrefix, err)
		return
	}
	started, err := startSupervisor(h, filepath.Join(h.Bin(), "goblins.exe"), b.address)
	if err == nil {
		if err = awaitSupervisor(h.State, started, true); err != nil && processIs(started) {
			_ = endSupervisor(h, started)
		}
	}
	if err == nil {
		fmt.Fprintf(stdout, "cfo install: the board (pid %d) serves this build\n", started.pid)
		_ = os.Remove(b.previous)
		return
	}
	if b.previous == "" {
		fmt.Fprintf(stdout, "%s The board did not start on this build (%v); opening Code Goblins starts it again.\n", notePrefix, err)
		return
	}
	earlier, previousErr := startSupervisor(h, b.previous, b.address)
	if previousErr == nil {
		previousErr = awaitSupervisor(h.State, earlier, false)
	}
	if previousErr != nil {
		fmt.Fprintf(stdout, "%s The board did not start on this build (%v) or on the one before (%v); opening Code Goblins starts it again.\n", notePrefix, err, previousErr)
		return
	}
	fmt.Fprintf(stdout, "%s The board did not start on this build (%v), so it runs the one before again.\n", notePrefix, err)
}

// copyProgram copies the program at from to to.
func copyProgram(from, to string) error {
	data, err := fsx.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o755)
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

// installTarget is the home install wires in, as install.FindTarget picks it:
// the home CFO_HOME names where a fleet lives there, kept where it is, and
// otherwise the per-user home. A CFO_HOME naming a folder with no fleet never
// decides it, so the one command that is supposed to repair the machine never
// confirms a broken value, and the working directory never decides it either:
// an install run from a code-goblins checkout sets up the same home the
// desktop installer does.
func installTarget() (install.Target, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return install.Target{}, fmt.Errorf("cfo install: LOCALAPPDATA is not set, so there is no per-user folder for a CFO home")
	}
	standard, err := fsx.AbsClean(filepath.Join(local, "CodeGoblins"))
	if err != nil {
		return install.Target{}, fmt.Errorf("cfo install: resolve the per-user home: %w", err)
	}
	return install.FindTarget(install.NewEnvStore(execx.OSRunner{}), standard)
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
