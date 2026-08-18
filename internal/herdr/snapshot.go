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
}

// SnapshotAgent is the registered agent association CFO validates against.
type SnapshotAgent struct {
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Agent       string `json:"agent"`
	Status      string `json:"agent_status"`
}

// Snapshot reads the complete structural session state through
// `herdr api snapshot` in one typed response. The command already emits the
// typed API envelope, so it takes no --json flag.
func (c *Client) Snapshot(ctx context.Context) (SessionSnapshot, error) {
	session := c.session()
	result, err := c.required(ctx, session, Target{}, "api snapshot", "api", "snapshot")
	if err != nil {
		return SessionSnapshot{}, err
	}
	var response struct {
		Type     string `json:"type"`
		Snapshot struct {
			Version    string              `json:"version"`
			Protocol   int                 `json:"protocol"`
			Workspaces []SnapshotWorkspace `json:"workspaces"`
			Tabs       []SnapshotTab       `json:"tabs"`
			Panes      []SnapshotPane      `json:"panes"`
			Agents     []SnapshotAgent     `json:"agents"`
		} `json:"snapshot"`
	}
	if err := decodeResult(result.Stdout, &response); err != nil {
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
	}, nil
}

// CaptureEvidence reads the bounded unwrapped recent terminal text the
// structural monitor consumes. The session snapshot carries no terminal
// contents, so each structurally valid task gets exactly one of these reads.
func (c *Client) CaptureEvidence(ctx context.Context, target Target) ([]byte, error) {
	if err := validateTarget(target); err != nil {
		return nil, err
	}
	result, err := c.required(ctx, target.Session, target, "pane read", "pane", "read", target.Pane, "--source", "recent-unwrapped", "--lines", fmt.Sprint(captureFloor))
	if err != nil {
		return nil, err
	}
	if len(result.Stdout) == 0 {
		return nil, fmt.Errorf("herdr: pane read for %s returned no terminal text", target)
	}
	return result.Stdout, nil
}

// AgentList reads every registered agent's native state through
// `herdr agent list` (socket API, JSON): agent_status (working | idle | done)
// plus interactive_ready, revision, and state_change_seq. This is the primary
// supervision signal for both claude and pi panes, unlike pane-text diffing.
func (c *Client) AgentList(ctx context.Context) ([]AgentRecord, error) {
	session := c.session()
	result, err := c.required(ctx, session, Target{}, "agent list", "agent", "list")
	if err != nil {
		return nil, err
	}
	var response struct {
		Result struct {
			Agents []AgentRecord `json:"agents"`
		} `json:"result"`
	}
	if err := json.Unmarshal(result.Stdout, &response); err != nil {
		return nil, fmt.Errorf("herdr: decode agent list response for session %q: %w", session, err)
	}
	return response.Result.Agents, nil
}

// EffectiveSession reports the session every request routes to.
func (c *Client) EffectiveSession() string {
	return c.session()
}
