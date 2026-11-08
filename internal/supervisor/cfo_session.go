package supervisor

import (
	"context"
	"fmt"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// The CFO's conversation record, state/cfo-conversation.json, is written when
// its terminal registers. A harness moves to another conversation without
// registering again, as a compact, a clear or a resume does, so a record that
// is never refreshed names a conversation that stopped writing hours ago and
// the board renders it as current (issue #2172: on 2026-11-08 the record
// named a pi session last written at 18:41Z while another was being appended
// to at 00:01Z). What follows keeps the record on the live conversation, and
// says so when it cannot.

// provedCFOSession is the conversation the registered CFO's harness is in now,
// as the harness proves it itself: Claude Code's record of its own process,
// the Codex rollout of the fleet's home written last, or the pi session of
// that folder written last, once written since pi started. Empty when it
// cannot be proved, so a caller keeps the record it has.
func (s *Service) provedCFOSession(ctx context.Context) string {
	if s.Options.Tree == nil {
		return ""
	}
	primary, live := livePrimary(s.Store.Home.State)
	if !live || primary.Agent == "" {
		return ""
	}
	// The goblin's own reader proves a session from the process: Claude
	// Code's record of itself, Codex's rollout of the folder written last,
	// or the pi session of the folder written last, once written since the
	// harness started. The CFO runs in the fleet's home, so that is its
	// folder.
	goblin := fleettree.Goblin{
		Meta:           state.TaskMeta{Harness: primary.Agent, Worktree: s.Store.Home.Root},
		HarnessPID:     primary.Process.PID,
		HarnessStarted: primary.Process.Start,
	}
	session := s.Options.Tree.RunningSession(ctx, goblin)
	if session == "" {
		return ""
	}
	// A harness's own record of a process names the conversation that process
	// is in, and on a machine where one process stands for several sessions,
	// as a test's does, that is another session's. The conversation must be
	// one this home's folder keeps, or the board would follow another
	// session's words.
	if !fleettree.HoldsTranscript(s.Options.Tree.Home, primary.Agent, s.Store.Home.Root, session) {
		return ""
	}
	return session
}

// cfoConversation is the conversation the board reads a CFO's messages from:
// the live one its harness proves, with the record brought up to date when it
// named another, and why the record is stale when it could not be. A record
// that cannot be corrected is reported as stale rather than rendered as the
// current conversation.
func (h *HTTP) cfoConversation(ctx context.Context, recorded cfoState) (session, stale string) {
	_, session, _ = strings.Cut(recorded.session, "/")
	if !recorded.registered {
		return session, ""
	}
	primary, live := livePrimary(h.Service.Store.Home.State)
	if !live {
		return session, ""
	}
	if proved := h.Service.provedCFOSession(ctx); proved != "" {
		if proved == session {
			return session, ""
		}
		if err := recordCFOConversation(h.Service.Store.Home.State, primary, proved); err != nil {
			return session, fmt.Sprintf("The CFO moved to another conversation and the board could not record it, so it is reading the one before: %v", err)
		}
		return proved, ""
	}
	// Nothing proves a live conversation, so the record stands unless the
	// harness shows it names one this session never wrote.
	recordedPath := fleettree.SessionTranscript(h.Service.Options.Tree.Home, primary.Agent, session)
	if recordedPath == "" {
		return session, "The board's record names a conversation this session has written nothing to, so it cannot be read."
	}
	return session, ""
}

// refreshCFOConversation keeps the CFO's conversation record on the live
// conversation, so no board read is the first to notice a session moved.
func (s *Service) refreshCFOConversation(ctx context.Context) error {
	recorded := readCFOState(s.Store.Home.State)
	if !recorded.registered {
		return nil
	}
	primary, live := livePrimary(s.Store.Home.State)
	if !live {
		return nil
	}
	proved := s.provedCFOSession(ctx)
	_, current, _ := strings.Cut(recorded.session, "/")
	if proved == "" || proved == current {
		return nil
	}
	return recordCFOConversation(s.Store.Home.State, primary, proved)
}
