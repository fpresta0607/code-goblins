package connections

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func nativeRuntime(ctx context.Context, stateDir string, meta state.TaskMeta) ([]string, []string, error) {
	var childPID, ownerPID int
	var announced time.Time
	if meta.Backend == "native" {
		record, err := host.ReadRecord(stateDir, meta.ID)
		if err != nil {
			return nil, nil, errors.New("Goblin runtime is unavailable.")
		}
		childPID, ownerPID, announced = record.ChildPID, record.HostPID, record.Started
	} else {
		client := &herdr.Client{Commands: execx.OSRunner{}, Session: meta.HerdrSession}
		snapshot, err := client.Snapshot(ctx)
		if err != nil || !slices.ContainsFunc(snapshot.Agents, func(agent herdr.SnapshotAgent) bool {
			return agent.PaneID == meta.HerdrPaneID && agent.TabID == meta.HerdrTabID && agent.WorkspaceID == meta.HerdrWorkspaceID && agent.Agent == meta.Harness && strings.EqualFold(filepath.Clean(agent.Cwd), filepath.Clean(meta.Worktree))
		}) {
			return nil, nil, errors.New("Goblin pane registration is unavailable.")
		}
		info, err := client.PaneProcessInfo(ctx, herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID})
		if err != nil || info.ShellPID == info.ForegroundProcessGroupID {
			return nil, nil, errors.New("Goblin runtime is unavailable.")
		}
		childPID, ownerPID = info.ForegroundProcessGroupID, info.ShellPID
	}
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
