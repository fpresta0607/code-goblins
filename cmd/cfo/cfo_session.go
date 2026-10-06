package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// cfoHarnesses are the harnesses goblins can start the CFO as.
var cfoHarnesses = []string{"claude", "codex", "pi"}

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
// Claude Code's own Stop hook wakes it, and needs no word. A Codex or pi CFO,
// which goblins starts in a native terminal for this, is woken by a line the
// supervisor types into that terminal while it sits idle, once its first
// prompt has registered it.
func wakePath(harness string) []string {
	if supervisor.CFOWakeFor(harness) != supervisor.CFOWakeTyped {
		return nil
	}
	return []string{fmt.Sprintf("A %s CFO is woken by one line typed into this terminal while it sits idle at an empty prompt, once its first prompt has run cfo register.", onboarding.Name(harness))}
}

// recommendedCFOAgent is the agent the table of what is proved recommends for
// the CFO, and cfoAgentNotes the few words it says of each.
func recommendedCFOAgent() string {
	for _, capability := range supervisor.CFOCapabilities() {
		if capability.Recommended {
			return capability.Agent
		}
	}
	return ""
}

func cfoAgentNotes() map[string]string {
	notes := map[string]string{}
	for _, capability := range supervisor.CFOCapabilities() {
		notes[capability.Agent] = capability.Note
	}
	return notes
}

// focusCFOInHerdr brings a live CFO's workspace and tab to the front in the
// session it registered in.
func focusCFOInHerdr(ctx context.Context, endpoint herdr.Endpoint) error {
	return (&herdr.Client{Commands: execx.OSRunner{}, Session: endpoint.Target.Session}).Focus(ctx, endpoint)
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
