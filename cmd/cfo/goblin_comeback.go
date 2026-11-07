package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/lock"
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

// messageNote is the line a goblin the supervisor brings back for a message
// the Overlord sent it while its terminal did not run is told, after the
// instruction a resumed session gets.
const messageNote = "Your terminal was not running when the Overlord sent you a message on the board, so this is your own session brought back for it; his message is typed in after this instruction, so take it up first, then continue where you left off."

// bringGoblinsBack brings back, in place, every goblin a reboot or sign-out
// ended: each task with a record whose native terminal no longer runs,
// except one paused or stopped on purpose or one whose latest report
// delivered and that took no prompt since, which waits on its merge or on
// nothing. One whose resume the reboot cut short comes back through a resume
// that finishes it, and each other through the switch cfo switch makes to
// what the task already runs, keeping its worktree, its uncommitted work, its
// harness, model and effort, on its own conversation where the board's record
// proves it is the task's and from a handoff where it does not. Each launch is
// admitted on what the one before it left, so a goblin there is no room for
// yet waits, with the reason, and one that cannot come back does not keep the
// rest from coming back. A queued or retired task has no record, so it is
// never started here.
func bringGoblinsBack(ctx context.Context, h home.Home, runtime commandRuntime) ([]goblinComeback, error) {
	scan, err := state.ScanIDs(h.State)
	if err != nil {
		return nil, err
	}
	var comebacks []goblinComeback
	for _, id := range scan.MetaIDs {
		if comeback, ended := bringGoblinBack(ctx, h, runtime, id, "", false); ended {
			comebacks = append(comebacks, comeback)
		}
	}
	return comebacks, nil
}

