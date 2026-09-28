package harness

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestCodexBuildsStructuredLaunchWithoutBashNotify(t *testing.T) {
	registry := DefaultRegistry()
	adapter, err := registry.Get(Codex)
	if err != nil {
		t.Fatalf("Get(Codex): %v", err)
	}

	defaults, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, GoTmp: `C:\gotmp\task`})
	if err != nil {
		t.Fatalf("Build defaults: %v", err)
	}
	assertLaunch(t, defaults, Launch{
		Args:           []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "check_for_update_on_startup=false"},
		Env:            map[string]string{"CFO_ROLE": RoleGoblin, "GOTMPDIR": `C:\gotmp\task`},
		PromptFile:     `C:\briefs\task.md`,
		TypedLaunch:    true,
		Executable:     "codex",
		ConfirmMarkers: []string{"Do you trust the contents of this directory?"},
		ConfirmKeys:    []string{"enter"},
	})

	explicit, err := adapter.Build(LaunchSpec{
		BriefPath:       `C:\briefs\task.md`,
		TaskTmp:         `C:\tasks\task`,
		GoTmp:           `C:\gotmp\task`,
		TurnEndedPath:   `C:\tasks\task\turn-ended`,
		Model:           "gpt-5.2-codex",
		Effort:          "high",
		PiExtensionPath: `C:\ignored\pi.ts`,
	})
	if err != nil {
		t.Fatalf("Build explicit: %v", err)
	}
	wantArgs := []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "check_for_update_on_startup=false", "--model", "gpt-5.2-codex", "-c", `model_reasoning_effort=high`}
	if !equalStrings(explicit.Args, wantArgs) {
		t.Errorf("Args = %#v, want %#v", explicit.Args, wantArgs)
	}
	for _, arg := range explicit.Args {
		if strings.Contains(arg, "notify=") || strings.Contains(arg, "bash") || strings.Contains(arg, "turn-ended") {
			t.Errorf("Args contained unsupported Bash notification: %#v", explicit.Args)
		}
	}

	if _, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, GoTmp: `C:\gotmp\task`, Effort: "invalid"}); err == nil {
		t.Fatal("Build returned nil error for invalid effort")
	}
}

func TestCodexValidateChecksExecutable(t *testing.T) {
	registry := DefaultRegistry()
	adapter, err := registry.Get(Codex)
	if err != nil {
		t.Fatalf("Get(Codex): %v", err)
	}
	runner := &fakeRunner{}
	if err := adapter.Validate(context.Background(), runner); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	assertRequests(t, runner.requests, []execx.Request{{Name: "codex", Args: []string{"--version"}}})
}

func TestCodexMaxEffortForAstra(t *testing.T) {
	adapter, _ := DefaultRegistry().Get(Codex)
	launch, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, GoTmp: `C:\gotmp\task`, Model: "gpt-6-astra", Effort: "max"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "check_for_update_on_startup=false", "--model", "gpt-6-astra", "-c", "model_reasoning_effort=max"}
	if !equalStrings(launch.Args, want) {
		t.Fatalf("Args = %q, want %q", launch.Args, want)
	}
}

// On 2026-09-28 a Codex goblin never started: Codex 0.154.0 knew of 0.157.1,
// so it opened its "Update available!" prompt before its composer, the spawn
// typed the goblin's brief into that prompt, a key in it left Codex, and the
// rest of the brief ran in PowerShell. A goblin never needs that prompt, so
// Codex starts without checking for an update, on either backend.
func TestCodexStartsWithoutCheckingForAnUpdate(t *testing.T) {
	adapter, _ := DefaultRegistry().Get(Codex)

	launch, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, GoTmp: `C:\gotmp\task`})

	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(launch.Args, "check_for_update_on_startup=false") {
		t.Errorf("Args = %q, want Codex's startup update check off", launch.Args)
	}
}

// On 2026-09-28 every Codex goblin started the MCP servers of the operator's
// own Codex configuration, qdrant's python and uv and a gcloud-mcp among them,
// about 2.6 GB across three goblins for tools none of them used, while Claude
// Code goblins start none (--strict-mcp-config). Each server the operator's
// configuration defines is turned off for a goblin, on either backend.
func TestCodexStartsNoneOfTheOperatorsMCPServers(t *testing.T) {
	adapter, _ := DefaultRegistry().Get(Codex)

	launch, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, GoTmp: `C:\gotmp\task`, CodexMCPServers: []string{"qdrant", "gcloud", "node_repl"}})

	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"qdrant", "gcloud", "node_repl"} {
		if !slices.Contains(launch.Args, "mcp_servers."+name+".enabled=false") {
			t.Errorf("Args = %q, want MCP server %s turned off", launch.Args, name)
		}
	}
}

// A server name Codex's -c override cannot address stops the launch rather
// than letting that server start.
func TestCodexRefusesAnMCPServerItCannotTurnOff(t *testing.T) {
	adapter, _ := DefaultRegistry().Get(Codex)

	_, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, GoTmp: `C:\gotmp\task`, CodexMCPServers: []string{"my server.v2"}})

	if err == nil || !strings.Contains(err.Error(), "my server.v2") {
		t.Fatalf("Build error = %v, want the server named", err)
	}
}

// The operator's MCP servers are read from Codex's own configuration: tables
// under mcp_servers and inline entries in a bare [mcp_servers] table, from
// CODEX_HOME when set.
func TestCodexMCPServersAreReadFromTheOperatorsConfiguration(t *testing.T) {
	home := t.TempDir()
	config := "model = \"gpt-6-astra\"\n\n[mcp_servers.qdrant]\ncommand = \"powershell.exe\"\n\n[mcp_servers.qdrant.env]\nKEY = \"x\"\n\n[mcp_servers.gcloud]\ncommand = \"npx\"\n\n[mcp_servers]\nnode_repl = { command = \"node_repl.exe\" }\n\n[plugins.\"gmail@openai-curated\"]\nenabled = true\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)

	names, err := CodexMCPServers()

	if err != nil || !slices.Equal(names, []string{"gcloud", "node_repl", "qdrant"}) {
		t.Errorf("CodexMCPServers = %q, %v; want gcloud, node_repl and qdrant", names, err)
	}
}

// With no Codex configuration there is no server to turn off.
func TestCodexMCPServersWithoutAConfiguration(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	names, err := CodexMCPServers()

	if err != nil || len(names) != 0 {
		t.Errorf("CodexMCPServers = %q, %v; want none", names, err)
	}
}
