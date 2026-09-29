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
		Args:           []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "check_for_update_on_startup=false", "-c", "tui.animations=false"},
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
	wantArgs := []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "check_for_update_on_startup=false", "-c", "tui.animations=false", "--model", "gpt-5.2-codex", "-c", `model_reasoning_effort=high`}
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
	want := []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "check_for_update_on_startup=false", "-c", "tui.animations=false", "--model", "gpt-6-astra", "-c", "model_reasoning_effort=max"}
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

// In a Herdr pane (TERM=xterm-256color) Codex 0.154 draws an idle animation
// of braille dots over every empty cell, the spaces of its composer among
// them, so a message it held read as gone and was never submitted again, seen
// live on 2026-09-29. A goblin starts Codex with its animations off, on either
// backend.
func TestCodexStartsWithItsAnimationsOff(t *testing.T) {
	adapter, _ := DefaultRegistry().Get(Codex)

	launch, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, GoTmp: `C:\gotmp\task`})

	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(launch.Args, "tui.animations=false") {
		t.Errorf("Args = %q, want Codex's animations off", launch.Args)
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

// Every form TOML allows for defining a server is found, and a quoted name
// comes back without its quotes, so a quoted name that is a plain key is
// turned off like any other.
func TestCodexMCPServersFindEveryFormOfDefinition(t *testing.T) {
	for name, test := range map[string]struct {
		config string
		want   []string
	}{
		"table":                        {"[mcp_servers.qdrant]\ncommand = \"uv\"\n", []string{"qdrant"}},
		"quoted table":                 {"[mcp_servers.\"qdrant\"]\ncommand = \"uv\"\n", []string{"qdrant"}},
		"literal quoted table":         {"[ mcp_servers . 'qdrant' ]\ncommand = \"uv\"\n", []string{"qdrant"}},
		"quoted table with a dot":      {"[mcp_servers.\"my.server\".env]\nKEY = \"x\"\n", []string{"my.server"}},
		"inline entry":                 {"[mcp_servers]\nnode_repl = { command = \"node\" }\n", []string{"node_repl"}},
		"quoted inline entry":          {"[mcp_servers]\n\"node_repl\" = { command = \"node\" }\n", []string{"node_repl"}},
		"dotted entry":                 {"[mcp_servers]\nqdrant.command = \"uv\"\nqdrant.args = [\"x\"]\n", []string{"qdrant"}},
		"quoted dotted entry":          {"[mcp_servers]\n\"qdrant\" . command = \"uv\"\n", []string{"qdrant"}},
		"top-level dotted key":         {"model = \"gpt\"\nmcp_servers.qdrant.command = \"uv\"\n", []string{"qdrant"}},
		"top-level quoted dotted key":  {"mcp_servers.\"gcloud\".command = \"npx\"\n", []string{"gcloud"}},
		"top-level inline entry":       {"mcp_servers.qdrant = { command = \"uv\" }\n", []string{"qdrant"}},
		"key of another table":         {"[profiles.work]\nmcp_servers.qdrant.command = \"uv\"\n", nil},
		"key of a server's subtable":   {"[mcp_servers.qdrant.env]\nKEY = \"x\"\n", []string{"qdrant"}},
		"commented out":                {"# [mcp_servers.qdrant]\n[mcp_servers]\n# gcloud = { command = \"npx\" }\n", nil},
		"another table named likewise": {"[mcp_servers_extra.qdrant]\ncommand = \"uv\"\n", nil},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(test.config), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CODEX_HOME", home)

			names, err := CodexMCPServers()

			if err != nil || !slices.Equal(names, test.want) {
				t.Errorf("CodexMCPServers = %q, %v; want %q", names, err, test.want)
			}
		})
	}
}

