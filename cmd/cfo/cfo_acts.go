package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// notTheCFO is why this process may not act as the CFO, or nil when it may. A
// test replaces it, since a test binary an agent runs has that agent's
// harness among its parents.
var notTheCFO = supervisor.NotTheCFO

// actsAsCFO reports whether the command line args asks for something that is
// the CFO's to decide for the fleet: dispatching a task (brief, spawn),
// steering one (send, switch, title), stopping or retiring one (pause,
// resume, kill, cleanup, supersede, backlog, a reap that acts), landing its
// work (pr, merge-local, deploy) and acknowledging the wake queue (a drain
// that acknowledges). Reading the fleet is anyone's: fleet-view, peek,
// runtime, tickets, a drain that only prints and a reap that only reports.
//
// The commands bound to the registered CFO by its process (answer, question,
// run-request, a review cleared, AFK mode at his ask) need no entry: only the
// CFO's own session registers. A goblin's commands for its own task (notify,
// helper, pipeline, worktree, services, present, deliver, review) are a
// goblin's, and are not the CFO's acts.
func actsAsCFO(args []string) bool {
	hasFlag := func(names ...string) bool {
		return slices.ContainsFunc(args[1:], func(arg string) bool {
			name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			return strings.HasPrefix(arg, "-") && slices.Contains(names, name)
		})
	}
	switch args[0] {
	case "brief", "spawn", "send", "switch", "title", "pause", "resume", "kill", "cleanup", "supersede", "backlog", "pr", "merge-local", "deploy":
		return true
	case "drain":
		return hasFlag("ack-through", "recovery-generation", "ack-blocking")
	case "reap":
		return hasFlag("apply", "force")
	}
	return false
}

// refuseAnotherAgentsSession is what cfo says instead of running a command
// that acts as the CFO in an agent's session that is not the CFO's own, and
// empty wherever the command may run. `cfo install` puts cfo on every
// session's PATH, and on 2026-10-09 a Claude Code session of the Overlord's
// own drained the fleet's wakes, acknowledged them and steered a goblin while
// the CFO held nothing.
func refuseAnotherAgentsSession(args []string, resolveHome func() (home.Home, error)) string {
	if !actsAsCFO(args) {
		return ""
	}
	// A home that cannot be resolved proves no session the CFO's own.
	stateDir := ""
	if resolveHome == nil {
		resolveHome = home.Resolve
	}
	if h, err := resolveHome(); err == nil {
		stateDir = h.State
	}
	why := notTheCFO(stateDir)
	if why == nil {
		return ""
	}
	return fmt.Sprintf("cfo %s: %s. Dispatching, steering, stopping and landing a goblin's work and acknowledging the fleet's wakes are the CFO's alone, so nothing was done. Ask the CFO, or run goblins in a terminal to start it or show it.", args[0], why)
}
