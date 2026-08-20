package worktree

import (
	"encoding/json"
	"fmt"
	"strings"
)

// mcpServerShape is the part of one MCP server entry that decides whether a
// goblin can use it unattended. Every other field is preserved verbatim when
// the server is kept.
type mcpServerShape struct {
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
// server intact; it is nil when nothing qualifies.
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
			remaining[name] = raw
			kept = append(kept, name)
		} else {
			dropped = append(dropped, name)
		}
	}
	if len(remaining) == 0 {
		return nil, kept, dropped, nil
	}
	filtered, err = json.Marshal(map[string]any{"mcpServers": remaining})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("worktree: marshal filtered .mcp.json: %w", err)
	}
	return filtered, kept, dropped, nil
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
