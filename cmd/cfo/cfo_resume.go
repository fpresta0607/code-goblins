package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
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
	if resume, _, _ := cfoResume(h, agent); len(resume) > 0 {
		if held, err := comeBack(h, agent, resume, start, runs); err != nil || held {
			return err
		}
	}
	return start(h, h.Root, agent, nil)
}

// cfoTranscriptLimit is the size past which a CFO starts a new conversation
// rather than resume its last one: the Overlord's rule (2026-09-29) that CFO
// sessions stay small, about 20 MB.
const cfoTranscriptLimit = 20 << 20

// cfoResume is how a CFO starting as agent comes back after the home's CFO
// was closed. native says the last CFO ran in a native terminal, which the
// CFO comes back in. args are the harness's own arguments that resume the
// conversation it last registered with, or none, with the reason when it
// starts a new one instead; a CFO last run as another harness, or in Herdr,
// starts a new conversation with nothing to say.
func cfoResume(h home.Home, agent string) (args []string, why string, native bool) {
	conversation, err := supervisor.ReadCFOConversation(h.State)
	if err != nil || conversation.Host == "" {
		return nil, "", false
	}
	if conversation.Harness != agent {
		return nil, "", true
	}
	switch agent {
	case "claude":
		if size := claudeTranscriptSize(h.Root, conversation.Session); size > cfoTranscriptLimit {
			return nil, fmt.Sprintf("Its last conversation is %d MB, past the %d MB a CFO resumes, so the CFO starts a new one.", size>>20, cfoTranscriptLimit>>20), true
		}
		return []string{"--resume", conversation.Session}, "", true
	case "codex":
		return []string{"resume", conversation.Session}, "", true
	}
	return nil, fmt.Sprintf("%s has no way to resume a conversation, so the CFO starts a new one.", agent), true
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
