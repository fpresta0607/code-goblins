package herdr

import "context"

// Focus brings the endpoint's workspace and tab to the front, so the next
// client to attach opens on it.
func (c *Client) Focus(ctx context.Context, endpoint Endpoint) error {
	if _, err := c.required(ctx, endpoint.Target.Session, Target{}, "workspace focus", "workspace", "focus", endpoint.WorkspaceID); err != nil {
		return err
	}
	_, err := c.required(ctx, endpoint.Target.Session, Target{}, "tab focus", "tab", "focus", endpoint.TabID)
	return err
}
