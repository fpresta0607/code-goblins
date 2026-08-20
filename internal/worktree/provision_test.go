package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// provisionFixture builds a project and worktree pair under one temp root and
// returns them with the runner Provision should drive.
func provisionFixture(t *testing.T) (project, worktreePath string, runner *scriptedRunner) {
	t.Helper()
	root := t.TempDir()
	project = filepath.Join(root, "demo")
	worktreePath = filepath.Join(project, ".worktrees", "gb-task")
	for _, dir := range []string{project, worktreePath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return project, worktreePath, &scriptedRunner{}
}

// ignoredScript answers `git check-ignore` with already-ignored for every name
// the provisioning pass asks about, so no info/exclude writes happen.
func ignoredScript(count int) []scriptedResult {
	results := make([]scriptedResult, count)
	return results
}

// unignoredScript answers the check-ignore conversation for names that the
// project does not ignore: exit 1, then the common-dir probe pointing at
// gitDir, per name.
func unignoredScript(gitDir string, names ...string) []scriptedResult {
	results := []scriptedResult{}
	for range names {
		results = append(results,
			scriptedResult{result: execx.Result{ExitCode: 1}},
			scriptedResult{result: execx.Result{Stdout: []byte(gitDir + "\n")}},
		)
	}
	return results
}

func TestProvisionNoOpsOnABareProject(t *testing.T) {
	project, worktreePath, runner := provisionFixture(t)
	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %#v, want none for a project with nothing to share", runner.calls)
	}
	if result.HasMCP || len(result.Linked) != 0 || result.Installed != "" {
		t.Errorf("result = %+v, want an empty provisioning", result)
	}
}

func TestProvisionHardlinksConfigFiles(t *testing.T) {
	project, worktreePath, runner := provisionFixture(t)
	source := filepath.Join(project, ".env")
	if err := os.WriteFile(source, []byte("DATABASE_URL=postgres://x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(project, ".git")
	runner.results = unignoredScript(gitDir, ".env")

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	destination := filepath.Join(worktreePath, ".env")
	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	destinationInfo, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("shared .env missing from the worktree: %v", err)
	}
	if !os.SameFile(sourceInfo, destinationInfo) {
		t.Error("worktree .env is a copy, want a hardlink to the primary checkout's file")
	}
	if !slices.Contains(result.Linked, ".env") {
		t.Errorf("Linked = %v, want .env named", result.Linked)
	}
	exclude, err := os.ReadFile(filepath.Join(gitDir, "info", "exclude"))
	if err != nil {
		t.Fatalf("read info/exclude: %v", err)
	}
	if !strings.Contains(string(exclude), ".env") {
		t.Errorf("info/exclude = %q, want .env registered so goblin git status stays clean", exclude)
	}
}

func TestProvisionRespectsExistingIgnoreRules(t *testing.T) {
	project, worktreePath, runner := provisionFixture(t)
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("K=V\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(project, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner.results = ignoredScript(1)

	if _, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %#v, want just the check-ignore probe", runner.calls)
	}
	want := execx.Request{Dir: worktreePath, Name: "git", Args: []string{"check-ignore", "-q", "--", ".env"}}
	if runner.calls[0].Dir != want.Dir || runner.calls[0].Name != want.Name || !slices.Equal(runner.calls[0].Args, want.Args) {
		t.Errorf("call = %#v, want %#v", runner.calls[0], want)
	}
	if _, err := os.Stat(filepath.Join(gitDir, "info", "exclude")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("info/exclude was written although the project already ignores .env: %v", err)
	}
}

func TestProvisionInstallsFromTheLockfile(t *testing.T) {
	project, worktreePath, runner := provisionFixture(t)
	if err := os.WriteFile(filepath.Join(worktreePath, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(project, ".git")
	runner.results = append(unignoredScript(gitDir, "node_modules"), scriptedResult{})

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if result.Installed != "pnpm install --frozen-lockfile" {
		t.Errorf("Installed = %q, want the pnpm lockfile command", result.Installed)
	}
	last := runner.calls[len(runner.calls)-1]
	if last.Dir != worktreePath || last.Name != "pnpm" || !slices.Equal(last.Args, []string{"install", "--frozen-lockfile"}) {
		t.Errorf("install call = %#v, want pnpm install --frozen-lockfile in the worktree", last)
	}
}

func TestProvisionManifestOverridesTheInstallCommands(t *testing.T) {
	project, worktreePath, runner := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{
		Project:      "demo",
		Dependencies: Dependencies{Install: []string{"uv venv", "uv pip install -r requirements.txt"}},
	})
	gitDir := filepath.Join(project, ".git")
	runner.results = append(unignoredScript(gitDir, ".venv"), scriptedResult{}, scriptedResult{})

	result, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if result.Installed != "uv venv && uv pip install -r requirements.txt" {
		t.Errorf("Installed = %q, want the manifest override", result.Installed)
	}
	installs := runner.calls[len(runner.calls)-2:]
	if installs[0].Name != "uv" || !slices.Equal(installs[0].Args, []string{"venv"}) {
		t.Errorf("first install call = %#v, want uv venv", installs[0])
	}
	if installs[1].Name != "uv" || !slices.Equal(installs[1].Args, []string{"pip", "install", "-r", "requirements.txt"}) {
		t.Errorf("second install call = %#v, want uv pip install -r requirements.txt", installs[1])
	}
	for _, call := range installs {
		if call.Dir != worktreePath {
			t.Errorf("install call Dir = %q, want the worktree %q", call.Dir, worktreePath)
		}
	}
}

func TestProvisionLinksDeclaredDependencyDirectories(t *testing.T) {
	project, worktreePath, runner := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{
		Project:      "demo",
		Dependencies: Dependencies{Strategy: StrategyLink, Paths: []string{"node_modules"}},
	})
	if err := os.MkdirAll(filepath.Join(project, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner.results = append(ignoredScript(1), scriptedResult{})

	result, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !slices.Contains(result.Linked, "node_modules") {
		t.Errorf("Linked = %v, want node_modules named", result.Linked)
	}
	last := runner.calls[len(runner.calls)-1]
	if last.Name != "cmd" || !slices.Equal(last.Args, []string{"/c", "mklink", "/J", filepath.Join(worktreePath, "node_modules"), filepath.Join(project, "node_modules")}) {
		t.Errorf("link call = %#v, want a junction from the primary checkout", last)
	}
}

func TestProvisionRefusesToLinkAMissingDependencyPath(t *testing.T) {
	project, worktreePath, runner := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{
		Project:      "demo",
		Dependencies: Dependencies{Strategy: StrategyLink, Paths: []string{"node_modules"}},
	})
	_, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Provision error = %v, want a missing-path refusal", err)
	}
}

func TestProvisionRefusesAnUnknownStrategy(t *testing.T) {
	project, worktreePath, runner := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{
		Project:      "demo",
		Dependencies: Dependencies{Strategy: "teleport"},
	})
	_, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath)
	if err == nil || !strings.Contains(err.Error(), "unknown dependency strategy") {
		t.Fatalf("Provision error = %v, want an unknown-strategy refusal", err)
	}
}

