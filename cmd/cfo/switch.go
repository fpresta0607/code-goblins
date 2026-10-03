package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
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
	meta, err := state.ReadTaskMeta(h.State, id)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	request := spawn.SwitchRequest{
		ID:         id,
		Harness:    harness.Kind(*harnessName),
		Model:      *model,
		Effort:     *effort,
		ForceDirty: *forceDirty,
		BriefPath:  filepath.Join(h.Data, id, "brief.md"),
		Generation: meta.SpawnGen,
	}
	if (request.Harness == "" || string(request.Harness) == meta.Harness) && (meta.Harness == "codex" || meta.Harness == "claude") {
		data, readErr := fsx.ReadFile(filepath.Join(h.State, ".supervisor.json"))
		if readErr == nil {
			var database supervisor.Database
			if err := json.Unmarshal(data, &database); err != nil {
				fmt.Fprintf(stderr, "switch: read session ownership: %v\n", err)
				return 1
			}
			session := database.Sessions[database.TaskSessions[meta.ID]]
			if meta.SpawnGen != "" && session.TaskID == meta.ID && session.Generation == meta.SpawnGen && session.Harness == meta.Harness && session.Role == "goblin" {
				request.ResumeSession = session.NativeID
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			fmt.Fprintf(stderr, "switch: read session ownership: %v\n", readErr)
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
