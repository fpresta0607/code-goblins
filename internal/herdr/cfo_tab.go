package herdr

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// cfoTabLabel is the CFO's own tab in the fleet workspace: the factory tab
// EnsureContainer adopts, or the tab the launcher creates beside the goblins.
const cfoTabLabel = "cfo"

// CFOTab returns the pane of the CFO's tab in container, and whether an agent
// already runs in any of its panes, which is a CFO session the launcher must
// not start a second time. Every pane of every cfo tab is checked. Herdr
// starts an agent in its pane's own directory, so a cfo tab with no agent in
// any pane is never reused: a fresh cfo tab is created in cwd first, so the
// workspace never loses its last tab, and then each old one is closed when
// every one of its panes sits at its shell prompt or renamed to shell
// otherwise, since whatever runs there, goblins itself among them, is never
// closed.
func (c *Client) CFOTab(ctx context.Context, container Container, cwd string) (Endpoint, bool, error) {
	if container.Session == "" || container.WorkspaceID == "" {
		return Endpoint{}, false, errors.New("herdr: the CFO tab needs a session and workspace_id")
	}
	tabs, err := c.tabs(ctx, container.Session, container.WorkspaceID)
	if err != nil {
		return Endpoint{}, false, err
	}
	panes, err := c.panes(ctx, container.Session, container.WorkspaceID)
	if err != nil {
		return Endpoint{}, false, err
	}
	type staleTab struct {
		id      string
		targets []Target
	}
	var stale []staleTab
	for _, tab := range tabs {
		if tab.Label != cfoTabLabel {
			continue
		}
		if tab.ID == "" {
			return Endpoint{}, false, errors.New("herdr: the cfo tab has no tab_id")
		}
		old := staleTab{id: tab.ID}
		for _, pane := range panes {
			if pane.TabID != tab.ID || pane.ID == "" {
				continue
			}
			target := Target{Session: container.Session, Pane: pane.ID}
			status, err := c.AgentStatus(ctx, target)
			if err != nil {
				return Endpoint{}, false, err
			}
			if status != AgentMissing && status != AgentDead {
				return Endpoint{Target: target, WorkspaceID: container.WorkspaceID, TabID: tab.ID, PaneID: pane.ID}, true, nil
			}
			old.targets = append(old.targets, target)
		}
		if len(old.targets) == 0 {
			return Endpoint{}, false, fmt.Errorf("herdr: the cfo tab %s has no pane", tab.ID)
		}
		stale = append(stale, old)
	}

	result, err := c.required(ctx, container.Session, Target{}, "tab create", "tab", "create", "--workspace", container.WorkspaceID, "--cwd", cwd, "--label", cfoTabLabel, "--no-focus")
	if err != nil {
		return Endpoint{}, false, err
	}
	var create struct {
		Tab struct {
			ID string `json:"tab_id"`
		} `json:"tab"`
		RootPane struct {
			ID string `json:"pane_id"`
		} `json:"root_pane"`
	}
	if err := decodeResult(result.Stdout, &create); err != nil {
		return Endpoint{}, false, fmt.Errorf("herdr: decode tab create response: %w", err)
	}
	if create.Tab.ID == "" || create.RootPane.ID == "" {
		return Endpoint{}, false, errors.New("herdr: tab create response is missing tab_id or root pane_id")
	}
	for _, old := range stale {
		idle := slices.IndexFunc(old.targets, func(target Target) bool {
			info, err := c.PaneProcessInfo(ctx, target)
			return err != nil || info.ForegroundProcessGroupID != info.ShellPID
		}) < 0
		if idle {
			if err := c.CloseTab(ctx, container.Session, old.id); err != nil {
				return Endpoint{}, false, err
			}
			continue
		}
		if _, err := c.required(ctx, container.Session, Target{}, "tab rename", "tab", "rename", old.id, "shell"); err != nil {
			return Endpoint{}, false, err
		}
	}
	target := Target{Session: container.Session, Pane: create.RootPane.ID}
	return Endpoint{Target: target, WorkspaceID: container.WorkspaceID, TabID: create.Tab.ID, PaneID: create.RootPane.ID}, false, nil
}

// Focus brings the endpoint's workspace and tab to the front, so the next
// client to attach opens on it.
func (c *Client) Focus(ctx context.Context, endpoint Endpoint) error {
	if _, err := c.required(ctx, endpoint.Target.Session, Target{}, "workspace focus", "workspace", "focus", endpoint.WorkspaceID); err != nil {
		return err
	}
	_, err := c.required(ctx, endpoint.Target.Session, Target{}, "tab focus", "tab", "focus", endpoint.TabID)
	return err
}
