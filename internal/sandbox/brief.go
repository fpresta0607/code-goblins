// Package sandbox is the containment mode a task whose code is the adversary
// runs in: a brief that carries `sandbox: needed` keeps its goblin's harness
// on the host, in its worktree, and runs every command that executes the
// project's code inside one container, through goblins sandbox run, in the
// machine's heavy line. The host runs only ceo, git and gh for it, with the
// worktree's .git link checked and git's hooks, fsmonitor and in-tree
// attributes off, so nothing the container writes runs on the host.
package sandbox

import (
	"errors"
	"fmt"
	"strings"
)

// briefSection and briefKey are where a brief asks for the container: one
// `sandbox:` line under its Authentication heading, beside its credentials,
// mcp and connectors lines, since it too says what the goblin's work can
// reach.
const (
	briefSection = "Authentication"
	briefKey     = "sandbox"
	// Needed is the word a brief writes on its sandbox line.
	Needed = "needed"
)

// IsNeededBy reports whether brief asks for the container with
// `sandbox: needed`. A brief with no sandbox line runs as every task does. A
// line that says anything else, or a second line, is refused rather than
// guessed at.
func IsNeededBy(brief string) (bool, error) {
	section, isStated := "", false
	for _, line := range strings.Split(strings.ReplaceAll(brief, "\r\n", "\n"), "\n") {
		if heading, found := strings.CutPrefix(line, "## "); found {
			section = strings.TrimSpace(heading)
			continue
		}
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if section != briefSection || !found || !strings.EqualFold(strings.TrimSpace(key), briefKey) {
			continue
		}
		if isStated {
			return false, errors.New("the brief's " + briefSection + " section has more than one " + briefKey + " line")
		}
		isStated = true
		if value = strings.TrimSpace(value); !strings.EqualFold(value, Needed) {
			return false, fmt.Errorf("the brief's %s line says %q: write %s: %s to run its commands in a container, or no %s line to run them as every task does", briefKey, value, briefKey, Needed, briefKey)
		}
	}
	return isStated, nil
}
