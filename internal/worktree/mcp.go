package worktree

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// mcpServerShape is the part of one MCP server entry that decides whether a
// goblin can use it unattended. Every other field is preserved verbatim when
// the server is kept.
type mcpServerShape struct {
	Type              string            `json:"type"`
	Command           string            `json:"command"`
	URL               string            `json:"url"`
	BearerTokenEnvVar string            `json:"bearerTokenEnvVar"`
	Headers           map[string]string `json:"headers"`
}

// FilterMCPServers reduces one project's .mcp.json to the servers a goblin can
// use, holding the one rule by construction: only token-authenticated servers
// may be declared for a goblin. A stdio server (command) authenticates through
// the environment the spawn injects, so it qualifies. An HTTP server qualifies
// only when it carries a static bearer token (bearerTokenEnvVar or an
// Authorization header); anything else is an OAuth connector, which prints an
// authentication prompt a goblin can never satisfy, so it is dropped and named
// in dropped. The filtered config is returned re-marshaled with each kept
// server intact; it is nil when nothing qualifies. kept and dropped are
// sorted, because they are read out to the operator and a map's iteration
// order is not.
func FilterMCPServers(config []byte) (filtered []byte, kept, dropped []string, err error) {
	var document struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(config, &document); err != nil {
		return nil, nil, nil, fmt.Errorf("worktree: parse .mcp.json: %w", err)
	}
	if len(document.Servers) == 0 {
		return nil, nil, nil, nil
	}
	remaining := map[string]json.RawMessage{}
	for name, raw := range document.Servers {
		var shape mcpServerShape
		if err := json.Unmarshal(raw, &shape); err != nil {
			dropped = append(dropped, name)
			continue
		}
		if qualifiesForGoblin(shape) {
			if strings.TrimSpace(shape.Command) == "" && shape.Type == "" {
				if raw, err = typedForClaude(raw, shape); err != nil {
					return nil, nil, nil, fmt.Errorf("worktree: type MCP server %q: %w", name, err)
				}
			}
			remaining[name] = raw
			kept = append(kept, name)
		} else {
			dropped = append(dropped, name)
		}
	}
	slices.Sort(kept)
	slices.Sort(dropped)
	if len(remaining) == 0 {
		return nil, kept, dropped, nil
	}
	filtered, err = json.Marshal(map[string]any{"mcpServers": remaining})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("worktree: marshal filtered .mcp.json: %w", err)
	}
	return filtered, kept, dropped, nil
}

// typedForClaude gives a URL server with no type the type Claude needs, sse
// for an /sse endpoint and http otherwise: Claude reads a server without one
// as stdio and skips it with a warning on every start. Claude also does not
// read bearerTokenEnvVar, so a server that authenticates only that way gets
// the same token as the Authorization header Claude expands from the
// injected environment. Every other field is kept.
func typedForClaude(raw json.RawMessage, shape mcpServerShape) (json.RawMessage, error) {
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, err
	}
	entry["type"] = "http"
	if strings.HasSuffix(strings.TrimRight(strings.TrimSpace(shape.URL), "/"), "/sse") {
		entry["type"] = "sse"
	}
	hasAuthorization := false
	headers := map[string]string{}
	for header, value := range shape.Headers {
		headers[header] = value
		hasAuthorization = hasAuthorization || strings.EqualFold(strings.TrimSpace(header), "authorization")
	}
	if token := strings.TrimSpace(shape.BearerTokenEnvVar); token != "" && !hasAuthorization {
		headers["Authorization"] = "Bearer ${" + token + "}"
		entry["headers"] = headers
	}
	return json.Marshal(entry)
}

// qualifiesForGoblin reports whether one server can authenticate without a
// human: stdio servers ride the injected environment, and HTTP servers need a
// static bearer token reference. An OAuth HTTP endpoint never qualifies.
func qualifiesForGoblin(shape mcpServerShape) bool {
	if strings.TrimSpace(shape.Command) != "" {
		return true
	}
	if strings.TrimSpace(shape.URL) == "" {
		return false
	}
	if strings.TrimSpace(shape.BearerTokenEnvVar) != "" {
		return true
	}
	for name := range shape.Headers {
		if strings.EqualFold(strings.TrimSpace(name), "authorization") {
			return true
		}
	}
	return false
}
