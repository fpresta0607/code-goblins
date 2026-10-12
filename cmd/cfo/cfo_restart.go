package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
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
// or not, and starts it again in that terminal, on its conversation where it
// can, and returns the conversation it last registered with and, when it did
// not come back on that conversation, why it started a new one. Restart
// always restarts the CFO (the Overlord, 2026-10-09: "this makes no sense" of
// a refusal): a conversation it cannot resume, one past the size a CFO
// resumes, one of a harness that resumes none, or one not registered for the
// program its terminal runs, is left as it is, and the CFO starts on a new
// one, which takes the home's digest as a CFO reopened after a close does. A
// harness that ends at once, as one that cannot resume the conversation does,
// is started again on a new conversation, and the board names the
// conversation it could not resume. It leaves the CFO running only where it
// cannot restart it: its terminal has no record or does not close, its
// program cannot be found, or this runs inside that terminal, which closing
// would end before it started the CFO again. Closing the terminal ends the harness and everything it started;
// goblins, each in a terminal of its own, keep running.
func restartCFO(h home.Home, harness string) (supervisor.CFOConversation, string, error) {
	if harness != "" && !slices.Contains(cfoHarnesses, harness) {
		return supervisor.CFOConversation{}, "", fmt.Errorf("harness %q is not claude, codex or pi, so the CFO is left running", harness)
	}
	if _, err := lock.AcquireExclusiveNamed(h.State, cfoLaunchLock); err != nil {
		return supervisor.CFOConversation{}, "", fmt.Errorf("another CFO restart is under way: %w", err)
	}
	defer lock.ReleaseExclusiveNamed(h.State, cfoLaunchLock)
	id, live := supervisor.NativeCFO(h.State)
	if !live || id != supervisor.NativeCFOTerminal {
		return supervisor.CFOConversation{}, "", errNoRunningCFO
	}
	if os.Getenv(host.IDVariable) == id {
		return supervisor.CFOConversation{}, "", fmt.Errorf("this runs inside the CFO's own native terminal %s, which closing would end before the CFO started again, so the CFO is left running: run goblins resume in another terminal, or from the board as a run the Overlord starts there", id)
	}
	record, err := host.ReadRecord(h.State, id)
	if err != nil {
		return supervisor.CFOConversation{}, "", fmt.Errorf("native terminal %s has no host record, so the CFO is left running: %w", id, err)
	}
	var args []string
	conversation, err := supervisor.ReadCFOConversation(h.State)
	fresh := fmt.Sprintf("The CFO in native terminal %s registered no conversation it can come back on, so the CFO starts a new one.", id)
	if err == nil && conversation.PID == record.ChildPID && conversation.Host == id && conversation.MatchesRunningCFO(h.State, record) && (harness == "" || harness == conversation.Harness) {
		var why string
		args, why = cfoResume(h, conversation.Harness)
		fresh = why + ", so the CFO starts a new one."
	} else {
		chosen := harness
		if chosen == "" {
			chosen, err = cfoHarness(h.State)
			if err != nil {
				return supervisor.CFOConversation{}, "", fmt.Errorf("the CFO registered no conversation it can come back on, and the harness to start it as cannot be read, so it is left running: %w", err)
			}
		} else if err == nil && harness != conversation.Harness {
			fresh = fmt.Sprintf("The CFO starts a new conversation in %s because its harness changed.", harness)
		}
		conversation = supervisor.CFOConversation{Harness: chosen}
	}
	if _, err := nativeCFOProgram(conversation.Harness); err != nil {
		return supervisor.CFOConversation{}, "", fmt.Errorf("the CFO's program cannot be found, so the CFO is left running: %w", err)
	}
	if harness != "" {
		if err := fsx.AtomicWriteFile(cfoHarnessPath(h.State), []byte(harness+"\n")); err != nil {
			return supervisor.CFOConversation{}, "", fmt.Errorf("the CFO's harness could not be remembered, so the CFO is left running: %w", err)
		}
	}
	if err := host.Close(h.State, record, 5*time.Second); err != nil {
		return supervisor.CFOConversation{}, "", fmt.Errorf("the CFO's terminal could not be closed, so nothing was restarted: %w", err)
	}
	if len(args) == 0 {
		held, err := comeBack(h, conversation.Harness, nil, startNativeCFO, supervisor.NativeTerminalRuns)
		if err != nil {
			return supervisor.CFOConversation{}, "", fmt.Errorf("the CFO stopped but did not start again on a new conversation; run goblins to bring it back: %w", err)
		}
		if !held {
			return supervisor.CFOConversation{}, "", errors.New("the restarted CFO's native terminal ended during startup; run goblins to bring it back")
		}
		supervisor.ClearCFOConversationLeft(h.State)
		return conversation, fresh, nil
	}
	if err := startNativeCFO(h, h.Root, conversation.Harness, args); err != nil {
		return supervisor.CFOConversation{}, "", fmt.Errorf("the CFO stopped but did not start again on its conversation; run goblins to bring it back: %w", err)
	}
	cfoResumeWait(cfoResumeSettle)
	if supervisor.NativeTerminalRuns(h.State, id) {
		supervisor.ClearCFOConversationLeft(h.State)
		return conversation, "", nil
	}
	held, err := comeBack(h, conversation.Harness, nil, startNativeCFO, supervisor.NativeTerminalRuns)
	if err != nil {
		return supervisor.CFOConversation{}, "", fmt.Errorf("the CFO stopped, its conversation %s could not be resumed, and it did not start on a new one; run goblins to bring it back: %w", conversation.Session, err)
	}
	if !held {
		return supervisor.CFOConversation{}, "", errors.New("the restarted CFO's native terminal ended during startup; run goblins to bring it back")
	}
	if err := supervisor.RecordCFOConversationLeft(h.State, supervisor.CFOConversationLeft{Harness: conversation.Harness, Session: conversation.Session, Resume: args}); err != nil {
		return supervisor.CFOConversation{}, "", fmt.Errorf("the CFO started on a new conversation, but the board could not be told that its conversation %s could not be resumed: %w", conversation.Session, err)
	}
	return conversation, fmt.Sprintf("Its conversation %s could not be resumed, so the CFO starts a new one.", conversation.Session), nil
}
