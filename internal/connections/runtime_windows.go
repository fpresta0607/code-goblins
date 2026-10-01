package connections

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func nativeRuntime(_ context.Context, stateDir string, meta state.TaskMeta) ([]string, []string, error) {
	if meta.Backend != "native" {
		return nil, nil, errors.New("Goblin runtime is unavailable.")
	}
	record, err := host.ReadRecord(stateDir, meta.ID)
	if err != nil {
		return nil, nil, errors.New("Goblin runtime is unavailable.")
	}
	childPID, ownerPID, announced := record.ChildPID, record.HostPID, record.Started
	ancestry, err := proc.Ancestry(childPID, 16)
	if err != nil || len(ancestry) < 2 || !slices.ContainsFunc(ancestry[1:], func(entry proc.Entry) bool { return entry.PID == ownerPID }) || (!announced.IsZero() && ancestry[0].Start.After(announced)) {
		return nil, nil, errors.New("Goblin runtime changed. Refresh connections.")
	}
	directory, err := proc.WorkingDirectory(childPID)
	if err != nil || !strings.EqualFold(filepath.Clean(directory), filepath.Clean(meta.Worktree)) {
		return nil, nil, errors.New("Goblin working folder could not be verified.")
	}
	env, err := proc.Environment(childPID)
	if err != nil {
		return nil, nil, errors.New("Goblin credentials could not be checked.")
	}
	generation := ""
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "CFO_SPAWN_GEN") {
			generation = value
		}
	}
	if generation != meta.SpawnGen {
		return nil, nil, errors.New("Goblin runtime changed. Refresh connections.")
	}
	args, err := proc.Arguments(childPID)
	if err != nil {
		return nil, nil, errors.New("Goblin launch settings are unavailable.")
	}
	after, err := proc.Ancestry(childPID, 1)
	if err != nil || len(after) != 1 || !after[0].Start.Equal(ancestry[0].Start) {
		return nil, nil, errors.New("Goblin runtime changed. Refresh connections.")
	}
	return env, args, nil
}
