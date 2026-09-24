package worktree

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestFilterMCPServersKeepsStdioVerbatim(t *testing.T) {
	config := []byte(`{
		"mcpServers": {
			"supabase": {
				"command": "npx",
				"args": ["-y", "@supabase/mcp-server-supabase@latest"],
				"env": {"SUPABASE_ACCESS_TOKEN": "token-ref"},
				"customField": {"nested": true}
			}
		}
	}`)
	filtered, kept, dropped, _, err := FilterMCPServers(config, everyVariableSet)
	if err != nil {
		t.Fatalf("FilterMCPServers: %v", err)
	}
	if !reflect.DeepEqual(kept, []string{"supabase"}) || len(dropped) != 0 {
		t.Fatalf("kept = %v dropped = %v, want supabase kept and nothing dropped", kept, dropped)
	}
	var document struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(filtered, &document); err != nil {
		t.Fatalf("parse filtered: %v", err)
	}
	var original struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(config, &original); err != nil {
		t.Fatal(err)
	}
	var entry map[string]any
	if err := json.Unmarshal(document.Servers["supabase"], &entry); err != nil {
		t.Fatalf("parse kept entry: %v", err)
	}
	var originalEntry map[string]any
	if err := json.Unmarshal(original.Servers["supabase"], &originalEntry); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entry, originalEntry) {
		t.Errorf("kept entry = %v, want the original entry preserved verbatim (%v)", entry, originalEntry)
	}
}

func TestFilterMCPServersQualifiesHTTPServersByToken(t *testing.T) {
	config := []byte(`{
		"mcpServers": {
			"bearer": {"url": "https://mcp.neon.tech/mcp", "bearerTokenEnvVar": "NEON_API_KEY"},
			"header": {"url": "https://example.com/mcp", "headers": {"AUTHORIZATION": "Bearer ${TOKEN}"}},
			"oauth": {"url": "https://mcp.supabase.com/mcp"},
			"plain-header": {"url": "https://example.com/mcp", "headers": {"X-Team": "ops"}}
		}
	}`)
	filtered, kept, dropped, _, err := FilterMCPServers(config, everyVariableSet)
	if err != nil {
		t.Fatalf("FilterMCPServers: %v", err)
	}
	// Unsorted here on purpose: the names are read back to the operator on
	// the spawned line, so FilterMCPServers owes them a stable order that a
	// map's iteration cannot give.
	if !reflect.DeepEqual(kept, []string{"bearer", "header"}) {
		t.Errorf("kept = %v, want bearer and header in sorted order", kept)
	}
	if !reflect.DeepEqual(dropped, []string{"oauth", "plain-header"}) {
		t.Errorf("dropped = %v, want oauth and plain-header in sorted order", dropped)
	}
	if !strings.Contains(string(filtered), `"bearer"`) || strings.Contains(string(filtered), `"oauth"`) {
		t.Errorf("filtered = %s, want only the token-authenticated servers", filtered)
	}
}

func TestFilterMCPServersDropsUnshapedEntries(t *testing.T) {
	config := []byte(`{"mcpServers": {"broken": "not-an-object"}}`)
	filtered, kept, dropped, _, err := FilterMCPServers(config, everyVariableSet)
	if err != nil {
		t.Fatalf("FilterMCPServers: %v", err)
	}
	if filtered != nil || len(kept) != 0 || !reflect.DeepEqual(dropped, []string{"broken"}) {
		t.Errorf("filtered = %s kept = %v dropped = %v, want everything dropped", filtered, kept, dropped)
	}
}

func TestFilterMCPServersEmptyConfigKeepsNothing(t *testing.T) {
	for _, config := range [][]byte{
		[]byte(`{}`),
		[]byte(`{"mcpServers": {}}`),
	} {
		filtered, kept, dropped, unset, err := FilterMCPServers(config, everyVariableSet)
		if err != nil {
			t.Fatalf("FilterMCPServers(%s): %v", config, err)
		}
		if filtered != nil || kept != nil || dropped != nil || unset != nil {
			t.Errorf("FilterMCPServers(%s) = (%s, %v, %v, %v), want all nil", config, filtered, kept, dropped, unset)
		}
	}
}

func TestFilterMCPServersRejectsMalformedJSON(t *testing.T) {
	if _, _, _, _, err := FilterMCPServers([]byte(`{oops`), everyVariableSet); err == nil {
		t.Fatal("FilterMCPServers accepted malformed JSON")
	}
}

