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

// restartedWhy is why a restarted CEO started again, as its first instruction
// tells it.
const restartedWhy = "the board's Restart or Update, or goblins resume, restarted it"

// errNoRunningCEO says no CEO runs in native terminal ceo to restart.
var errNoRunningCEO = errors.New("no CEO runs in native terminal ceo")

// restartCEO stops the CEO running in native terminal ceo, its screen frozen
// or not, and starts it again in that terminal, on its conversation where it
// can, and returns the conversation it last registered with and, when it did
// not come back on that conversation, why it started a new one. A restart
// that names no harness keeps the one the CEO runs, as a harness Update and
// goblins resume do, even where a saved selection names another. Restart
// always restarts the CEO (the Overlord, 2026-10-09: "this makes no sense" of
// a refusal): a conversation it cannot resume, one past the size a CEO
// resumes, one of a harness that resumes none, or one not registered for the
// program its terminal runs, is left as it is, and the CEO starts on a new
// one, which takes the home's digest as a CEO reopened after a close does. A
// harness that ends at once, as one that cannot resume the conversation does,
// is started again on a new conversation, and the board names the
// conversation it could not resume. It leaves the CEO running only where it
// cannot restart it: its terminal has no record or does not close, its
// program cannot be found, or this runs inside that terminal, which closing
// would end before it started the CEO again. Closing the terminal ends the harness and everything it started;
// goblins, each in a terminal of its own, keep running.
func restartCEO(h home.Home, harness string) (supervisor.CEOConversation, string, error) {
	if harness != "" && !slices.Contains(ceoHarnesses, harness) {
		return supervisor.CEOConversation{}, "", fmt.Errorf("harness %q is not claude, codex or pi, so the CEO is left running", harness)
	}
	if _, err := lock.AcquireExclusiveNamed(h.State, supervisor.CEOLaunchLock); err != nil {
		return supervisor.CEOConversation{}, "", fmt.Errorf("another CEO restart is under way: %w", err)
	}
	defer lock.ReleaseExclusiveNamed(h.State, supervisor.CEOLaunchLock)
	id, live := supervisor.NativeCEO(h.State)
	if !live || id != supervisor.NativeCEOTerminal {
		return supervisor.CEOConversation{}, "", errNoRunningCEO
	}
	if os.Getenv(host.IDVariable) == id {
		return supervisor.CEOConversation{}, "", fmt.Errorf("this runs inside the CEO's own native terminal %s, which closing would end before the CEO started again, so the CEO is left running: run goblins resume in another terminal, or from the board as a run the Overlord starts there", id)
	}
	record, err := host.ReadRecord(h.State, id)
	if err != nil {
		return supervisor.CEOConversation{}, "", fmt.Errorf("native terminal %s has no host record, so the CEO is left running: %w", id, err)
	}
	var args []string
	conversation, err := supervisor.ReadCEOConversation(h.State)
	fresh := fmt.Sprintf("The CEO in native terminal %s registered no conversation it can come back on, so the CEO starts a new one.", id)
	if err == nil && conversation.PID == record.ChildPID && conversation.Host == id && conversation.MatchesRunningCEO(h.State, record) && (harness == "" || harness == conversation.Harness) {
		var why string
		args, why = ceoResume(h, conversation.Harness)
		fresh = why + ", so the CEO starts a new one."
	} else {
		chosen := harness
		if chosen == "" {
			chosen, err = ceoHarness(h.State)
			if err != nil {
				return supervisor.CEOConversation{}, "", fmt.Errorf("the CEO registered no conversation it can come back on, and the harness to start it as cannot be read, so it is left running: %w", err)
			}
		} else if err == nil && harness != conversation.Harness {
			fresh = fmt.Sprintf("The CEO starts a new conversation in %s because its harness changed.", harness)
		}
		conversation = supervisor.CEOConversation{Harness: chosen}
	}
	if _, err := nativeCEOProgram(conversation.Harness); err != nil {
		return supervisor.CEOConversation{}, "", fmt.Errorf("the CEO's program cannot be found, so the CEO is left running: %w", err)
	}
	if harness != "" {
		if err := fsx.AtomicWriteFile(ceoHarnessPath(h.State), []byte(harness+"\n")); err != nil {
			return supervisor.CEOConversation{}, "", fmt.Errorf("the CEO's harness could not be remembered, so the CEO is left running: %w", err)
		}
	}
	if err := host.Close(h.State, record, 5*time.Second); err != nil {
		return supervisor.CEOConversation{}, "", fmt.Errorf("the CEO's terminal could not be closed, so nothing was restarted: %w", err)
	}
	// The restarted CEO is told so, as every CEO brought back is, so it takes
	// up its work without anybody typing into it.
	if len(args) == 0 {
		held, err := comeBack(h, conversation.Harness, nil, ceoBackPrompt(conversation.Harness, restartedWhy, fresh, false), startNativeCEO, supervisor.NativeTerminalRuns)
		if err != nil {
			return supervisor.CEOConversation{}, "", fmt.Errorf("the CEO stopped but did not start again on a new conversation; run goblins to bring it back: %w", err)
		}
		if !held {
			return supervisor.CEOConversation{}, "", errors.New("the restarted CEO's native terminal ended during startup. Run goblins to bring it back")
		}
		supervisor.ClearCEOConversationLeft(h.State)
		return conversation, fresh, nil
	}
	if err := startNativeCEO(h, h.Root, conversation.Harness, args, ceoBackPrompt(conversation.Harness, restartedWhy, "", true)); err != nil {
		return supervisor.CEOConversation{}, "", fmt.Errorf("the CEO stopped but did not start again on its conversation; run goblins to bring it back: %w", err)
	}
	ceoResumeWait(ceoResumeSettle)
	if supervisor.NativeTerminalRuns(h.State, id) {
		supervisor.ClearCEOConversationLeft(h.State)
		return conversation, "", nil
	}
	unresumed := fmt.Sprintf("Its conversation %s could not be resumed, so the CEO starts a new one.", conversation.Session)
	held, err := comeBack(h, conversation.Harness, nil, ceoBackPrompt(conversation.Harness, restartedWhy, unresumed, false), startNativeCEO, supervisor.NativeTerminalRuns)
	if err != nil {
		return supervisor.CEOConversation{}, "", fmt.Errorf("the CEO stopped, its conversation %s could not be resumed, and it did not start on a new one; run goblins to bring it back: %w", conversation.Session, err)
	}
	if !held {
		return supervisor.CEOConversation{}, "", errors.New("the restarted CEO's native terminal ended during startup. Run goblins to bring it back")
	}
	if err := supervisor.RecordCEOConversationLeft(h.State, supervisor.CEOConversationLeft{Harness: conversation.Harness, Session: conversation.Session, Resume: args}); err != nil {
		return supervisor.CEOConversation{}, "", fmt.Errorf("the CEO started on a new conversation, but the board could not be told that its conversation %s could not be resumed: %w", conversation.Session, err)
	}
	return conversation, unresumed, nil
}
