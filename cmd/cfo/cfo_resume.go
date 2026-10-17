package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// A CFO started to resume its conversation is looked at again after
// cfoResumeSettle: a harness that cannot resume it ends at once, and the CFO
// then starts on a new one. cfoResumeWait is how that wait is made.
var (
	cfoResumeSettle = 3 * time.Second
	cfoResumeWait   = time.Sleep
)

// startCFO starts harness as the CFO of home h in native terminal cfo, in
// project, with args, such as the ones that resume a conversation, and prompt
// as its first instruction, as startNativeCFO does.
type startCFO func(h home.Home, project, harness string, args []string, prompt string) error

// comeBack starts agent as the CFO of h in native terminal cfo on the
// conversation resume names, with prompt as its first instruction, and
// reports whether it holds: a terminal that ended within cfoResumeSettle is a
// harness that could not resume it.
func comeBack(h home.Home, agent string, resume []string, prompt string, start startCFO, runs func(stateDir, id string) bool) (bool, error) {
	if err := start(h, h.Root, agent, resume, prompt); err != nil {
		return false, err
	}
	cfoResumeWait(cfoResumeSettle)
	return runs(h.State, supervisor.NativeCFOTerminal), nil
}

// bringCFOBack starts the home's closed CFO in native terminal cfo as agent,
// by the rule Restart the CFO keeps: on the conversation it last registered
// with, where its harness resumes one and it is small enough, and on a new one
// where not or where the resumed terminal does not hold, which the board is
// told of. A CFO that ran in this home before is told with its first
// instruction that it is back because of why, and to take up the fleet's
// work, so nobody has to type into it; a home's first CFO waits for the
// Overlord. The caller holds the CFO's launch lock.
func bringCFOBack(h home.Home, agent, why string, start startCFO, runs func(stateDir, id string) bool) error {
	resume, fresh := cfoResume(h, agent)
	if fresh != "" {
		fresh = "You start on a new conversation. " + fresh + "."
	}
	if len(resume) > 0 {
		held, err := comeBack(h, agent, resume, backPrompt(h, agent, why, ""), start, runs)
		if err != nil {
			return err
		}
		if held {
			supervisor.ClearCFOConversationLeft(h.State)
			return nil
		}
		fresh = fmt.Sprintf("You start on a new conversation. Your conversation %s could not be resumed.", resume[len(resume)-1])
	}
	if err := start(h, h.Root, agent, nil, backPrompt(h, agent, why, fresh)); err != nil {
		return err
	}
	if len(resume) > 0 {
		left := supervisor.CFOConversationLeft{Harness: agent, Session: resume[len(resume)-1], Resume: resume}
		if err := supervisor.RecordCFOConversationLeft(h.State, left); err != nil {
			return fmt.Errorf("the CFO started on a new conversation, but the board could not be told that its conversation %s could not be resumed: %w", left.Session, err)
		}
	}
	return nil
}

// backPrompt is the first instruction of agent started again as the CFO of h
// because of why, and on a new conversation as fresh says (cfoBackPrompt), or
// none for a home whose CFO never registered, whose first CFO waits for the
// Overlord.
func backPrompt(h home.Home, agent, why, fresh string) string {
	if _, err := supervisor.ReadCFOConversation(h.State); err != nil {
		return ""
	}
	return cfoBackPrompt(agent, why, fresh)
}

// reopenCFO brings the home's closed CFO back for the board's Reopen, and for
// the desktop window and goblins --board, as the agent the home remembers and
// as bringCFOBack does, telling it why. The board shows the CFO in a native
// terminal, so it starts in one wherever it ran before.
func reopenCFO(h home.Home, why string, start startCFO, runs func(stateDir, id string) bool) error {
	launchErr := acquireCFOLaunch(context.Background(), h.State)
	if launchErr == nil {
		defer lock.ReleaseExclusiveNamed(h.State, cfoLaunchLock)
	}
	if supervisor.CFORuns(h.State) || runs(h.State, supervisor.NativeCFOTerminal) {
		return nil
	}
	if launchErr != nil {
		return launchErr
	}
	agent, err := cfoHarness(h.State)
	if err != nil {
		return err
	}
	return bringCFOBack(h, agent, why, start, runs)
}