// SIQshift's neon server is declared {url, bearerTokenEnvVar} with no type,
// and Claude reads a server without one as stdio and skips it with a warning
// on every start. A kept URL server now carries the type Claude needs, and a
// bearerTokenEnvVar, which Claude does not read, becomes the Authorization
// header Claude expands from the injected environment.
func TestFilterMCPServersTypesURLServersForClaude(t *testing.T) {
	// A regression that expands the reference fails on this stand-in rather
	// than printing the key of whoever runs the tests.
	t.Setenv("NEON_API_KEY", "neon-stand-in")
	config := []byte(`{
		"mcpServers": {
			"neon": {"url": "https://mcp.neon.tech/mcp", "bearerTokenEnvVar": "NEON_API_KEY"},
			"events": {"url": "https://example.com/sse/", "headers": {"Authorization": "Bearer ${EVENTS_TOKEN}"}},
			"typed": {"type": "sse", "url": "https://example.com/mcp", "bearerTokenEnvVar": "TYPED_TOKEN"},
			"stdio": {"command": "npx", "args": ["-y", "server"]}
		}
	}`)
	filtered, kept, _, _, err := FilterMCPServers(config, everyVariableSet)
	if err != nil || len(kept) != 4 {
		t.Fatalf("FilterMCPServers = kept %v, %v; want all four kept", kept, err)
	}
	var document struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(filtered, &document); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]map[string]any{
		"neon":   {"type": "http", "url": "https://mcp.neon.tech/mcp", "bearerTokenEnvVar": "NEON_API_KEY", "headers": map[string]any{"Authorization": "Bearer ${NEON_API_KEY}"}},
		"events": {"type": "sse", "url": "https://example.com/sse/", "headers": map[string]any{"Authorization": "Bearer ${EVENTS_TOKEN}"}},
		"typed":  {"type": "sse", "url": "https://example.com/mcp", "bearerTokenEnvVar": "TYPED_TOKEN"},
		"stdio":  {"command": "npx", "args": []any{"-y", "server"}},
	} {
		if got := document.Servers[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
}

// everyVariableSet stands in for a goblin environment that holds every token
// variable a test's servers name.
func everyVariableSet(string) bool { return true }

// A server whose only token is a bearerTokenEnvVar missing from the goblin's
// environment is withheld and named with its variable, and no Authorization
// header is ever written from a missing token: Claude would send the
// reference unexpanded, so the server could only fail or ask for an
// authentication a goblin cannot complete. A server that carries its own
// Authorization header keeps exactly that header.
func TestFilterMCPServersNeverWritesAHeaderForAMissingToken(t *testing.T) {
	config := []byte(`{
		"mcpServers": {
			"neon": {"url": "https://mcp.neon.tech/mcp", "bearerTokenEnvVar": "NEON_API_KEY"},
			"typed": {"type": "http", "url": "https://example.com/mcp", "bearerTokenEnvVar": "TYPED_TOKEN"},
			"header": {"url": "https://example.com/mcp", "bearerTokenEnvVar": "UNUSED_TOKEN", "headers": {"authorization": "Bearer ${HEADER_TOKEN}"}},
			"stdio": {"command": "npx", "args": ["-y", "server"]}
		}
	}`)
	filtered, kept, dropped, unset, err := FilterMCPServers(config, func(string) bool { return false })
	if err != nil {
		t.Fatalf("FilterMCPServers: %v", err)
	}
	if !reflect.DeepEqual(unset, []string{"neon (NEON_API_KEY)", "typed (TYPED_TOKEN)"}) {
		t.Errorf("unset = %v, want neon and typed named with their variables", unset)
	}
	if !reflect.DeepEqual(kept, []string{"header", "stdio"}) || len(dropped) != 0 {
		t.Errorf("kept = %v dropped = %v, want header and stdio kept and nothing dropped as OAuth", kept, dropped)
	}
	var document struct {
		Servers map[string]struct {
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(filtered, &document); err != nil {
		t.Fatal(err)
	}
	if got := document.Servers["header"].Headers; !reflect.DeepEqual(got, map[string]string{"authorization": "Bearer ${HEADER_TOKEN}"}) {
		t.Errorf("header's headers = %v, want only its own Authorization header", got)
	}
	for _, name := range []string{"neon", "typed"} {
		if _, ok := document.Servers[name]; ok {
			t.Errorf("filtered config holds %s, want it withheld", name)
		}
	}
}
