package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/terminal"
)

// cfoHarnesses are the harnesses goblins can start the CFO as.
var cfoHarnesses = []string{"claude", "codex", "pi"}

// cfoTypedLaunchSettle is how long a typed CFO launch line settles in the
// cfo tab's shell before Enter submits it, as a goblin's typed launch does.
const cfoTypedLaunchSettle = 300 * time.Millisecond

// A typed CFO has cfoLaunchTries looks, cfoLaunchPoll apart, to show in
// Herdr: the budget a goblin spawn's confirmLaunch has. cfoLaunchSleep is how
// a typed CFO start waits.
var (
	cfoLaunchPoll  = 1500 * time.Millisecond
	cfoLaunchTries = 80
	cfoLaunchSleep = time.Sleep
)

// cfoHarnessPath is where a CFO home remembers the harness goblins starts the
// CFO as.
func cfoHarnessPath(stateDir string) string {
	return filepath.Join(stateDir, "cfo-harness")
}

// cfoHarness is the harness goblins starts the CFO as: the one last chosen
// with --harness, or claude.
func cfoHarness(stateDir string) (string, error) {
	data, err := os.ReadFile(cfoHarnessPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return "claude", nil
	}
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(data))
	if !slices.Contains(cfoHarnesses, name) {
		return "", fmt.Errorf("%s names %q, which is not claude, codex or pi; choose again with goblins --harness", cfoHarnessPath(stateDir), name)
	}
	return name, nil
}

