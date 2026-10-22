package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/starthook"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const startHookUsage = "usage: cfo start-hook accept <task-id> <file> [--fleet]"

// runStartHook accepts a start hook the CFO judged safe, one a goblin's seal
// stopped (startHookRefusal): file, a path in task's worktree, by its name,
// its exact content and the distribution whose RECORD installs it, for the
// goblins of task's project, or of every project with --fleet. Every later
// seal of those goblins lets that file through, and still stops one that
// differs in any of the three.
func runStartHook(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 || args[0] != "accept" {
		fmt.Fprintln(stderr, startHookUsage)
		return 2
	}
	flags := flag.NewFlagSet("cfo start-hook accept", flag.ContinueOnError)
	flags.SetOutput(stderr)
	isFleet := flags.Bool("fleet", false, "accept it for every project's goblins, not only for the goblins of the task's project")
	var positional []string
	rest := args[1:]
	for len(rest) > 0 {
		if err := flags.Parse(rest); err != nil {
			return 2
		}
		rest = flags.Args()
		if len(rest) > 0 {
			positional = append(positional, rest[0])
			rest = rest[1:]
		}
	}
	if len(positional) != 2 {
		fmt.Fprintln(stderr, startHookUsage)
		return 2
	}
	task, file := positional[0], positional[1]
	if err := state.ValidTaskID(task); err != nil {
		fmt.Fprintf(stderr, "cfo start-hook: %v\n", err)
		return 2
	}
	// A goblin that accepted a start hook would let through what stopped
	// it, so only the CFO accepts one, on his own judgment.
	if os.Getenv(harness.RoleVariable) == harness.RoleGoblin || os.Getenv(gateAgentVariable) != "" {
		fmt.Fprintln(stderr, "cfo start-hook: a start hook is the CFO's to accept once he judged it safe, never a goblin's or a gate agent's")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintf(stderr, "cfo start-hook: %v\n", err)
		return 1
	}
	meta, err := state.ReadTaskMeta(h.State, task)
	if err != nil {
		fmt.Fprintf(stderr, "cfo start-hook: %s: %v\n", task, err)
		return 1
	}
	hooks, err := starthook.Find(meta.Worktree)
	if err != nil {
		fmt.Fprintf(stderr, "cfo start-hook: %s's Python environments could not be read: %v\n", task, err)
		return 1
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(meta.Worktree, file)
	}
	at := slices.IndexFunc(hooks, func(hook starthook.Hook) bool { return fsx.SamePath(hook.Path, file) })
	project := taskProject(h.State, task)
	switch {
	case at < 0:
		fmt.Fprintf(stderr, "cfo start-hook: %s is no start hook %s's seal stops, which its wake names\n", file, task)
		return 1
	case hooks[at].Package == "":
		fmt.Fprintf(stderr, "cfo start-hook: no installed distribution's RECORD names %s, so it has no distribution to be accepted from; remove it, or end the task\n", file)
		return 1
	case hooks[at].Digest == "":
		fmt.Fprintf(stderr, "cfo start-hook: %s is a folder, which has no one content to accept; remove it, or end the task\n", file)
		return 1
	case project == "" && !*isFleet:
		fmt.Fprintf(stderr, "cfo start-hook: %s's record names no project to accept it for; accept it for every project with --fleet\n", task)
		return 1
	}
	scope, whose := project, "for "+project+"'s goblins"
	if *isFleet {
		scope, whose = "", "for every project's goblins"
	}
	hook := hooks[at]
	acceptance := starthook.Acceptance{Project: scope, File: filepath.Base(hook.Path), SHA256: hook.Digest, Distribution: hook.Package, Task: task, At: time.Now().UTC()}
	if err := starthook.Accept(h.State, acceptance); err != nil {
		fmt.Fprintf(stderr, "cfo start-hook: %v\n", err)
		return 1
	}
	next := task + "'s commands run again, so tell it to go on."
	if stopped, err := starthook.Unaccepted(h.State, project, hooks); err != nil || len(stopped) != 0 {
		next = task + " stays stopped while another start hook its wakes name is there."
	}
	fmt.Fprintf(stdout, "Accepted %s from %s %s, its content's SHA-256 %s: their seals let it through at every install from now on, and still stop it once its content, its name or its distribution differs. %s\n", acceptance.File, acceptance.Distribution, whose, acceptance.SHA256, next)
	return 0
}
