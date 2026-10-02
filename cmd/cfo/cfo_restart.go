package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// cfoLaunchLock serializes restarting the CFO, so two restarts started at
// once never start two CFOs.
const cfoLaunchLock = ".cfo-launch.lock"

// errNoRunningCFO says no CFO runs in native terminal cfo to restart.
var errNoRunningCFO = errors.New("no CFO runs in native terminal cfo")

// restartCFO stops the CFO running in native terminal cfo, its screen frozen
// or not, and starts it again in that terminal on the same conversation, and
// returns the conversation. It stops nothing it cannot bring back: a CFO
// whose terminal runs a process its conversation was not recorded for, or
// whose conversation cannot be resumed, is left running, with the reason.
// Closing the terminal ends the harness and everything it started; goblins,
// each in a terminal of its own, keep running.
func restartCFO(h home.Home) (string, error) {
	if _, err := lock.AcquireExclusiveNamed(h.State, cfoLaunchLock); err != nil {
		return "", fmt.Errorf("another CFO restart is under way: %w", err)
	}
	defer lock.ReleaseExclusiveNamed(h.State, cfoLaunchLock)
	id, live := supervisor.NativeCFO(h.State)
	if !live || id != supervisor.NativeCFOTerminal {
		return "", errNoRunningCFO
	}
	record, err := host.ReadRecord(h.State, id)
	if err != nil {
		return "", fmt.Errorf("native terminal %s has no host record, so the CFO is left running: %w", id, err)
	}
	conversation, err := supervisor.ReadCFOConversation(h.State)
	if err != nil || conversation.PID != record.ChildPID || conversation.Host != id {
		return "", fmt.Errorf("the CFO in native terminal %s registered no conversation it can come back on, so it is left running", id)
	}
	args, why, _ := cfoResume(h, conversation.Harness)
	if len(args) == 0 {
		return "", fmt.Errorf("%s It is left running: close it and run goblins to start it on a new conversation", why)
	}
	if err := host.Close(h.State, record, 5*time.Second); err != nil {
		return "", fmt.Errorf("the CFO's terminal could not be closed, so nothing was restarted: %w", err)
	}
	if err := startNativeCFO(h, h.Root, conversation.Harness, args); err != nil {
		return "", fmt.Errorf("the CFO stopped but did not start again on its conversation; run goblins to bring it back: %w", err)
	}
	return conversation.Session, nil
}
