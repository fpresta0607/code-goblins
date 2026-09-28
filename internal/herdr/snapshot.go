package herdr

import (
	"context"
	"encoding/json"
	"fmt"
)

// SessionSnapshot is the structural topology and activity view returned by the
// upstream session.snapshot API in one response.
type SessionSnapshot struct {
	Version    string
	Protocol   int
	Workspaces []SnapshotWorkspace
	Tabs       []SnapshotTab
	Panes      []SnapshotPane
	Agents     []SnapshotAgent
	Layouts    []SnapshotLayout
}

// SnapshotLayout is one tab's layout: the size of each of its panes.
type SnapshotLayout struct {
	TabID string `json:"tab_id"`
	Panes []struct {
		ID   string `json:"pane_id"`
		Rect struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"rect"`
	} `json:"panes"`
}

// SnapshotWorkspace is the workspace identity CFO validates against.
type SnapshotWorkspace struct {
	ID    string `json:"workspace_id"`
	Label string `json:"label"`
}

// SnapshotTab is the tab identity CFO validates against.
type SnapshotTab struct {
	ID          string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

// SnapshotPane is the pane identity CFO validates against.
type SnapshotPane struct {
	ID          string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	TerminalID  string `json:"terminal_id"`
}

// SnapshotAgent is the registered agent association CFO validates against.
type SnapshotAgent struct {
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Agent       string `json:"agent"`
	Status      string `json:"agent_status"`
	// Cwd is the directory the agent is working in. It is the one live answer
	// to "which worktree is this goblin in" that does not depend on any record
	// CFO keeps, which is what the reap sweep asks it for.
	Cwd string `json:"cwd"`
}

// Snapshot reads the complete structural session state through
// `herdr api snapshot` in one typed response. The command already emits the
// typed API envelope, so it takes no --json flag.
func (c *Client) Snapshot(ctx context.Context) (SessionSnapshot, error) {
	session := c.session()
	if raw, ok := c.socketRead(ctx, "session.snapshot", map[string]any{}); ok {
		if snapshot, err := decodeSnapshot(session, raw); err == nil {
			return snapshot, nil
		}
	}
	result, err := c.required(ctx, session, Target{}, "api snapshot", "api", "snapshot")
	if err != nil {
		return SessionSnapshot{}, err
	}
	raw, code, err := envelope(result.Stdout)
	if err == nil && code != "" {
		err = fmt.Errorf("response error %s", code)
	}
	if err != nil {
		return SessionSnapshot{}, fmt.Errorf("herdr: decode api snapshot response for session %q: %w", session, err)
	}
	return decodeSnapshot(session, raw)
}

// decodeSnapshot reads a session snapshot result, as the socket answers it
// and as the herdr command wraps it.
func decodeSnapshot(session string, raw json.RawMessage) (SessionSnapshot, error) {
	var response struct {
		Type     string `json:"type"`
		Snapshot struct {
			Version    string              `json:"version"`
			Protocol   int                 `json:"protocol"`
			Workspaces []SnapshotWorkspace `json:"workspaces"`
			Tabs       []SnapshotTab       `json:"tabs"`
			Panes      []SnapshotPane      `json:"panes"`
			Agents     []SnapshotAgent     `json:"agents"`
			Layouts    []SnapshotLayout    `json:"layouts"`
		} `json:"snapshot"`
	}
	if err := decodeRaw(raw, &response); err != nil {
		return SessionSnapshot{}, fmt.Errorf("herdr: decode api snapshot response for session %q: %w", session, err)
	}
	if response.Type != "session_snapshot" {
		return SessionSnapshot{}, fmt.Errorf("herdr: api snapshot for session %q returned type %q", session, response.Type)
	}
	if response.Snapshot.Protocol == 0 {
		return SessionSnapshot{}, fmt.Errorf("herdr: api snapshot for session %q is missing protocol", session)
	}
	return SessionSnapshot{
		Version:    response.Snapshot.Version,
		Protocol:   response.Snapshot.Protocol,
		Workspaces: response.Snapshot.Workspaces,
		Tabs:       response.Snapshot.Tabs,
		Panes:      response.Snapshot.Panes,
		Agents:     response.Snapshot.Agents,
		Layouts:    response.Snapshot.Layouts,
	}, nil
}

// CaptureEvidence reads the bounded unwrapped recent terminal text the
// structural monitor consumes, on the session's socket when the client has a
// cache (the socket calls the source recent_unwrapped). The session snapshot
// carries no terminal contents, so each structurally valid task gets exactly
// one of these reads.
func (c *Client) CaptureEvidence(ctx context.Context, target Target) ([]byte, error) {
	if err := validateTarget(target); err != nil {
		return nil, err
	}
	var answer struct {
		Read *struct {
			Text *string `json:"text"`
		} `json:"read"`
	}
	scoped := *c
	scoped.Session = target.Session
	raw, ok := scoped.socketRead(ctx, "pane.read", map[string]any{"pane_id": target.Pane, "source": "recent_unwrapped", "lines": captureFloor})
	var text []byte
	if ok && decodeRaw(raw, &answer) == nil && answer.Read != nil && answer.Read.Text != nil {
		text = []byte(*answer.Read.Text)
	} else {
		result, err := c.required(ctx, target.Session, target, "pane read", "pane", "read", target.Pane, "--source", "recent-unwrapped", "--lines", fmt.Sprint(captureFloor))
		if err != nil {
			return nil, err
		}
		text = result.Stdout
	}
	if len(text) == 0 {
		return nil, fmt.Errorf("herdr: pane read for %s returned no terminal text", target)
	}
	return text, nil
}

// AgentList reads every registered agent's native state on the session's
// socket when the client has a cache, or else through `herdr agent list`
// (socket API, JSON): agent_status (working | idle | done)
// plus interactive_ready, revision, and state_change_seq. This is the primary
// supervision signal for both claude and pi panes, unlike pane-text diffing.
func (c *Client) AgentList(ctx context.Context) ([]AgentRecord, error) {
	session := c.session()
	var response struct {
		Type   string        `json:"type"`
		Agents []AgentRecord `json:"agents"`
	}
	raw, ok := c.socketRead(ctx, "agent.list", map[string]any{})
	if !ok || decodeRaw(raw, &response) != nil {
		result, err := c.required(ctx, session, Target{}, "agent list", "agent", "list")
		if err != nil {
			return nil, err
		}
		if err := decodeResult(result.Stdout, &response); err != nil {
			return nil, fmt.Errorf("herdr: decode agent list response for session %q: %w", session, err)
		}
	}
	if response.Type != "agent_list" {
		return nil, fmt.Errorf("herdr: agent list for session %q returned type %q", session, response.Type)
	}
	if response.Agents == nil {
		return nil, fmt.Errorf("herdr: agent list for session %q is missing agents", session)
	}
	return response.Agents, nil
}

// EffectiveSession reports the session every request routes to.
func (c *Client) EffectiveSession() string {
	return c.session()
}
