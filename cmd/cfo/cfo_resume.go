package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
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
// Overlord. settle answers the startup dialogs of each CFO it starts, and a
// resumed CFO whose terminal ends while they are answered did not hold
// either: Claude Code 2.1.302 took 3.6 seconds to end on a conversation it
// could not resume. The caller holds the CFO's launch lock.
func bringCFOBack(h home.Home, agent, why string, start startCFO, runs func(stateDir, id string) bool, settle func()) error {
	resume, fresh := cfoResume(h, agent)
	if fresh != "" {
		fresh = "You start on a new conversation. " + fresh + "."
	}
	if len(resume) > 0 {
		held, err := comeBack(h, agent, resume, backPrompt(h, agent, why, "", true), start, runs)
		if err != nil {
			return err
		}
		if held {
			settle()
			held = runs(h.State, supervisor.NativeCFOTerminal)
		}
		if held {
			supervisor.ClearCFOConversationLeft(h.State)
			return nil
		}
		fresh = fmt.Sprintf("You start on a new conversation. Your conversation %s could not be resumed.", resume[len(resume)-1])
	}
	if err := start(h, h.Root, agent, nil, backPrompt(h, agent, why, fresh, false)); err != nil {
		return err
	}
	if len(resume) > 0 {
		left := supervisor.CFOConversationLeft{Harness: agent, Session: resume[len(resume)-1], Resume: resume}
		if err := supervisor.RecordCFOConversationLeft(h.State, left); err != nil {
			return fmt.Errorf("the CFO started on a new conversation, but the board could not be told that its conversation %s could not be resumed: %w", left.Session, err)
		}
	}
	settle()
	return nil
}

// backPrompt is the first instruction of agent started again as the CFO of h
// because of why, on its conversation where isResumed and on a new one as
// fresh says (cfoBackPrompt), or none for a home whose CFO never registered,
// whose first CFO waits for the Overlord.
func backPrompt(h home.Home, agent, why, fresh string, isResumed bool) string {
	if _, err := supervisor.ReadCFOConversation(h.State); err != nil {
		return ""
	}
	return cfoBackPrompt(agent, why, fresh, isResumed)
}

