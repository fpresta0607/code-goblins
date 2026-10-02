package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
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
	data, err := fsx.ReadFile(cfoHarnessPath(stateDir))
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

// wakePath says how a CFO that is not Claude Code learns a goblin reported.
// Claude Code's own Stop hook wakes it, and needs no word. A Codex or pi CFO
// is woken by a line the supervisor types into its native terminal while it
// sits idle, once it has registered; one in Herdr learns of reports only when
// it looks.
func wakePath(harness string, native bool) []string {
	switch wake := supervisor.CFOWakeFor(harness); {
	case wake == supervisor.CFOWakeTyped && native:
		return []string{fmt.Sprintf("A %s CFO is woken by one line typed into this terminal while it sits idle at an empty prompt, once it has run cfo register in this terminal.", onboarding.Name(harness))}
	case wake == supervisor.CFOWakeTyped:
		return []string{fmt.Sprintf("A %s CFO has no wake path in Herdr: a wake is typed only into a native terminal (goblins --native), so here it sees reports only when it checks the board or runs cfo drain.", onboarding.Name(harness))}
	}
	return nil
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
	command := execx.Command("herdr", "--session", session)
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