// bringGoblinBack brings one goblin back as bringGoblinsBack says, with note
// added to what it is told when it starts, and reports false for one that
// still runs, was paused or stopped on purpose, or delivered, which it leaves
// as it is. isPrompted says the Overlord prompted it, so one that delivered
// comes back too, and so does one whose failed pause took hold.
func bringGoblinBack(ctx context.Context, h home.Home, runtime commandRuntime, id, note string, isPrompted bool) (goblinComeback, bool) {
	meta, err := state.ReadTaskMeta(h.State, id)
	if err != nil {
		return goblinComeback{id: id, said: "its task record cannot be read: " + err.Error()}, true
	}
	if meta.SpawnGen == "" {
		return goblinComeback{}, false
	}
	if !isPrompted {
		isDelivered, err := supervisor.HasDelivered(h.State, meta)
		if err != nil {
			return goblinComeback{id: id, said: err.Error()}, true
		}
		if isDelivered {
			return goblinComeback{}, false
		}
	}
	if meta.Backend != "native" {
		return goblinComeback{id: id, said: fmt.Sprintf("it ran in Herdr, so it cannot come back in place; retire it with cfo cleanup %s --force-archive", id)}, true
	}
	record, err := state.ReadLifecycle(h.State, id)
	isFailedPause := err == nil && !record.LeavesRunning(h.State, meta) && record.Action == "pause" && record.Phase == "failed"
	switch {
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return goblinComeback{id: id, said: "its lifecycle record cannot be read: " + err.Error()}, true
	case err == nil && !record.LeavesRunning(h.State, meta) && !(isFailedPause && isPrompted):
		switch {
		// A failed pause took hold once the terminal ended, so the goblin is
		// paused, as one paused on purpose is, and only his message wakes it.
		case record.SuppressesMonitoring(h.State), isFailedPause:
			return goblinComeback{}, false
		case record.Phase == "failed":
			return goblinComeback{id: id, said: fmt.Sprintf("its %s failed before it ended; see its card on the board", record.Action)}, true
		}
		return goblinComeback{id: id, said: fmt.Sprintf("its %s was cut short; finish it from its card on the board", record.Action)}, true
	}
	if runtime.nativeTerminalRuns(h.State, id) {
		return goblinComeback{}, false
	}
	if err := runtime.admitLaunch(h); err != nil {
		return goblinComeback{id: id, said: "waits for room: " + err.Error(), isWaiting: true}, true
	}
	name := ".prepared-wait-" + id + ".lock"
	if _, err := lock.AcquireExclusiveNamed(h.State, name); err != nil {
		return goblinComeback{id: id, said: "prepared dependency admission cannot be checked: " + err.Error()}, true
	}
	defer lock.ReleaseExclusiveNamed(h.State, name)
	if err := state.CheckPreparedComeback(h.State, meta); err != nil {
		return goblinComeback{id: id, said: err.Error()}, true
	}
	// A resume the reboot cut short is finished by a resume, which takes up
	// what its record kept and leaves the record running, where a bare
	// switch would leave it reading resuming. A failed pause his message
	// wakes ends the same way, or it would go on reading failed.
	if err == nil && (record.Phase == "resuming" || isFailedPause) {
		reason, said := "the machine restarted", "back, with the resume the restart cut short finished"
		if isFailedPause {
			reason, said = "the Overlord sent it a message", "back for his message, from the pause that failed"
		}
		_, err := runtime.taskLifecycle(ctx, h, lifecycle.Request{ID: id, Generation: meta.SpawnGen, Operation: fmt.Sprintf("op-%d", time.Now().UnixNano()), Action: "resume", Reason: reason, ResumeNote: note, IsWatched: true}, "")
		switch {
		case errors.Is(err, state.ErrNoRoom):
			return goblinComeback{id: id, said: err.Error(), isWaiting: true}, true
		case errors.Is(err, lock.ErrHeld):
			return goblinComeback{id: id, said: "waits for room: " + err.Error(), isWaiting: true}, true
		case err != nil:
			return goblinComeback{id: id, said: "it could not come back: " + err.Error()}, true
		}
		return goblinComeback{id: id, said: said, isBack: true}, true
	}
	session, err := fleettree.OwnedSession(h.State, meta)
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
		Admit:         func() error { return runtime.admitLaunch(h) },
	})
	switch {
	case errors.Is(err, state.ErrNoRoom):
		return goblinComeback{id: id, said: err.Error(), isWaiting: true}, true
	case errors.Is(err, lock.ErrHeld):
		return goblinComeback{id: id, said: "waits for room: " + err.Error(), isWaiting: true}, true
	case err != nil:
		return goblinComeback{id: id, said: "it could not come back: " + err.Error()}, true
	}
	if result.Resumed {
		return goblinComeback{id: id, said: "back on its conversation", isBack: true}, true
	}
	return goblinComeback{id: id, said: "back from a handoff, since no conversation is proved its own", isBack: true}, true
}

// comebackGoblin is the supervisor's comeback of one goblin, after a restart
// or for a message the Overlord sent it: bringGoblinBack, telling the goblin
// note, in the words the supervisor records.
func comebackGoblin(ctx context.Context, h home.Home, runtime commandRuntime, id, note string, isPrompted bool) supervisor.GoblinComeback {
	comeback, ended := bringGoblinBack(ctx, h, runtime, id, note, isPrompted)
	switch {
	case !ended && runtime.nativeTerminalRuns(h.State, id):
		return supervisor.GoblinComeback{Outcome: supervisor.AlreadyRuns}
	case !ended:
		return supervisor.GoblinComeback{Outcome: supervisor.LeftAsItWas}
	case comeback.isBack:
		return supervisor.GoblinComeback{Outcome: supervisor.CameBack, Said: comeback.said}
	case runtime.nativeTerminalRuns(h.State, id):
		return supervisor.GoblinComeback{Outcome: supervisor.CameBack, Said: strings.TrimPrefix(comeback.said, "it could not come back: ")}
	case comeback.isWaiting:
		return supervisor.GoblinComeback{Outcome: supervisor.WaitsForRoom, Said: strings.TrimPrefix(comeback.said, "waits for room: ")}
	}
	return supervisor.GoblinComeback{Outcome: supervisor.DidNotComeBack, Said: strings.TrimPrefix(comeback.said, "it could not come back: ")}
}
