package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// A CFO started to resume its conversation is looked at again after
// cfoResumeSettle: a harness that cannot resume it ends at once, and the CFO
// then starts on a new one. cfoResumeWait is how that wait is made.
var (
	cfoResumeSettle = 3 * time.Second
	cfoResumeWait   = time.Sleep
)

// comeBack starts agent as the CFO of h in native terminal cfo on the
// conversation resume names, and reports whether it holds: a terminal that
// ended within cfoResumeSettle is a harness that could not resume it.
func comeBack(h home.Home, agent string, resume []string, start func(h home.Home, project, harness string, args []string) error, runs func(stateDir, id string) bool) (bool, error) {
	if err := start(h, h.Root, agent, resume); err != nil {
		return false, err
	}
	cfoResumeWait(cfoResumeSettle)
	return runs(h.State, supervisor.NativeCFOTerminal), nil
}

// reopenCFO brings the home's closed CFO back for the board's Reopen, as
// goblins brings it back: as the agent the home remembers, in native terminal
// cfo, on the conversation it last registered with where its harness resumes
// one, and on a new one when there is none or the resumed terminal does not
// hold. The board shows the CFO in a native terminal, so it starts in one
// wherever it ran before.
func reopenCFO(h home.Home, start func(h home.Home, project, harness string, args []string) error, runs func(stateDir, id string) bool) error {
	agent, err := cfoHarness(h.State)
	if err != nil {
		return err
	}
	resume, _ := cfoResume(h, agent)
	if len(resume) > 0 {
		held, err := comeBack(h, agent, resume, start, runs)
		if err != nil {
			return err
		}
		if held {
			supervisor.ClearCFOConversationLeft(h.State)
			return nil
		}
	}
	if err := start(h, h.Root, agent, nil); err != nil {
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

// comebackCFO brings the home's CFO back after a restart, as the
// supervisor's comeback does first: as the agent the home remembers, in
// native terminal cfo, on the conversation it last registered with and never
// on a new one, with its startup dialogs answered. It says why the CFO did
// not come back, and the board's Reopen then starts it on a new one. A CFO
// that runs already, as one goblins started first, is back.
func comebackCFO(ctx context.Context, h home.Home, runtime commandRuntime) error {
	if _, err := lock.AcquireExclusiveNamed(h.State, cfoLaunchLock); err != nil {
		return fmt.Errorf("another start of the CFO is under way: %w", err)
	}
	defer lock.ReleaseExclusiveNamed(h.State, cfoLaunchLock)
	if cfoRuns(runtime, h.State) {
		return nil
	}
	agent, err := cfoHarness(h.State)
	if err != nil {
		return err
	}
	resume, why := cfoResume(h, agent)
	if len(resume) == 0 {
		if why == "" {
			why = "it registered no conversation of its own as " + onboarding.Name(agent)
		}
		return fmt.Errorf("%s; Reopen on its bar starts it on a new one", why)
	}
	held, err := comeBack(h, agent, resume, runtime.startNativeCFO, runtime.nativeTerminalRuns)
	if err != nil {
		return err
	}
	if held {
		runtime.settleCFO(ctx, h.State, agent)
	}
	if !held || !runtime.nativeTerminalRuns(h.State, supervisor.NativeCFOTerminal) {
		return fmt.Errorf("its conversation %s could not be resumed; Reopen on its bar starts it on a new one", resume[len(resume)-1])
	}
	supervisor.ClearCFOConversationLeft(h.State)
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