// startCFOSession brings the CFO's session to the front. A CFO registered in
// a native terminal is shown in this terminal. A CFO whose registration names
// a live process in Herdr is brought to the front where it registered. With
// no CFO registered, native terminal cfo is shown while its host answers,
// since the CFO started there may not have registered yet; otherwise the CFO
// is started as the harness this home remembers, in the project this terminal
// is in or one the Overlord picks: in a native terminal shown in this one when
// native is set, or else in Herdr. Then the terminal is handed to herdr, which
// attaches with the CFO's tab in front. Inside a Herdr pane there is nothing
// to attach. A harness chosen is remembered first, and a CFO already running
// keeps the harness it runs.
func startCFOSession(ctx context.Context, runtime commandRuntime, stateDir string, native bool, chosen string, stdout, stderr io.Writer) int {
	if chosen != "" {
		if _, err := exec.LookPath(chosen); err != nil {
			fmt.Fprintf(stderr, "goblins: %s is not on PATH, so the CFO cannot start as it; install it or choose another with goblins --harness\n", chosen)
			return 1
		}
		if err := fsx.AtomicWriteFile(cfoHarnessPath(stateDir), []byte(chosen+"\n")); err != nil {
			fmt.Fprintf(stderr, "goblins: the harness choice could not be saved: %v\n", err)
			return 1
		}
	}
	keeps := func() {
		if chosen != "" {
			fmt.Fprintf(stdout, "\nThe CFO already runs, and keeps its harness; %s is the harness goblins starts the next CFO as.\n", chosen)
		}
	}
	if id, live := runtime.nativeCFO(stateDir); live {
		keeps()
		return runtime.attachNative(stateDir, id, stdout, stderr)
	}
	session := herdrSession()
	if endpoint, live := runtime.liveCFO(stateDir); live {
		keeps()
		if err := runtime.focusCFO(ctx, endpoint); err != nil {
			fmt.Fprintf(stderr, "goblins: the CFO could not be brought to the front in Herdr: %v\n", err)
			return 1
		}
		session = endpoint.Target.Session
	} else {
		if runtime.nativeTerminalRuns(stateDir, supervisor.NativeCFOTerminal) {
			keeps()
			fmt.Fprintf(stdout, "\nThe CFO is already running in native terminal %s.\n", supervisor.NativeCFOTerminal)
			return runtime.attachNative(stateDir, supervisor.NativeCFOTerminal, stdout, stderr)
		}
		harness, err := cfoHarness(stateDir)
		if err != nil {
			fmt.Fprintf(stderr, "goblins: %v\n", err)
			return 1
		}
		project, err := pickProject(ctx, runtime, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "goblins: %v\n", err)
			return 1
		}
		if native {
			if err := runtime.startNativeCFO(stateDir, project, harness); err != nil {
				fmt.Fprintf(stderr, "goblins: the CFO could not be started in a native terminal: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "\nThe CFO starts as %s in %s, in native terminal %s.\n", harness, project, supervisor.NativeCFOTerminal)
			warnNoWakePath(stdout, harness)
			return runtime.attachNative(stateDir, supervisor.NativeCFOTerminal, stdout, stderr)
		}
		started, err := runtime.startCFO(ctx, project, harness)
		if err != nil {
			fmt.Fprintf(stderr, "goblins: the CFO session could not be started in Herdr: %v\n", err)
			return 1
		}
		if started {
			fmt.Fprintf(stdout, "\nThe CFO starts as %s in %s.\n", harness, project)
			warnNoWakePath(stdout, harness)
		} else {
			keeps()
			fmt.Fprintln(stdout, "\nThe CFO is already running in Herdr's cfo tab.")
		}
	}
	if os.Getenv("HERDR_PANE_ID") != "" {
		fmt.Fprintln(stdout, "The CFO is in front in Herdr.")
		return 0
	}
	return runtime.attachHerdr(session)
}

// warnNoWakePath says what a CFO that is not Claude Code goes without: only
// Claude Code's Stop hook wakes the CFO when a goblin reports, so a Codex or
// pi CFO learns of reports only when it looks.
func warnNoWakePath(stdout io.Writer, harness string) {
	if harness != "claude" {
		fmt.Fprintf(stdout, "A %s CFO has no wake path: only Claude Code's Stop hook wakes the CFO when a goblin reports, so it sees reports only when it checks the board or runs cfo drain.\n", harness)
	}
}

// pickProject is the git checkout this terminal is in, or else one of the
// checkouts under the projects root: the only one, or the one the Overlord
// picks by number.
func pickProject(ctx context.Context, runtime commandRuntime, stdout io.Writer) (string, error) {
	if top, err := runtime.gitTop(ctx); err == nil && top != "" {
		return top, nil
	}
	var root string
	var err error
	if runtime.projectsRoot != nil {
		root, err = runtime.projectsRoot()
	}
	if err != nil || root == "" {
		return "", errors.New("this terminal is in no git checkout and no projects root is set: cd into a project, or record the folder that holds them with cfo install --projects-root <dir>")
	}
	checkouts, err := supervisor.ProjectCheckouts(root)
	if err != nil {
		return "", fmt.Errorf("read the projects root %s: %w", root, err)
	}
	switch len(checkouts) {
	case 0:
		return "", fmt.Errorf("no git checkout under the projects root %s: cd into a project first", root)
	case 1:
		return checkouts[0], nil
	}
	fmt.Fprintln(stdout, "\nPick the project the CFO starts in:")
	for index, checkout := range checkouts {
		fmt.Fprintf(stdout, "  %d  %s\n", index+1, filepath.Base(checkout))
	}
	fmt.Fprint(stdout, "Project number: ")
	line, err := bufio.NewReader(runtime.stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no project was picked")
	}
	number, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || number < 1 || number > len(checkouts) {
		return "", fmt.Errorf("%q is not one of the project numbers 1 to %d", strings.TrimSpace(line), len(checkouts))
	}
	return checkouts[number-1], nil
}

// gitTop is the git checkout the working directory is in.
func gitTop(ctx context.Context) (string, error) {
	result, err := execx.OSRunner{}.Run(ctx, execx.Request{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}})
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", errors.New("not a git checkout")
	}
	return filepath.Clean(strings.TrimSpace(string(result.Stdout))), nil
}

// startCFOInHerdr starts the CFO as harness in the fleet's own Herdr session,
// and reports whether it started one.
func startCFOInHerdr(ctx context.Context, project, harness string) (bool, error) {
	return startCFOWith(ctx, &herdr.Client{Commands: execx.OSRunner{}, Session: herdrSession()}, project, harness, os.Stdout)
}

