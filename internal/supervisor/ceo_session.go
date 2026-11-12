package supervisor

import (
	"context"
	"fmt"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// The CEO's conversation record, state/ceo-conversation.json, is written when
// its terminal registers. A harness moves to another conversation without
// registering again, as a compact, a clear or a resume does, so a record that
// is never refreshed names a conversation that stopped writing hours ago and
// the board renders it as current (issue #2172: on 2026-11-08 the record
// named a pi session last written at 18:41Z while another was being appended
// to at 00:01Z). What follows keeps the record on the live conversation, and
// says so when it cannot.

// provedCEOSession is the conversation the registered CEO's harness is in now,
// as the harness proves it itself: Claude Code's record of its own process,
// the Codex rollout of the fleet's home written last, or the pi session of
// that folder written last, once written since pi started. Empty when it
// cannot be proved, so a caller keeps the record it has.
func (s *Service) provedCEOSession(ctx context.Context) string {
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
	// harness started. The CEO runs in the fleet's home, so that is its
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

// ceoConversation is the conversation the board reads a CEO's messages from:
// the live one its harness proves, with the record brought up to date when it
// named another, and why the record is stale when it could not be. A record
// that cannot be corrected is reported as stale rather than rendered as the
// current conversation.
func (h *HTTP) ceoConversation(ctx context.Context, recorded ceoState) (session, stale string) {
	_, session, _ = strings.Cut(recorded.session, "/")
	if !recorded.registered {
		return session, ""
	}
	primary, live := livePrimary(h.Service.Store.Home.State)
	if !live {
		return session, ""
	}
	if proved := h.Service.provedCEOSession(ctx); proved != "" {
		if proved == session {
			return session, ""
		}
		if err := recordCEOConversation(h.Service.Store.Home.State, primary, proved); err != nil {
			return session, fmt.Sprintf("The CEO moved to another conversation and the board could not record it, so it is reading the one before: %v", err)
		}
		return proved, ""
	}
	// Nothing proves a live conversation, so the record stands only while this
	// home's own folder holds it. A record naming a conversation another
	// folder keeps, or one never written, is reported stale: Codex keeps no
	// folder of its own, so its record also needs the rollout to exist.
	transcripts, folder := h.Service.Options.Tree.Home, h.Service.Store.Home.Root
	if !fleettree.HoldsTranscript(transcripts, primary.Agent, folder, session) || fleettree.HeldTranscript(transcripts, primary.Agent, folder, session) == "" {
		return session, fmt.Sprintf("The board's record names conversation %s, which the fleet home's own folder does not hold, so it is reported stale rather than drawn as the current one.", session)
	}
	return session, ""
}

// refreshCEOConversation keeps the CEO's conversation record on the live
// conversation, so no board read is the first to notice a session moved.
func (s *Service) refreshCEOConversation(ctx context.Context) error {
	recorded := readCEOState(s.Store.Home.State)
	if !recorded.registered {
		return nil
	}
	primary, live := livePrimary(s.Store.Home.State)
	if !live {
		return nil
	}
	proved := s.provedCEOSession(ctx)
	_, current, _ := strings.Cut(recorded.session, "/")
	if proved == "" || proved == current {
		return nil
	}
	return recordCEOConversation(s.Store.Home.State, primary, proved)
}