// reopenCFO brings the home's closed CFO back for the board's Reopen, and for
// the desktop window and goblins --board, as the agent the home remembers and
// as bringCFOBack does, telling it why. The board shows the CFO in a native
// terminal, so it starts in one wherever it ran before.
func reopenCFO(h home.Home, why string, start startCFO, runs func(stateDir, id string) bool) error {
	launchErr := acquireCFOLaunch(context.Background(), h.State)
	if launchErr == nil {
		defer lock.ReleaseExclusiveNamed(h.State, supervisor.CFOLaunchLock)
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
	// The board opens the reopened CFO's terminal, where the Overlord answers
	// what it asks.
	return bringCFOBack(h, agent, why, start, runs, func() {})
}

// comebackCFO brings the home's CFO back by itself, as the supervisor does
// after a restart or sign-out and after the CFO's terminal ended while nobody
// asked it to stop: as the agent the home remembers and as bringCFOBack does,
// telling it why, with its startup dialogs answered. A CFO that runs already,
// as one goblins started first, is back.
func comebackCFO(ctx context.Context, h home.Home, runtime commandRuntime, why string) error {
	launchErr := acquireCFOLaunch(ctx, h.State)
	if launchErr == nil {
		defer lock.ReleaseExclusiveNamed(h.State, supervisor.CFOLaunchLock)
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
	if err := bringCFOBack(h, agent, why, runtime.startNativeCFO, runtime.nativeTerminalRuns, func() { runtime.settleCFO(ctx, h.State, agent) }); err != nil {
		return err
	}
	if !runtime.nativeTerminalRuns(h.State, supervisor.NativeCFOTerminal) {
		return errors.New("its native terminal ended during startup")
	}
	return nil
}

// cfoContextLimit is the context, in tokens, past which a Claude Code CFO
// starts a new conversation rather than resume its last one: the window it
// compacts in, which every request of a CFO that compacts in it fits. It
// keeps the Overlord's rule (2026-09-29) that CFO sessions stay small, which
// was 20 MB of transcript until 2026-10. Claude Code's resume never loads a
// transcript whole: past 5 MiB it keeps only what follows the last
// compaction, and records of requests, which it never sends the model, were
// 94 percent of a CFO transcript's bytes, so a CFO that compacted was past
// 20 MB for good and never resumed.
const cfoContextLimit = install.AutoCompactWindow

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
		tokens, isWritten := claudeTranscriptContext(h.Root, conversation.Session)
		switch {
		case !isWritten:
			// Claude Code writes a conversation's transcript as its first
			// turn begins, and ends with code 1 on a --resume it finds none
			// for, as a home's first CFO's is until the Overlord types into it.
			return nil, fmt.Sprintf("Claude Code has written no transcript for its conversation %s, which it writes when its first turn begins", conversation.Session)
		case tokens > cfoContextLimit:
			return nil, fmt.Sprintf("Its last conversation holds %dK tokens of context, past the %dK a CFO resumes", tokens/1000, cfoContextLimit/1000)
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

// claudeTranscriptContext is the context, in tokens, that Claude Code loads
// to resume session, a conversation it held in dir, and whether there is a
// transcript to read: what the conversation's last request carried, its
// input, cached and output tokens, or what its last compaction left where no
// request followed it. Claude Code resumes a conversation from its last
// compaction, so nothing before it counts, and a subagent's request or a
// reply Claude Code wrote itself, which carries no tokens, says nothing of
// the conversation's. A transcript with none, as one whose first turn has not
// been answered, holds none. The transcript is read from its end, as far as
// that request or compaction, so a long one costs no more than a short one.
func claudeTranscriptContext(dir, session string) (int64, bool) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return 0, false
	}
	file, err := fsx.Open(filepath.Join(userHome, ".claude", "projects", claudeProjectFolder.ReplaceAllString(dir, "-"), session+".jsonl"))
	if err != nil {
		return 0, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, false
	}
	var tokens int64
	err = eachLineFromTheEnd(file, info.Size(), func(line []byte) bool {
		if !bytes.Contains(line, []byte(`"usage"`)) && !bytes.Contains(line, []byte(`"compact_boundary"`)) {
			return true
		}
		var entry struct {
			Type            string `json:"type"`
			Subtype         string `json:"subtype"`
			IsSidechain     bool   `json:"isSidechain"`
			CompactMetadata struct {
				PostTokens int64 `json:"postTokens"`
			} `json:"compactMetadata"`
			Message struct {
				Usage struct {
					InputTokens              int64 `json:"input_tokens"`
					CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
					OutputTokens             int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil {
			return true
		}
		if entry.Type == "system" && entry.Subtype == "compact_boundary" {
			tokens = entry.CompactMetadata.PostTokens
			return false
		}
		usage := entry.Message.Usage
		carried := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens + usage.OutputTokens
		if entry.Type != "assistant" || entry.IsSidechain || carried == 0 {
			return true
		}
		tokens = carried
		return false
	})
	if err != nil {
		return 0, false
	}
	return tokens, true
}

// eachLineFromTheEnd hands visit each line of file, size bytes long, the
// last first, until visit returns false, reading the file back from its end
// a chunk at a time.
func eachLineFromTheEnd(file io.ReaderAt, size int64, visit func(line []byte) bool) error {
	const chunk = 1 << 20
	var rest []byte
	for end := size; end > 0; {
		start := max(0, end-chunk)
		read := make([]byte, end-start, end-start+int64(len(rest)))
		if _, err := file.ReadAt(read, start); err != nil {
			return err
		}
		read = append(read, rest...)
		for {
			newline := bytes.LastIndexByte(read, '\n')
			if newline < 0 {
				break
			}
			if line := read[newline+1:]; len(line) > 0 && !visit(line) {
				return nil
			}
			read = read[:newline]
		}
		rest, end = read, start
	}
	if len(rest) > 0 {
		visit(rest)
	}
	return nil
}