// focusCFOInHerdr brings a live CFO's workspace and tab to the front in the
// session it registered in.
func focusCFOInHerdr(ctx context.Context, endpoint herdr.Endpoint) error {
	return (&herdr.Client{Commands: execx.OSRunner{}, Session: endpoint.Target.Session}).Focus(ctx, endpoint)
}

// startCFOWith makes sure Herdr's server runs, finds the CFO's tab in the
// fleet workspace or creates a fresh one in project, starts harness there as
// the CFO unless an agent already runs in it, and brings the tab to the
// front. It reports whether it started a CFO. Claude Code starts through
// herdr agent start; codex and pi, npm script shims Herdr's Windows agent
// start cannot run, are typed into the tab's shell as a goblin's typed launch
// is. A typed CFO Herdr never sees start is said on stdout, and the tab is
// still brought to the front.
func startCFOWith(ctx context.Context, client terminal.Backend, project, harness string, stdout io.Writer) (bool, error) {
	if err := client.EnsureServer(ctx); err != nil {
		return false, err
	}
	container, err := client.EnsureContainer(ctx, project)
	if err != nil {
		return false, err
	}
	endpoint, running, err := client.CFOTab(ctx, container, project)
	if err != nil {
		return false, err
	}
	switch {
	case running:
	case harness == "claude":
		if err := client.AgentStart(ctx, endpoint.Target, "cfo", harness, nil); err != nil {
			return false, err
		}
	default:
		// No environment or arguments are typed: the CFO has none, as the claude CFO has none.
		if err := client.SendLiteral(ctx, endpoint.Target, harness); err != nil {
			return false, err
		}
		cfoLaunchSleep(cfoTypedLaunchSettle)
		if err := client.SendKey(ctx, endpoint.Target, "Enter"); err != nil {
			return false, err
		}
		// Startup dialogs are not confirmed: the Overlord, who is at the terminal, answers them.
		cfoLaunchSleep(cfoTypedLaunchSettle)
		// No brief is delivered and no working state awaited: the CFO has no brief and waits for the Overlord.
		seen, err := awaitTypedCFO(ctx, client, endpoint.Target, project, harness)
		if err != nil {
			return false, err
		}
		// A CFO Herdr never saw is not failed, as a goblin whose pane is not provably dead is not: the Overlord looks at the tab.
		if !seen {
			fmt.Fprintf(stdout, "\nHerdr never saw %s start in the cfo tab; look at the tab to see what it shows.\n", harness)
		}
	}
	return !running, client.Focus(ctx, endpoint)
}

// awaitTypedCFO looks, up to cfoLaunchTries times, for a typed CFO in its
// pane, and reports whether it saw one: Herdr holding an agent there, or a
// pane Herdr's detection missed whose harness runs, which it tells Herdr of
// as a goblin's typed launch does, so a later goblins finds an agent in the
// cfo tab rather than starting a second CFO. A look Herdr cannot answer sees
// nothing.
func awaitTypedCFO(ctx context.Context, client terminal.Backend, target herdr.Target, project, harness string) (bool, error) {
	for attempt := 0; attempt < cfoLaunchTries; attempt++ {
		if attempt > 0 {
			cfoLaunchSleep(cfoLaunchPoll)
		}
		status, err := client.AgentStatus(ctx, target)
		if err != nil {
			continue
		}
		if status != herdr.AgentDead {
			return true, nil
		}
		running, err := client.HarnessRunning(ctx, target)
		if err != nil || !running {
			continue
		}
		if err := client.ReportAgent(ctx, target, harness, "unknown", "cfo", project); err != nil {
			return false, fmt.Errorf("register the undetected %s CFO with herdr: %w", harness, err)
		}
		return true, nil
	}
	return false, nil
}

// attachHerdr hands this terminal to herdr, which attaches to session, and
// returns its exit code when the Overlord leaves it.
func attachHerdr(session string) int {
	command := exec.Command("herdr", "--session", session)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "goblins: herdr could not be started: %v\n", err)
		return 1
	}
	return 0
}