// A quoted server name that is a plain key is turned off; one that really
// needs its quotes still stops the launch.
func TestCodexTurnsOffAQuotedServerNameItCanAddress(t *testing.T) {
	home := t.TempDir()
	config := "[mcp_servers.\"qdrant\"]\ncommand = \"uv\"\n\n[mcp_servers.\"my server\"]\ncommand = \"npx\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	adapter, _ := DefaultRegistry().Get(Codex)
	servers, err := CodexMCPServers()
	if err != nil {
		t.Fatal(err)
	}

	_, err = adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, GoTmp: `C:\gotmp\task`, CodexMCPServers: servers})
	plain, plainErr := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, GoTmp: `C:\gotmp\task`, CodexMCPServers: servers[1:]})

	if err == nil || !strings.Contains(err.Error(), `"my server"`) {
		t.Errorf("Build error = %v, want the server that needs quotes named", err)
	}
	if plainErr != nil || !slices.Contains(plain.Args, "mcp_servers.qdrant.enabled=false") {
		t.Errorf("Build = %q, %v; want qdrant turned off", plain.Args, plainErr)
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

// A spawn that continues past Codex's hook review names the hooks the session
// loads: the operator's hooks.json, then the project's in its main checkout,
// from which Codex takes a worktree's project hooks, then the worktree's own
// only when it differs, each command hook as its event and command; a file
// that is not there lists none.
func TestCodexHooksListsTheCommandHooksASessionLoads(t *testing.T) {
	home, project, worktree := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	operator := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"herdr-state session"}]},{"hooks":[{"type":"command","command":"siqshift --event session-start"}]}],"SessionEnd":[{"hooks":[{"type":"command","command":"siqshift --event session-end"}]}]}}`
	main := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"cfo hook stop"},{"type":"prompt","command":"not a command"}]}]}}`
	edited := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"goblin hook stop"}]}]}}`
	operatorHooks := []string{"SessionEnd: siqshift --event session-end", "SessionStart: herdr-state session", "SessionStart: siqshift --event session-start"}
	for name, test := range map[string]struct {
		operator, main, worktree string
		want                     []string
	}{
		"none":                  {"", "", "", nil},
		"operator only":         {operator, "", "", operatorHooks},
		"main checkout":         {operator, main, "", append(slices.Clone(operatorHooks), "Stop: cfo hook stop")},
		"worktree as main":      {operator, main, main, append(slices.Clone(operatorHooks), "Stop: cfo hook stop")},
		"worktree differs":      {operator, main, edited, append(slices.Clone(operatorHooks), "Stop: cfo hook stop", "Stop: goblin hook stop")},
		"worktree without main": {"", "", main, []string{"Stop: cfo hook stop"}},
	} {
		t.Run(name, func(t *testing.T) {
			writeHooks(t, filepath.Join(home, "hooks.json"), test.operator)
			writeHooks(t, filepath.Join(project, ".codex", "hooks.json"), test.main)
			writeHooks(t, filepath.Join(worktree, ".codex", "hooks.json"), test.worktree)

			hooks, err := CodexHooks(project, worktree)

			if err != nil || !slices.Equal(hooks, test.want) {
				t.Errorf("CodexHooks = %q, %v; want %q", hooks, err, test.want)
			}
		})
	}
}

// A hooks file that cannot be parsed is named in the error while the hooks of
// every other file are still listed.
func TestCodexHooksListsTheOthersWhenOneFileCannotBeParsed(t *testing.T) {
	home, project, worktree := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	broken := filepath.Join(home, "hooks.json")
	writeHooks(t, broken, `{"hooks":{"SessionStart":{"type":"command"}}}`)
	writeHooks(t, filepath.Join(project, ".codex", "hooks.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"cfo hook stop"}]}]}}`)

	hooks, err := CodexHooks(project, worktree)

	if !slices.Equal(hooks, []string{"Stop: cfo hook stop"}) {
		t.Errorf("CodexHooks = %q, want the project's hook still listed", hooks)
	}
	if err == nil || !strings.Contains(err.Error(), broken) {
		t.Errorf("CodexHooks error = %v, want %s named", err, broken)
	}
}

// writeHooks writes content to the hooks file at path, or removes the file for
// no content.
func writeHooks(t *testing.T, path, content string) {
	t.Helper()
	_ = os.Remove(path)
	if content == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
