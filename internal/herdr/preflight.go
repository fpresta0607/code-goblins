package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// SupportedProtocol and SupportedSchemaVersion pin the one Herdr machine
// contract this build is coded against: protocol 19 with schema version 1, as
// reported by the installed 0.8.0-preview client.
const (
	SupportedProtocol      = 19
	SupportedSchemaVersion = 1
)

// requiredMethods are the typed socket methods CFO relies on. The preflight
// fails before any workspace, tab, pane, agent, or worktree mutation when the
// installed schema does not advertise every one of them.
var requiredMethods = []string{
	"server.agent_manifests",
	"session.snapshot",
	"workspace.create",
	"workspace.list",
	"workspace.rename",
	"tab.close",
	"tab.create",
	"tab.list",
	"tab.rename",
	"agent.get",
	"agent.prompt",
	"agent.start",
	"pane.close",
	"pane.get",
	"pane.list",
	"pane.read",
	"pane.send_keys",
	"pane.send_text",
}

// Preflight proves the installed Herdr speaks the contract CFO is coded
// against before any task resource is created: the local schema (version,
// protocol, envelopes, methods), then live client/server protocol
// compatibility, then selected-session addressability.
func (c *Client) Preflight(ctx context.Context) error {
	if err := c.CheckSchema(ctx); err != nil {
		return err
	}
	return c.CheckRuntime(ctx)
}

// CheckSchema parses the local `herdr api schema --json` document and
// requires the supported schema version, protocol, both response envelopes,
// and every method CFO uses. The schema command emits the bare schema
// document, not a response envelope.
func (c *Client) CheckSchema(ctx context.Context) error {
	session := c.session()
	result, err := c.required(ctx, session, Target{}, "api schema", "api", "schema", "--json")
	if err != nil {
		return err
	}
	var document struct {
		Protocol      int                        `json:"protocol"`
		SchemaVersion int                        `json:"schema_version"`
		Schemas       map[string]json.RawMessage `json:"schemas"`
	}
	if err := json.Unmarshal(result.Stdout, &document); err != nil {
		return fmt.Errorf("herdr: decode api schema response for session %q: %w", session, err)
	}
	if document.SchemaVersion != SupportedSchemaVersion {
		return fmt.Errorf("herdr: unsupported schema version %d for session %q: install a Herdr with schema version %d", document.SchemaVersion, session, SupportedSchemaVersion)
	}
	if document.Protocol != SupportedProtocol {
		return fmt.Errorf("herdr: unsupported schema protocol %d for session %q: install a Herdr with protocol %d", document.Protocol, session, SupportedProtocol)
	}
	for _, name := range []string{"success_response", "error_response"} {
		if len(document.Schemas[name]) == 0 {
			return fmt.Errorf("herdr: schema for session %q is missing the %s envelope: install a compatible Herdr", session, name)
		}
	}
	methods, err := schemaMethods(document.Schemas["request"])
	if err != nil {
		return fmt.Errorf("herdr: schema for session %q has an unreadable request contract: %w", session, err)
	}
	for _, required := range requiredMethods {
		if !methods[required] {
			return fmt.Errorf("herdr: schema for session %q does not advertise %s: install a compatible Herdr", session, required)
		}
	}
	return nil
}

// CheckRuntime parses `herdr status --json` and `herdr session list --json`
// for the selected session and requires compatible client and server
// protocols plus an addressable session, rather than inferring compatibility
// from the executable version string.
func (c *Client) CheckRuntime(ctx context.Context) error {
	session := c.session()
	result, err := c.required(ctx, session, Target{}, "status", "status", "--json")
	if err != nil {
		return err
	}
	var status struct {
		Client struct {
			Protocol int `json:"protocol"`
		} `json:"client"`
		Server struct {
			Running    *bool `json:"running"`
			Protocol   int   `json:"protocol"`
			Compatible *bool `json:"compatible"`
		} `json:"server"`
	}
	if err := json.Unmarshal(result.Stdout, &status); err != nil {
		return fmt.Errorf("herdr: decode status response for session %q: %w", session, err)
	}
	if status.Server.Running == nil || !*status.Server.Running {
		return fmt.Errorf("herdr: server for session %q is not running", session)
	}
	if status.Client.Protocol != SupportedProtocol || status.Server.Protocol != SupportedProtocol {
		return fmt.Errorf("herdr: protocol mismatch for session %q: client %d, server %d, want %d; install a compatible Herdr", session, status.Client.Protocol, status.Server.Protocol, SupportedProtocol)
	}
	if status.Server.Compatible == nil || !*status.Server.Compatible {
		return fmt.Errorf("herdr: server for session %q does not report client compatibility", session)
	}

	listResult, err := c.required(ctx, session, Target{}, "session list", "session", "list", "--json")
	if err != nil {
		return err
	}
	var list struct {
		Sessions []struct {
			Name string `json:"name"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(listResult.Stdout, &list); err != nil {
		return fmt.Errorf("herdr: decode session list response for session %q: %w", session, err)
	}
	matches := 0
	for _, entry := range list.Sessions {
		if entry.Name == session {
			matches++
		}
	}
	if matches == 0 {
		return fmt.Errorf("herdr: session %q is not addressable by this client: install a compatible Herdr", session)
	}
	if matches > 1 {
		return fmt.Errorf("herdr: session %q is ambiguous in session list", session)
	}
	return nil
}

func schemaMethods(requestSchema json.RawMessage) (map[string]bool, error) {
	if len(requestSchema) == 0 {
		return nil, errors.New("missing schemas.request")
	}
	var request struct {
		OneOf []struct {
			Properties struct {
				Method struct {
					Const string `json:"const"`
				} `json:"method"`
			} `json:"properties"`
		} `json:"oneOf"`
	}
	if err := json.Unmarshal(requestSchema, &request); err != nil {
		return nil, err
	}
	methods := make(map[string]bool, len(request.OneOf))
	for _, variant := range request.OneOf {
		if variant.Properties.Method.Const != "" {
			methods[variant.Properties.Method.Const] = true
		}
	}
	if len(methods) == 0 {
		return nil, errors.New("schemas.request advertises no methods")
	}
	return methods, nil
}
