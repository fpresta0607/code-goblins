// Package nativehook translates harness lifecycle notifications into small,
// credential-free durable events. Hooks do no network or Git work.
package nativehook

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const MaxInputBytes = 64 << 10
const MaxQueuedEvents = 4096

type Event struct {
	Schema          int       `json:"schema"`
	ID              string    `json:"id"`
	Harness         string    `json:"harness"`
	Kind            string    `json:"kind"`
	SessionID       string    `json:"session_id"`
	TaskID          string    `json:"task_id,omitempty"`
	Role            string    `json:"role"`
	TurnID          string    `json:"turn_id,omitempty"`
	Generation      string    `json:"generation,omitempty"`
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	ParentHarness   string    `json:"parent_harness,omitempty"`
	RootSessionID   string    `json:"root_session_id,omitempty"`
	Relation        string    `json:"relation,omitempty"`
	Model           string    `json:"model,omitempty"`
	AgentType       string    `json:"agent_type,omitempty"`
	CWD             string    `json:"cwd"`
	OccurredAt      time.Time `json:"occurred_at"`
}

type Context struct {
	Harness, Role, TaskID, Generation             string
	ParentSessionID, ParentHarness, RootSessionID string
	Now                                           time.Time
}

func Normalize(r io.Reader, c Context) (Event, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxInputBytes+1))
	if err != nil {
		return Event{}, err
	}
	if len(data) > MaxInputBytes {
		return Event{}, errors.New("native hook input exceeds 64 KiB")
	}
	// Only these protocol fields enter the spool. Prompts, transcripts, tool
	// results, arguments, and environment values are intentionally not retained.
	var p struct {
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
		Event     string `json:"hook_event_name"`
		TurnID    string `json:"turn_id"`
		Pending   bool   `json:"pending_messages"`
		AgentID   string `json:"agent_id"`
		AgentType string `json:"agent_type"`
		Model     string `json:"model"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return Event{}, fmt.Errorf("native hook JSON: %w", err)
	}
	kind := ""
	switch c.Harness {
	case "claude", "codex":
		switch p.Event {
		case "SessionStart":
			kind = "started"
		case "UserPromptSubmit", "PreToolUse", "PostToolUse":
			kind = "active"
		case "Stop":
			kind = "settled"
		case "SessionEnd":
			kind = "ended"
		case "Interrupt":
			kind = "interrupted"
		case "SubagentStart":
			kind = "started"
		case "SubagentStop":
			kind = "settled"
		}
	case "pi":
		switch p.Event {
		case "session_start":
			kind = "started"
		case "agent_start", "tool_execution_start", "tool_execution_end":
			kind = "active"
		case "agent_settled":
			if !p.Pending {
				kind = "settled"
			}
		case "session_shutdown":
			kind = "ended"
		}
	}
	if kind == "" {
		return Event{}, fmt.Errorf("unsupported or premature %s event %q", c.Harness, p.Event)
	}
	if c.Now.IsZero() {
		c.Now = time.Now()
	}
	if c.Role == "" && c.TaskID == "" {
		c.Role = "cfo"
	}
	e := Event{Schema: 1, Harness: c.Harness, Kind: kind, SessionID: p.SessionID, TaskID: c.TaskID, Role: c.Role, TurnID: p.TurnID, Generation: c.Generation, CWD: p.CWD, OccurredAt: c.Now.UTC()}
	e.Model, e.AgentType = p.Model, p.AgentType
	e.ParentSessionID, e.ParentHarness, e.RootSessionID = c.ParentSessionID, c.ParentHarness, c.RootSessionID
	if e.ParentSessionID != "" {
		e.Relation = "spawned"
	}
	if p.Event == "SubagentStart" || p.Event == "SubagentStop" {
		if p.AgentID == "" {
			return Event{}, errors.New("subagent event needs an agent ID")
		}
		e.ParentSessionID, e.ParentHarness, e.SessionID, e.Role, e.Relation = p.SessionID, c.Harness, p.AgentID, "subagent", "delegated"
	}
	// A Stop hook can request continuation within the same native turn. Turn
	// identity therefore cannot identify an invocation. Keep the random ID in
	// the durable record: retry/replay of that record retains its identity.
	var invocation [32]byte
	if _, err := rand.Read(invocation[:]); err != nil {
		return Event{}, err
	}
	e.ID = hex.EncodeToString(invocation[:])
	return e, e.Validate()
}

func (e Event) Validate() error {
	if e.Schema != 1 {
		return errors.New("unsupported native event schema")
	}
	id, err := hex.DecodeString(e.ID)
	if err != nil || len(id) != 32 {
		return errors.New("invalid native event ID")
	}
	if e.Harness != "claude" && e.Harness != "codex" && e.Harness != "pi" {
		return errors.New("unsupported harness")
	}
	switch e.Kind {
	case "started", "active", "settled", "ended", "interrupted":
	default:
		return errors.New("invalid event kind")
	}
	if e.Role != "cfo" && e.Role != "goblin" && e.Role != "subagent" && e.Role != "worker" {
		return errors.New("invalid session role")
	}
	if e.Role == "goblin" {
		if err := state.ValidTaskID(e.TaskID); err != nil {
			return err
		}
	} else if e.Role == "cfo" && e.TaskID != "" {
		return errors.New("CFO session cannot claim a goblin task")
	}
	if !filepath.IsAbs(e.CWD) || e.OccurredAt.IsZero() {
		return errors.New("event needs an absolute cwd and timestamp")
	}
	if e.SessionID == "" {
		return errors.New("event needs a native session ID")
	}
	if e.TaskID != "" {
		if err := state.ValidTaskID(e.TaskID); err != nil {
			return err
		}
	}
	if e.ParentSessionID != "" && (e.ParentHarness != "claude" && e.ParentHarness != "codex" && e.ParentHarness != "pi") {
		return errors.New("parent harness is required")
	}
	if e.ParentSessionID != "" && e.Relation != "spawned" && e.Relation != "delegated" {
		return errors.New("invalid lineage relation")
	}
	for _, value := range []string{e.SessionID, e.TurnID, e.Generation, e.CWD, e.ParentSessionID, e.ParentHarness, e.RootSessionID, e.Model, e.AgentType} {
		if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("invalid event identity")
		}
	}
	return nil
}

func SpoolDir(stateDir string) string { return filepath.Join(stateDir, "native-inbox") }

func Spool(stateDir string, e Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	dir := SpoolDir(stateDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) >= MaxQueuedEvents {
		return errors.New("native event spool is full; start or repair cfo serve")
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(dir, e.ID+".event.json"), append(data, '\n'))
}
