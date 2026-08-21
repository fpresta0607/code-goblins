package worktree

import (
	"bytes"
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

// provisionFixture builds a project, its worktree, and the task temporary
// directory under one temp root, and returns them with the runner Provision
// should drive.
func provisionFixture(t *testing.T) (project, worktreePath, taskTmp string, runner *scriptedRunner) {
	t.Helper()
	root := t.TempDir()
	project = filepath.Join(root, "demo")
	worktreePath = filepath.Join(project, ".worktrees", "gb-task")
	taskTmp = filepath.Join(root, "tasktmp", "gb-task")
	for _, dir := range []string{project, worktreePath, taskTmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return project, worktreePath, taskTmp, &scriptedRunner{}
}

// untrackedScript answers the `git ls-files --error-unmatch` probe with exit 1,
// the answer for a path the project does not track.
func untrackedScript() []scriptedResult {
	return []scriptedResult{{result: execx.Result{ExitCode: 1}}}
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
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %#v, want none for a project with nothing to share", runner.calls)
	}
	if result.MCPConfig != "" || len(result.Linked) != 0 || result.Installed != "" {
		t.Errorf("result = %+v, want an empty provisioning", result)
	}
}

func TestProvisionHardlinksConfigFiles(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	source := filepath.Join(project, ".env")
	if err := os.WriteFile(source, []byte("DATABASE_URL=postgres://x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(project, ".git")
	runner.results = unignoredScript(gitDir, ".env")

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
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
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("K=V\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(project, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner.results = ignoredScript(1)

	if _, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil); err != nil {
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
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	if err := os.WriteFile(filepath.Join(worktreePath, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(project, ".git")
	runner.results = append(unignoredScript(gitDir, "node_modules"), scriptedResult{})

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
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

// TestProvisionInstallsAgainstTheSharedCaches is the point of the cache
// redirects. The install is both the largest consumer of the shared store and
// the thing that fills it, so an install that inherits the CFO's environment
// leaves the redirects doing nothing for the case they exist for - and worse
// than nothing for pnpm, which records the store it installed from and tears
// node_modules down when the pane names a different one.
//
// The project's own env block still wins, the same precedence the pane uses.
func TestProvisionInstallsAgainstTheSharedCaches(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{
		Project:      "demo",
		Env:          map[string]string{"PLAYWRIGHT_BROWSERS_PATH": "D:\\project\\browsers"},
		Dependencies: Dependencies{Install: []string{"pnpm install --frozen-lockfile"}},
	})
	gitDir := filepath.Join(project, ".git")
	runner.results = append(unignoredScript(gitDir, "node_modules"), scriptedResult{})
	caches := map[string]string{
		"npm_config_store_dir":     "C:\\cfo\\caches\\pnpm",
		"PLAYWRIGHT_BROWSERS_PATH": "C:\\cfo\\caches\\playwright",
	}

	if _, err := (Service{Commands: runner, DataDir: dataDir}).
		Provision(context.Background(), project, worktreePath, taskTmp, caches); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	install := runner.calls[len(runner.calls)-1]
	if install.Name != "pnpm" {
		t.Fatalf("install call = %#v, want the pnpm install", install)
	}
	got := map[string]string{}
	for _, entry := range install.Env {
		if name, value, found := strings.Cut(entry, "="); found {
			got[name] = value
		}
	}
	if got["npm_config_store_dir"] != "C:\\cfo\\caches\\pnpm" {
		t.Errorf("npm_config_store_dir = %q, want the install to fill the shared store", got["npm_config_store_dir"])
	}
	// The project named this one, so the machine-wide redirect fills in
	// behind it rather than over it.
	if got["PLAYWRIGHT_BROWSERS_PATH"] != "D:\\project\\browsers" {
		t.Errorf("PLAYWRIGHT_BROWSERS_PATH = %q, want the project's own redirect to win", got["PLAYWRIGHT_BROWSERS_PATH"])
	}
	// The install still needs the machine it runs on, so the redirects are an
	// overlay rather than a replacement.
	if len(install.Env) <= len(caches) {
		t.Errorf("install env has %d entries, want the CFO environment carried through under the redirects", len(install.Env))
	}
}

func TestProvisionPinsEveryDetectedInstallerToItsLockfile(t *testing.T) {
	// A worktree holds tracked files, so an installer that resolves drift by
	// rewriting its lockfile leaves uncommitted work no goblin authored, and
	// Return then refuses to remove the worktree at all.
	for _, test := range []struct {
		lockfile string
		ignored  string
		want     []string
	}{
		{lockfile: "pnpm-lock.yaml", ignored: "node_modules", want: []string{"pnpm", "install", "--frozen-lockfile"}},
		{lockfile: "package-lock.json", ignored: "node_modules", want: []string{"npm", "ci"}},
		{lockfile: "yarn.lock", ignored: "node_modules", want: []string{"yarn", "install", "--frozen-lockfile"}},
		{lockfile: "uv.lock", ignored: ".venv", want: []string{"uv", "sync", "--locked"}},
	} {
		t.Run(test.lockfile, func(t *testing.T) {
			project, worktreePath, taskTmp, runner := provisionFixture(t)
			writeFileLine(t, filepath.Join(worktreePath, test.lockfile), "lock")
			runner.results = []scriptedResult{{}, {}}

			result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
			if err != nil {
				t.Fatalf("Provision: %v", err)
			}
			if result.Installed != strings.Join(test.want, " ") {
				t.Errorf("Installed = %q, want %q", result.Installed, strings.Join(test.want, " "))
			}
			install := runner.calls[len(runner.calls)-1]
			if install.Dir != worktreePath || install.Name != test.want[0] || !slices.Equal(install.Args, test.want[1:]) {
				t.Errorf("install call = %#v, want %q in the worktree", install, test.want)
			}
		})
	}
}

func TestProvisionManifestOverridesTheInstallCommands(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{
		Project:      "demo",
		Dependencies: Dependencies{Install: []string{"uv venv", "uv pip install -r requirements.txt"}},
	})
	gitDir := filepath.Join(project, ".git")
	runner.results = append(unignoredScript(gitDir, ".venv"), scriptedResult{}, scriptedResult{})

	result, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
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
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{
		Project:      "demo",
		Dependencies: Dependencies{Strategy: StrategyLink, Paths: []string{"node_modules"}},
	})
	if err := os.MkdirAll(filepath.Join(project, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner.results = append(ignoredScript(1), scriptedResult{})

	result, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
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
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{
		Project:      "demo",
		Dependencies: Dependencies{Strategy: StrategyLink, Paths: []string{"node_modules"}},
	})
	_, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Provision error = %v, want a missing-path refusal", err)
	}
}

func TestProvisionRefusesAnUnknownStrategy(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{
		Project:      "demo",
		Dependencies: Dependencies{Strategy: "teleport"},
	})
	_, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown dependency strategy") {
		t.Fatalf("Provision error = %v, want an unknown-strategy refusal", err)
	}
}

func TestProvisionRefusesToShareTheProjectMCPConfig(t *testing.T) {
	// Sharing .mcp.json would defeat the filter outright: shareEntry
	// hardlinks a file, so the goblin would read the operator's own
	// unfiltered config - OAuth connectors and all - and any rewrite of it
	// would edit the primary checkout in place.
	for _, name := range []string{".mcp.json", ".MCP.json"} {
		t.Run(name, func(t *testing.T) {
			project, worktreePath, taskTmp, runner := provisionFixture(t)
			dataDir := t.TempDir()
			writeManifest(t, dataDir, project, Manifest{Project: "demo", Link: []string{".env", name}})
			source := filepath.Join(project, ".mcp.json")
			if err := os.WriteFile(source, []byte(`{"mcpServers":{"oauth":{"url":"https://example.com/mcp"}}}`), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
			if err == nil || !strings.Contains(err.Error(), "token-authenticated subset") {
				t.Fatalf("Provision error = %v, want a refusal naming the MCP filter", err)
			}
			if _, err := os.Lstat(filepath.Join(worktreePath, ".mcp.json")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the project's own .mcp.json reached the worktree: %v", err)
			}
		})
	}
}

func TestProvisionReportsAnOccupiedWorktreeMCPPath(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	config := []byte(`{"mcpServers": {
		"neon": {"url": "https://mcp.neon.tech/mcp", "bearerTokenEnvVar": "NEON_API_KEY"},
		"supabase": {"url": "https://mcp.supabase.com/mcp"}
	}}`)
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	// Untracked, but something already put a .mcp.json at the worktree root.
	// Leaving it alone is right; leaving it unreported is not, because a
	// cwd-reading harness reads that file and not the filtered one.
	occupied := filepath.Join(worktreePath, ".mcp.json")
	if err := os.WriteFile(occupied, config, 0o644); err != nil {
		t.Fatal(err)
	}
	runner.results = untrackedScript()

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !result.MCPWorktreeOccupied {
		t.Error("MCPWorktreeOccupied = false, want the occupied path reported")
	}
	if result.MCPProjectTracked {
		t.Error("MCPProjectTracked = true, want false for an untracked occupant")
	}
	if got := mcpServerNames(t, result.MCPConfig); !slices.Equal(got, []string{"neon"}) {
		t.Errorf("materialized servers = %v, want the filtered config still handed to the harness", got)
	}
	after, err := os.ReadFile(occupied)
	if err != nil || !bytes.Equal(after, config) {
		t.Fatalf("worktree .mcp.json = %s (%v), want the occupant left untouched", after, err)
	}
}

func TestProvisionSurfacesEnvRedirects(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{
		Project: "demo",
		Env:     map[string]string{"PLAYWRIGHT_BROWSERS_PATH": `C:\cache\ms-playwright`},
	})
	result, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if result.Env["PLAYWRIGHT_BROWSERS_PATH"] != `C:\cache\ms-playwright` {
		t.Errorf("Env = %v, want the manifest redirect", result.Env)
	}
}

// mcpServerNames parses one materialized MCP configuration and reports the
// server names it declares. The file is the goblin's MCP contract with its
// harness, so its meaning is what the tests assert.
func mcpServerNames(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var document struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	names := []string{}
	for name := range document.Servers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func TestProvisionMaterializesTheTokenAuthenticatedMCPSubset(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	config := []byte(`{"mcpServers": {
		"neon": {"url": "https://mcp.neon.tech/mcp", "bearerTokenEnvVar": "NEON_API_KEY"},
		"supabase": {"url": "https://mcp.supabase.com/mcp"}
	}}`)
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	runner.results = append(untrackedScript(), ignoredScript(1)...)

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	// The reported path is the one the harness is handed, and it lives
	// outside the checkout so it can never be the project's own file.
	if result.MCPConfig != filepath.Join(taskTmp, "mcp.json") {
		t.Fatalf("MCPConfig = %q, want the task temporary directory's mcp.json", result.MCPConfig)
	}
	if result.MCPProjectTracked {
		t.Error("MCPProjectTracked = true for an untracked .mcp.json")
	}
	if !slices.Equal(result.MCPDropped, []string{"supabase"}) {
		t.Errorf("MCPDropped = %v, want the OAuth-only server named", result.MCPDropped)
	}
	if got := mcpServerNames(t, result.MCPConfig); !slices.Equal(got, []string{"neon"}) {
		t.Errorf("materialized servers = %v, want only neon", got)
	}
	// kimi has no config flag and reads the project-scoped file from its
	// working directory, so the same filtered set has to be there too.
	if got := mcpServerNames(t, filepath.Join(worktreePath, ".mcp.json")); !slices.Equal(got, []string{"neon"}) {
		t.Errorf("worktree servers = %v, want only neon", got)
	}
}

func TestProvisionLeavesATrackedMCPConfigUntouched(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	config := []byte(`{"mcpServers": {
		"neon": {"url": "https://mcp.neon.tech/mcp", "bearerTokenEnvVar": "NEON_API_KEY"},
		"supabase": {"url": "https://mcp.supabase.com/mcp"}
	}}`)
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	// A project that commits .mcp.json has it checked out in the worktree
	// too, and `git ls-files --error-unmatch` answers exit 0 for it.
	checkedOut := filepath.Join(worktreePath, ".mcp.json")
	if err := os.WriteFile(checkedOut, config, 0o644); err != nil {
		t.Fatal(err)
	}
	runner.results = []scriptedResult{{}}

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !result.MCPProjectTracked {
		t.Error("MCPProjectTracked = false, want the withheld worktree copy reported")
	}
	if got := mcpServerNames(t, result.MCPConfig); !slices.Equal(got, []string{"neon"}) {
		t.Errorf("materialized servers = %v, want only neon", got)
	}
	// Overwriting the tracked file would leave the worktree permanently
	// modified, which Return refuses to remove, and would carry the stripped
	// config back into the project's own history.
	after, err := os.ReadFile(checkedOut)
	if err != nil || !bytes.Equal(after, config) {
		t.Fatalf("tracked worktree .mcp.json = %s (%v), want the committed bytes untouched", after, err)
	}
}

func TestProvisionRefusesWhenTheTrackednessProbeCannotAnswer(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	config := []byte(`{"mcpServers": {"neon": {"url": "https://mcp.neon.tech/mcp", "bearerTokenEnvVar": "NEON_API_KEY"}}}`)
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	// A tracked .mcp.json is checked out into the worktree, and this is what
	// git looks like when it cannot read its own index: exit 128, not the
	// exit 1 that means "untracked".
	checkedOut := filepath.Join(worktreePath, ".mcp.json")
	if err := os.WriteFile(checkedOut, config, 0o644); err != nil {
		t.Fatal(err)
	}
	runner.results = []scriptedResult{
		{result: execx.Result{ExitCode: 128, Stderr: []byte("fatal: not a git repository")}},
	}

	// Reading an unanswerable probe as "untracked" would overwrite the
	// operator's committed file and leave the worktree permanently dirty,
	// which is the exact outcome this probe exists to prevent.
	_, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err == nil || !strings.Contains(err.Error(), "is tracked") {
		t.Fatalf("Provision error = %v, want the unreadable trackedness probe surfaced", err)
	}
	after, readErr := os.ReadFile(checkedOut)
	if readErr != nil || !bytes.Equal(after, config) {
		t.Fatalf("worktree .mcp.json = %s (%v), want it untouched after an unanswerable probe", after, readErr)
	}
}

func TestProvisionWritesNoMCPConfigWhenNothingQualifies(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	config := []byte(`{"mcpServers": {"supabase": {"url": "https://mcp.supabase.com/mcp"}}}`)
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	runner.results = untrackedScript()

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if result.MCPConfig != "" {
		t.Errorf("MCPConfig = %q for an all-OAuth config, want none", result.MCPConfig)
	}
	if result.MCPProjectTracked {
		t.Error("MCPProjectTracked = true, want false when the project does not track .mcp.json")
	}
	if !slices.Equal(result.MCPDropped, []string{"supabase"}) {
		t.Errorf("MCPDropped = %v, want supabase named", result.MCPDropped)
	}
	for _, path := range []string{filepath.Join(worktreePath, ".mcp.json"), filepath.Join(taskTmp, "mcp.json")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s was materialized with zero qualifying servers: %v", path, err)
		}
	}
}

func TestProvisionDisclosesATrackedProjectConfigWhenNothingQualifies(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	// The mainstream case: the project commits a .mcp.json holding only OAuth
	// connectors. Nothing qualifies, so no filtered config is written at all -
	// and the goblin's cwd-reading harness still finds every withheld server
	// in the tracked file Acquire checked out. That is when the disclosure
	// matters most, so it must not depend on anything having qualified.
	config := []byte(`{"mcpServers": {
		"notion": {"url": "https://mcp.notion.com/mcp"},
		"supabase": {"url": "https://mcp.supabase.com/mcp"}
	}}`)
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	checkedOut := filepath.Join(worktreePath, ".mcp.json")
	if err := os.WriteFile(checkedOut, config, 0o644); err != nil {
		t.Fatal(err)
	}
	runner.results = []scriptedResult{{}}

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !result.MCPProjectTracked {
		t.Error("MCPProjectTracked = false, want the tracked project config disclosed")
	}
	if result.MCPConfig != "" {
		t.Errorf("MCPConfig = %q, want none when no server qualifies", result.MCPConfig)
	}
	if !slices.Equal(result.MCPDropped, []string{"notion", "supabase"}) {
		t.Errorf("MCPDropped = %v, want both OAuth connectors named", result.MCPDropped)
	}
	after, err := os.ReadFile(checkedOut)
	if err != nil || !bytes.Equal(after, config) {
		t.Fatalf("tracked worktree .mcp.json = %s (%v), want the committed bytes untouched", after, err)
	}
}

func TestProvisionReportsAFailedInstallWithoutFailingTheDispatch(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	writeFileLine(t, filepath.Join(worktreePath, "pnpm-lock.yaml"), "lockfileVersion: '9.0'")
	writeFileLine(t, filepath.Join(project, ".env"), "K=V")
	runner.results = []scriptedResult{
		{}, // check-ignore .env: already ignored
		{}, // check-ignore node_modules: already ignored
		{result: execx.Result{ExitCode: 1, Stderr: []byte("ERR_PNPM_OUTDATED_LOCKFILE  Cannot install" + "\n" + "with frozen-lockfile")}},
	}

	// A drifted lockfile must not abort the dispatch: the goblin can run the
	// installer itself, and repairing it may be the task it was sent to do.
	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err != nil {
		t.Fatalf("Provision: %v, want a reported install failure rather than an error", err)
	}
	if result.InstallFailed != "pnpm install --frozen-lockfile" {
		t.Errorf("InstallFailed = %q, want the exact command that failed", result.InstallFailed)
	}
	if !strings.Contains(result.InstallOutput, "ERR_PNPM_OUTDATED_LOCKFILE") {
		t.Errorf("InstallOutput = %q, want the installer's own cause", result.InstallOutput)
	}
	if strings.ContainsAny(result.InstallOutput, "\r\n") {
		t.Errorf("InstallOutput = %q, want one line for the spawn output", result.InstallOutput)
	}
	if result.Installed != "" {
		t.Errorf("Installed = %q, want no successful command claimed", result.Installed)
	}
	if !slices.Contains(result.Linked, ".env") {
		t.Errorf("Linked = %v, want provisioning to have continued past the failed install", result.Linked)
	}
}

func TestProvisionAbandonsTheChainAfterAFailedInstallCommand(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{
		Project:      "demo",
		Dependencies: Dependencies{Install: []string{"uv venv", "uv pip install -r requirements.txt"}},
	})
	runner.results = []scriptedResult{
		{}, // check-ignore .venv
		{result: execx.Result{ExitCode: 1, Stderr: []byte("uv: no interpreter found")}},
	}

	result, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if result.InstallFailed != "uv venv" {
		t.Errorf("InstallFailed = %q, want the first command", result.InstallFailed)
	}
	// The second command builds on the environment the first was meant to
	// create, so running it after the failure would only add noise.
	if got := len(runner.calls); got != 2 {
		t.Errorf("calls = %d (%#v), want the chain abandoned after the failure", got, runner.calls)
	}
}

func TestProvisionSurfacesAnUnstartableInstaller(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	writeFileLine(t, filepath.Join(worktreePath, "uv.lock"), "version = 1")
	runner.results = []scriptedResult{
		{}, // check-ignore .venv
		{err: errors.New("executable file not found in PATH")},
	}

	// A runner that cannot start the process at all is the runner failing,
	// not the project, so it stays an error.
	_, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err == nil || !strings.Contains(err.Error(), "install dependencies") {
		t.Fatalf("Provision error = %v, want the unstartable installer surfaced", err)
	}
}

func writeFileLine(t *testing.T, path, line string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionSkipsADefaultLinkWhoseDestinationExists(t *testing.T) {
	// A project that commits .env has it checked out by git worktree add. The
	// default link set is applied to every project that declares nothing, so
	// an occupied destination is the project's own config already in place,
	// not a misconfiguration: skipped and reported, never a torn-down spawn.
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	writeFileLine(t, filepath.Join(project, ".env"), "K=primary")
	writeFileLine(t, filepath.Join(worktreePath, ".env"), "K=checked-out")

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err != nil {
		t.Fatalf("Provision: %v, want the occupied default entry skipped rather than fatal", err)
	}
	if !slices.Contains(result.LinkSkipped, ".env") {
		t.Errorf("LinkSkipped = %v, want .env reported as skipped", result.LinkSkipped)
	}
	if slices.Contains(result.Linked, ".env") {
		t.Errorf("Linked = %v, want .env not claimed as shared", result.Linked)
	}
	data, err := os.ReadFile(filepath.Join(worktreePath, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "K=checked-out" {
		t.Errorf("worktree .env = %q, want the checked-out file left exactly as it was", data)
	}
	if len(runner.calls) != 0 {
		t.Errorf("calls = %#v, want no ignore-rule writes for an entry that was not linked", runner.calls)
	}
}

func TestProvisionRefusesADeclaredLinkWhoseDestinationExists(t *testing.T) {
	// The same occupied destination under an explicit manifest declaration is
	// a misconfiguration the operator must hear about: they asked for that
	// file to be shared, so silently not sharing it would hide the problem.
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{Project: "demo", Link: []string{".env"}})
	writeFileLine(t, filepath.Join(project, ".env"), "K=primary")
	writeFileLine(t, filepath.Join(worktreePath, ".env"), "K=checked-out")

	result, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err == nil || !strings.Contains(err.Error(), "already exists in the worktree") {
		t.Fatalf("Provision error = %v, want a refusal naming the occupied declared entry", err)
	}
	if len(result.LinkSkipped) != 0 {
		t.Errorf("LinkSkipped = %v, want a declared entry refused rather than skipped", result.LinkSkipped)
	}
}

func TestResolveRefusesAnInvalidEnvName(t *testing.T) {
	// Switch resolves the manifest before stopping the old harness so a
	// malformed one cannot strand the goblin; that only holds if Resolve
	// refuses every name the pane-shell renderer would refuse at launch.
	for _, name := range []string{"", "FOO BAR", "FOO=BAR", "1ABC", "FOO-BAR"} {
		t.Run(name, func(t *testing.T) {
			project, _, _, _ := provisionFixture(t)
			dataDir := t.TempDir()
			writeManifest(t, dataDir, project, Manifest{Project: "demo", Env: map[string]string{name: "value"}})

			_, err := Resolve(dataDir, project)
			if err == nil || !strings.Contains(err.Error(), "not a valid environment name") {
				t.Fatalf("Resolve error = %v, want the env name refused", err)
			}
		})
	}
}

func TestResolveAcceptsAValidEnvName(t *testing.T) {
	project, _, _, _ := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{Project: "demo", Env: map[string]string{"PLAYWRIGHT_BROWSERS_PATH": "value", "_x1": "y"}})

	manifest, err := Resolve(dataDir, project)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if manifest.Env["PLAYWRIGHT_BROWSERS_PATH"] != "value" {
		t.Errorf("Env = %v, want the declared redirect kept", manifest.Env)
	}
}

func TestProvisionRefusesWithoutATaskTemporaryDirectory(t *testing.T) {
	project, worktreePath, _, runner := provisionFixture(t)
	_, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, "", nil)
	if err == nil || !strings.Contains(err.Error(), "task temporary directory") {
		t.Fatalf("Provision error = %v, want a missing task temporary directory refusal", err)
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
