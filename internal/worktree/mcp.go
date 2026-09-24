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
// in dropped. A server whose only token is a bearerTokenEnvVar that
// hasVariable does not report in the goblin's environment is withheld too and
// named in unset as "server (VARIABLE)": kept without its token, it could only
// fail or ask for that same authentication. The filtered config is returned
// re-marshaled with each kept server intact; it is nil when nothing
// qualifies. kept, dropped and unset are sorted, because they are read out to
// the operator and a map's iteration order is not.
func FilterMCPServers(config []byte, hasVariable func(name string) bool) (filtered []byte, kept, dropped, unset []string, err error) {
	var document struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(config, &document); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("worktree: parse .mcp.json: %w", err)
	}
	if len(document.Servers) == 0 {
		return nil, nil, nil, nil, nil
	}
	remaining := map[string]json.RawMessage{}
	for name, raw := range document.Servers {
		var shape mcpServerShape
		if err := json.Unmarshal(raw, &shape); err != nil {
			dropped = append(dropped, name)
			continue
		}
		if qualifiesForGoblin(shape) {
			isURLServer := strings.TrimSpace(shape.Command) == ""
			token := strings.TrimSpace(shape.BearerTokenEnvVar)
			if isURLServer && !hasAuthorization(shape.Headers) && !hasVariable(token) {
				unset = append(unset, name+" ("+token+")")
				continue
			}
			if isURLServer && (shape.Type == "" || !hasAuthorization(shape.Headers)) {
				if raw, err = readyForClaude(raw, shape); err != nil {
					return nil, nil, nil, nil, fmt.Errorf("worktree: prepare MCP server %q: %w", name, err)
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
	slices.Sort(unset)
	if len(remaining) == 0 {
		return nil, kept, dropped, unset, nil
	}
	filtered, err = json.Marshal(map[string]any{"mcpServers": remaining})
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("worktree: marshal filtered .mcp.json: %w", err)
	}
	return filtered, kept, dropped, unset, nil
}

// readyForClaude gives a URL server with no type the type Claude needs, sse
// for an /sse endpoint and http otherwise: Claude reads a server without one
// as stdio and skips it with a warning on every start. An explicit type is
// kept. Claude also does not read bearerTokenEnvVar, so a server that
// authenticates only that way, typed or not, gets the same token as the
// Authorization header Claude expands from the injected environment. The
// header holds the variable reference, never its value. Every other field is
// kept.
func readyForClaude(raw json.RawMessage, shape mcpServerShape) (json.RawMessage, error) {
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, err
	}
	if shape.Type == "" {
		entry["type"] = "http"
		if strings.HasSuffix(strings.TrimRight(strings.TrimSpace(shape.URL), "/"), "/sse") {
			entry["type"] = "sse"
		}
	}
	if token := strings.TrimSpace(shape.BearerTokenEnvVar); token != "" && !hasAuthorization(shape.Headers) {
		headers := map[string]string{"Authorization": "Bearer ${" + token + "}"}
		for header, value := range shape.Headers {
			headers[header] = value
		}
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
	return strings.TrimSpace(shape.BearerTokenEnvVar) != "" || hasAuthorization(shape.Headers)
}

func hasAuthorization(headers map[string]string) bool {
	for name := range headers {
		if strings.EqualFold(strings.TrimSpace(name), "authorization") {
			return true
		}
	}
	return false
}
