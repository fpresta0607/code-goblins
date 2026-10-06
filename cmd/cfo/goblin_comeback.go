package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// goblinComeback is what goblins resume did for one goblin a reboot or
// sign-out left without its terminal, in words for the Overlord. isWaiting
// says it waits for room to come back.
type goblinComeback struct {
	id        string
	said      string
	isBack    bool
	isWaiting bool
}

// restartNote is the line a goblin the supervisor brings back after a
// restart is told, after the instruction a resumed session gets.
const restartNote = "The machine restarted, which ended your terminal; this is your own session brought back, so continue where you left off."

// bringGoblinsBack brings back, in place, every goblin a reboot or sign-out
// ended: each task with a record whose native terminal no longer runs,
// except one paused or stopped on purpose. Each comes back through the
// switch cfo switch makes to what the task already runs, keeping its
// worktree, its uncommitted work, its harness, model and effort, on its own
// conversation where the board's record proves it is the task's and from a
// handoff where it does not. Each launch is admitted on what the one before
// it left, so a goblin there is no room for yet waits, with the reason, and
// one that cannot come back does not keep the rest from coming back. A
// queued or retired task has no record, so it is never started here.
func bringGoblinsBack(ctx context.Context, h home.Home, runtime commandRuntime) ([]goblinComeback, error) {
	scan, err := state.ScanIDs(h.State)
	if err != nil {
		return nil, err
	}
	var comebacks []goblinComeback
	for _, id := range scan.MetaIDs {
		if comeback, ended := bringGoblinBack(ctx, h, runtime, id, ""); ended {
			comebacks = append(comebacks, comeback)
		}
	}
	return comebacks, nil
}

// bringGoblinBack brings one goblin back as bringGoblinsBack says, with note
// added to what it is told when it starts, and reports false for one that
// still runs or was paused or stopped on purpose, which it leaves as it is.
func bringGoblinBack(ctx context.Context, h home.Home, runtime commandRuntime, id, note string) (goblinComeback, bool) {
	meta, err := state.ReadTaskMeta(h.State, id)
	if err != nil {
		return goblinComeback{id: id, said: "its task record cannot be read: " + err.Error()}, true
	}
	if meta.SpawnGen == "" {
		return goblinComeback{}, false
	}
	if meta.Backend != "native" {
		return goblinComeback{id: id, said: fmt.Sprintf("it ran in Herdr, so it cannot come back in place; retire it with cfo cleanup %s --force-archive", id)}, true
	}
	record, err := state.ReadLifecycle(h.State, id)
	switch {
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return goblinComeback{id: id, said: "its lifecycle record cannot be read: " + err.Error()}, true
	case err == nil && record.Generation == meta.SpawnGen:
		switch record.Phase {
		case "paused", "stopped":
			return goblinComeback{}, false
		case "pausing", "resuming", "stopping":
			if record.SuppressesMonitoring(h.State) {
				return goblinComeback{}, false
			}
			return goblinComeback{id: id, said: fmt.Sprintf("its %s was cut short; finish it from its card on the board", record.Action)}, true
		case "failed":
			return goblinComeback{id: id, said: fmt.Sprintf("its %s failed before it ended; see its card on the board", record.Action)}, true
		}
	}
	if runtime.nativeTerminalRuns(h.State, id) {
		return goblinComeback{}, false
	}
	if err := runtime.admitLaunch(h); err != nil {
		return goblinComeback{id: id, said: "waits for room: " + err.Error(), isWaiting: true}, true
	}
	session, err := ownedSession(h.State, meta)
	if err != nil {
		return goblinComeback{id: id, said: err.Error()}, true
	}
	result, err := runtime.switchTask(ctx, h, spawn.SwitchRequest{
		ID:            id,
		Generation:    meta.SpawnGen,
		ForceDirty:    true,
		BriefPath:     filepath.Join(h.Data, id, "brief.md"),
		ResumeSession: session,
		ResumeNote:    note,
	})
	if err != nil {
		return goblinComeback{id: id, said: "it could not come back: " + err.Error()}, true
	}
	if result.Resumed {
		return goblinComeback{id: id, said: "back on its conversation", isBack: true}, true
	}
	return goblinComeback{id: id, said: "back from a handoff, since no conversation is proved its own", isBack: true}, true
}

// comebackGoblin is the supervisor's comeback of one goblin after a restart:
// bringGoblinBack, telling the goblin the machine restarted, in the words the
// supervisor records.
func comebackGoblin(ctx context.Context, h home.Home, runtime commandRuntime, id string) supervisor.GoblinComeback {
	comeback, ended := bringGoblinBack(ctx, h, runtime, id, restartNote)
	switch {
	case !ended && runtime.nativeTerminalRuns(h.State, id):
		return supervisor.GoblinComeback{Outcome: supervisor.AlreadyRuns}
	case !ended:
		return supervisor.GoblinComeback{Outcome: supervisor.LeftAsItWas}
	case comeback.isBack:
		return supervisor.GoblinComeback{Outcome: supervisor.CameBack, Said: comeback.said}
	case comeback.isWaiting:
		return supervisor.GoblinComeback{Outcome: supervisor.WaitsForRoom, Said: strings.TrimPrefix(comeback.said, "waits for room: ")}
	}
	return supervisor.GoblinComeback{Outcome: supervisor.DidNotComeBack, Said: strings.TrimPrefix(comeback.said, "it could not come back: ")}
}
