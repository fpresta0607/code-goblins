package worktree

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
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
// fail or ask for that same authentication.
//
// A server whose entry holds a value, in a command server's env map or in a
// header, is withheld and named in held unless isNamed reports that the
// task's brief names it: a value written into the entry is a credential the
// task did not ask for, and it used to reach every goblin of the project by
// the entry's shape alone. A value that only refers to a variable holds
// nothing, since the goblin's terminal carries that variable or it does not.
//
// The filtered config is returned re-marshaled with each kept server intact;
// it is nil when nothing qualifies. kept, dropped, unset and held are sorted,
// because they are read out to the operator and a map's iteration order is
// not.
func FilterMCPServers(config []byte, hasVariable, isNamed func(name string) bool) (filtered []byte, kept, dropped, unset, held []string, err error) {
	var document struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(config, &document); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("worktree: parse .mcp.json: %w", err)
	}
	if len(document.Servers) == 0 {
		return nil, nil, nil, nil, nil, nil
	}
	remaining := map[string]json.RawMessage{}
	for name, raw := range document.Servers {
		var shape mcpServerShape
		if err := json.Unmarshal(raw, &shape); err != nil {
			dropped = append(dropped, name)
			continue
		}
		if qualifiesForGoblin(shape) {
			if holdsValue(raw) && (isNamed == nil || !isNamed(name)) {
				held = append(held, name)
				continue
			}
			isURLServer := strings.TrimSpace(shape.Command) == ""
			token := strings.TrimSpace(shape.BearerTokenEnvVar)
			if isURLServer && !hasAuthorization(shape.Headers) && !hasVariable(token) {
				unset = append(unset, name+" ("+token+")")
				continue
			}
			if isURLServer && (shape.Type == "" || !hasAuthorization(shape.Headers)) {
				if raw, err = readyForClaude(raw, shape); err != nil {
					return nil, nil, nil, nil, nil, fmt.Errorf("worktree: prepare MCP server %q: %w", name, err)
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
	slices.Sort(held)
	if len(remaining) == 0 {
		return nil, kept, dropped, unset, held, nil
	}
	filtered, err = json.Marshal(map[string]any{"mcpServers": remaining})
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("worktree: marshal filtered .mcp.json: %w", err)
	}
	return filtered, kept, dropped, unset, held, nil
}

// variableReference is a value that holds nothing itself: one variable the
// harness reads from the goblin's terminal, with at most the scheme word an
// Authorization header puts before it. A reference with a default after it,
// ${NAME:-value}, holds that value and is not one.
var variableReference = regexp.MustCompile(`^(?:(?:Bearer|Basic|Token) )?\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)

// holdsValue reports whether a server's entry holds a value of its own in its
// env map or its headers: anything there that is not empty and not a
// reference to one variable. It is read as written, so a value that is no
// text, a number or a list, holds a value too.
func holdsValue(raw json.RawMessage) bool {
	var entry struct {
		Env     map[string]any `json:"env"`
		Headers map[string]any `json:"headers"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return true
	}
	for _, values := range []map[string]any{entry.Env, entry.Headers} {
		for _, value := range values {
			text, isText := value.(string)
			if !isText || (strings.TrimSpace(text) != "" && !variableReference.MatchString(strings.TrimSpace(text))) {
				return true
			}
		}
	}
	return false
}

// MCPServerNames returns, sorted, the names of the servers the project's
// .mcp.json defines, and none when the project has no such file. It reads
// names only, for the command that checks a name a brief or a grant gives.
func MCPServerNames(project string) ([]string, error) {
	data, err := fsx.ReadFile(filepath.Join(project, mcpFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("worktree: read project .mcp.json: %w", err)
	}
	return serverNames(data)
}

// serverNames returns, sorted, the names of the servers an MCP configuration
// defines.
func serverNames(config []byte) ([]string, error) {
	var document struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(config, &document); err != nil {
		return nil, fmt.Errorf("worktree: parse .mcp.json: %w", err)
	}
	names := make([]string, 0, len(document.Servers))
	for name := range document.Servers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

// mcpConfigName is a task's MCP configuration under its temporary directory,
// the file its harness is handed.
const mcpConfigName = "mcp.json"

// MCPPlan is the MCP configuration a task's next terminal is to be handed:
// the project's .mcp.json read and filtered for the task, and written nowhere
// yet. A spawn writes it at once. A relaunch plans while the task's old
// harness still runs, so a file it cannot read stops nothing, and writes once
// that harness has stopped.
type MCPPlan struct {
	// Dropped names the OAuth-only servers withheld from the goblin.
	Dropped []string
	// Unset names the servers withheld because their token variable is not
	// in the goblin's environment.
	Unset []string
	// Held names the servers withheld because their entry holds a value and
	// the task does not name them.
	Held []string
	// filtered is the configuration itself, nil when no server qualifies.
	filtered []byte
	// isDeclared reports that the project has a .mcp.json at all.
	isDeclared bool
}

// Config is where the plan's configuration is written under a task's
// temporary directory, and empty when no server qualifies.
func (p MCPPlan) Config(taskTmp string) string {
	if p.filtered == nil {
		return ""
	}
	return filepath.Join(taskTmp, mcpConfigName)
}

// PlanMCP reads the project's .mcp.json and filters it for one task.
// hasVariable reports whether a variable will be set in the goblin's
// environment, which decides whether a server that authenticates by
// bearerTokenEnvVar can be handed to it. mcpServers are the servers the task
// names: a server whose entry holds a value reaches the goblin only when it
// is among them.
func PlanMCP(project string, hasVariable func(name string) bool, mcpServers ...string) (MCPPlan, error) {
	data, err := fsx.ReadFile(filepath.Join(project, mcpFileName))
	if errors.Is(err, os.ErrNotExist) {
		return MCPPlan{}, nil
	}
	if err != nil {
		return MCPPlan{}, fmt.Errorf("worktree: read project .mcp.json: %w", err)
	}
	filtered, _, dropped, unset, held, err := FilterMCPServers(data, hasVariable, func(name string) bool { return slices.Contains(mcpServers, name) })
	if err != nil {
		return MCPPlan{}, err
	}
	return MCPPlan{Dropped: dropped, Unset: unset, Held: held, filtered: filtered, isDeclared: true}, nil
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