// comebackCFO brings the home's CFO back by itself, as the supervisor does
// after a restart or sign-out and after the CFO's terminal ended while nobody
// asked it to stop: as the agent the home remembers and as bringCFOBack does,
// telling it why, with its startup dialogs answered. A CFO that runs already,
// as one goblins started first, is back.
func comebackCFO(ctx context.Context, h home.Home, runtime commandRuntime, why string) error {
	launchErr := acquireCFOLaunch(ctx, h.State)
	if launchErr == nil {
		defer lock.ReleaseExclusiveNamed(h.State, cfoLaunchLock)
	}
	if cfoRuns(runtime, h.State) {
		return nil
	}
	if launchErr != nil {
		return launchErr
	}
	agent, err := cfoHarness(h.State)
	if err != nil {
		return err
	}
	if err := bringCFOBack(h, agent, why, runtime.startNativeCFO, runtime.nativeTerminalRuns); err != nil {
		return err
	}
	runtime.settleCFO(ctx, h.State, agent)
	if !runtime.nativeTerminalRuns(h.State, supervisor.NativeCFOTerminal) {
		return errors.New("its native terminal ended during startup")
	}
	return nil
}

// cfoTranscriptLimit is the size past which a CFO starts a new conversation
// rather than resume its last one: the Overlord's rule (2026-09-29) that CFO
// sessions stay small, about 20 MB.
const cfoTranscriptLimit = 20 << 20

// cfoResume is how a CFO starting as agent comes back after the home's CFO
// was closed. args are the harness's own arguments that resume the
// conversation it last registered with, or none, with why that conversation
// cannot be resumed when it starts a new one instead; a CFO last run as
// another harness, or in Herdr, starts a new conversation with nothing to
// say.
func cfoResume(h home.Home, agent string) (args []string, why string) {
	conversation, err := supervisor.ReadCFOConversation(h.State)
	if err != nil || conversation.Host == "" {
		return nil, ""
	}
	if conversation.Harness != agent {
		return nil, ""
	}
	switch agent {
	case "claude":
		if size := claudeTranscriptSize(h.Root, conversation.Session); size > cfoTranscriptLimit {
			return nil, fmt.Sprintf("Its last conversation is %d MB, past the %d MB a CFO resumes", size>>20, cfoTranscriptLimit>>20)
		}
		return []string{"--resume", conversation.Session}, ""
	case "codex":
		return []string{"resume", conversation.Session}, ""
	case "pi":
		// As a pi goblin's switch does: pi 0.85.1 writes a session's file when
		// its first reply ends, and exits on a --session it finds no file for.
		userHome, _ := os.UserHomeDir()
		if fleettree.SessionTranscript(userHome, "pi", conversation.Session) == "" {
			return nil, fmt.Sprintf("pi has written no file for its conversation %s, which it writes when its first reply ends", conversation.Session)
		}
		return []string{"--session", conversation.Session}, ""
	}
	return nil, fmt.Sprintf("%s has no way to resume a conversation", agent)
}

// claudeProjectFolder names the folder under ~\.claude\projects where Claude
// Code keeps a working folder's conversations: its path with every character
// that is not a letter or digit as a dash.
var claudeProjectFolder = regexp.MustCompile(`[^A-Za-z0-9]`)

// claudeTranscriptSize is the size of Claude Code's transcript of session,
// a conversation it held in dir, or 0 when there is none to read.
func claudeTranscriptSize(dir, session string) int64 {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	info, err := os.Stat(filepath.Join(userHome, ".claude", "projects", claudeProjectFolder.ReplaceAllString(dir, "-"), session+".jsonl"))
	if err != nil {
		return 0
	}
	return info.Size()
}