func TestProvisionSurfacesEnvRedirects(t *testing.T) {
	project, worktreePath, runner := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{
		Project: "demo",
		Env:     map[string]string{"PLAYWRIGHT_BROWSERS_PATH": `C:\cache\ms-playwright`},
	})
	result, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if result.Env["PLAYWRIGHT_BROWSERS_PATH"] != `C:\cache\ms-playwright` {
		t.Errorf("Env = %v, want the manifest redirect", result.Env)
	}
}

func TestProvisionMaterializesTheTokenAuthenticatedMCPSubset(t *testing.T) {
	project, worktreePath, runner := provisionFixture(t)
	config := []byte(`{"mcpServers": {
		"neon": {"url": "https://mcp.neon.tech/mcp", "bearerTokenEnvVar": "NEON_API_KEY"},
		"supabase": {"url": "https://mcp.supabase.com/mcp"}
	}}`)
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	runner.results = ignoredScript(1)

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !result.HasMCP {
		t.Fatal("HasMCP = false, want the filtered config materialized")
	}
	if !slices.Equal(result.MCPDropped, []string{"supabase"}) {
		t.Errorf("MCPDropped = %v, want the OAuth-only server named", result.MCPDropped)
	}
	data, err := os.ReadFile(filepath.Join(worktreePath, ".mcp.json"))
	if err != nil {
		t.Fatalf("read materialized .mcp.json: %v", err)
	}
	var document struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse materialized .mcp.json: %v", err)
	}
	if len(document.Servers) != 1 || document.Servers["neon"] == nil {
		t.Errorf("materialized servers = %v, want only neon", document.Servers)
	}
}

func TestProvisionWritesNoMCPConfigWhenNothingQualifies(t *testing.T) {
	project, worktreePath, runner := provisionFixture(t)
	config := []byte(`{"mcpServers": {"supabase": {"url": "https://mcp.supabase.com/mcp"}}}`)
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if result.HasMCP {
		t.Error("HasMCP = true for an all-OAuth config, want false")
	}
	if !slices.Equal(result.MCPDropped, []string{"supabase"}) {
		t.Errorf("MCPDropped = %v, want supabase named", result.MCPDropped)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, ".mcp.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".mcp.json was materialized with zero qualifying servers: %v", err)
	}
}

func writeManifest(t *testing.T, dataDir, project string, manifest Manifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := ManifestPath(dataDir, project)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
