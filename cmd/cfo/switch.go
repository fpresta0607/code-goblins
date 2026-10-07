package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// runSwitch changes a running goblin's harness, model, or effort in place.
func runSwitch(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "cfo switch: task ID is required")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("switch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	harnessName := flags.String("harness", "", "claude, codex, or pi")
	model := flags.String("model", "", "model for the new harness")
	effort := flags.String("effort", "", "reasoning effort for the new harness")
	generation := flags.String("generation", "", "switch only the selected task session")
	forceDirty := flags.Bool("force-dirty", false, "switch even though the worktree has uncommitted changes")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if *harnessName == "" && *model == "" && *effort == "" {
		fmt.Fprintln(stderr, "cfo switch: one of --harness, --model, or --effort is required")
		return 2
	}
	if *harnessName != "" && !validSpawnHarness(*harnessName) {
		fmt.Fprintln(stderr, "cfo switch: --harness must be claude, codex, or pi")
		return 2
	}

	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	meta, err := state.ReadTaskMeta(h.State, id)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// The switch is pinned to the session the caller chose or, with none
	// named, the one read here, so a task replaced in between is refused.
	pinned := *generation
	if pinned == "" {
		pinned = meta.SpawnGen
	}
	request := spawn.SwitchRequest{
		ID:         id,
		Harness:    harness.Kind(*harnessName),
		Model:      *model,
		Effort:     *effort,
		Generation: pinned,
		ForceDirty: *forceDirty,
		BriefPath:  filepath.Join(h.Data, id, "brief.md"),
	}
	if request.Harness == "" || string(request.Harness) == meta.Harness {
		if request.ResumeSession, err = fleettree.OwnedSession(h.State, meta); err != nil {
			fmt.Fprintf(stderr, "switch: %v\n", err)
			return 1
		}
	}
	result, err := runtime.switchTask(context.Background(), h, request)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, result.Output)
	if hint := runtime.speedHint(context.Background(), result.Meta.Harness); hint != "" {
		fmt.Fprintln(stdout, hint)
	}
	return 0
}
