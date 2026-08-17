package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/spawn"
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
	harnessName := flags.String("harness", "", "claude, codex, pi, or kimi")
	model := flags.String("model", "", "model for the new harness")
	effort := flags.String("effort", "", "reasoning effort for the new harness")
	forceDirty := flags.Bool("force-dirty", false, "switch even though the worktree has uncommitted changes")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if *harnessName == "" && *model == "" && *effort == "" {
		fmt.Fprintln(stderr, "cfo switch: one of --harness, --model, or --effort is required")
		return 2
	}
	if *harnessName != "" && !validSpawnHarness(*harnessName) {
		fmt.Fprintln(stderr, "cfo switch: --harness must be claude, codex, pi, or kimi")
		return 2
	}

	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result, err := runtime.switchTask(context.Background(), h, spawn.SwitchRequest{
		ID:         id,
		Harness:    harness.Kind(*harnessName),
		Model:      *model,
		Effort:     *effort,
		ForceDirty: *forceDirty,
		Session:    herdrSession(),
		BriefPath:  filepath.Join(h.Data, id, "brief.md"),
	})
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
