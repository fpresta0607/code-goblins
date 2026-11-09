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

// ceoHarnesses are the harnesses goblins can start the CEO as.
var ceoHarnesses = []string{"claude", "codex", "pi"}

// ceoHarnessPath is where a CEO home remembers the harness goblins starts the
// CEO as.
func ceoHarnessPath(stateDir string) string {
	return filepath.Join(stateDir, "ceo-harness")
}

// ceoHarness is the harness goblins starts the CEO as: the harness of the
// saved CEO engine where one is saved and can be read, or the one an explicit
// start chose over it since, else the one last chosen with --harness, else
// claude. A selection the fleet cannot read names no harness, so the home
// starts as it last ran and the board reports the selection's own problem
// rather than a CEO that cannot start.
func ceoHarness(stateDir string) (string, error) {
	choice, err := supervisor.ReadCEOEngine(stateDir)
	if err == nil && choice.Overridden != "" {
		return choice.Overridden, nil
	}
	if err == nil {
		return choice.Harness, nil
	}
	data, err := fsx.ReadFile(ceoHarnessPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return "claude", nil
	}
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(data))
	if !slices.Contains(ceoHarnesses, name) {
		return "", fmt.Errorf("%s names %q, which is not claude, codex or pi; choose again with goblins --harness", ceoHarnessPath(stateDir), name)
	}
	return name, nil
}

// wakePath says how a CEO that is not Claude Code learns a goblin reported.
// Claude Code's own Stop hook wakes it, and needs no word. A Codex or pi CEO,
// which goblins starts in a native terminal for this, is woken by a line the
// supervisor types into that terminal while it sits idle, once its first
// prompt has registered it.
func wakePath(harness string) []string {
	if supervisor.CEOWakeFor(harness) != supervisor.CEOWakeTyped {
		return nil
	}
	return []string{fmt.Sprintf("A %s CEO is woken by one line typed into this terminal while it sits idle at an empty prompt, once its first prompt has run ceo register.", onboarding.Name(harness))}
}

// recommendedCEOAgent is the agent the table of what is proved recommends for
// the CEO, and ceoAgentNotes the few words it says of each.
func recommendedCEOAgent() string {
	for _, capability := range supervisor.CEOCapabilities() {
		if capability.Recommended {
			return capability.Agent
		}
	}
	return ""
}

func ceoAgentNotes() map[string]string {
	notes := map[string]string{}
	for _, capability := range supervisor.CEOCapabilities() {
		notes[capability.Agent] = capability.Note
	}
	return notes
}

// focusCEOInHerdr brings a live CEO's workspace and tab to the front in the
// session it registered in.
func focusCEOInHerdr(ctx context.Context, endpoint herdr.Endpoint) error {
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
