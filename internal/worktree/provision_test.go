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
	if result.MCPConfig != "" || len(result.Linked) != 0 || len(result.Install) != 0 {
		t.Errorf("result = %+v, want an empty provisioning", result)
	}
}

// A worktree's config file used to be a hard link to the checkout's own, one
// file with two names, so a goblin that edited .env in its worktree edited the
// Overlord's real file in place. It is the worktree's own read-only copy now:
// a write to it is refused, and one that is forced changes only the copy.
func TestProvisionGivesTheWorktreeItsOwnReadOnlyCopyOfAConfigFile(t *testing.T) {
	// Arrange
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	source := filepath.Join(project, ".env")
	const overlords = "STANDIN_SETTING=the Overlord's own line\n"
	if err := os.WriteFile(source, []byte(overlords), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(project, ".git")
	runner.results = unignoredScript(gitDir, ".env")

	// Act
	result, err := (Service{Commands: runner, DataDir: sharing(t, project, ".env")}).Provision(context.Background(), project, worktreePath, taskTmp, nil)

	// Assert
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
	if os.SameFile(sourceInfo, destinationInfo) {
		t.Error("worktree .env is the checkout's own file under a second name, want a copy")
	}
	if copied, err := os.ReadFile(destination); err != nil || string(copied) != overlords {
		t.Errorf("worktree .env = %q, %v, want what the checkout's holds", copied, err)
	}
	if err := os.WriteFile(destination, []byte("STANDIN_SETTING=a goblin's edit\n"), 0o644); err == nil {
		t.Error("a write to the worktree's .env went through, want it refused: the copy is read-only")
	}
	// The premise of the next check: the forced write below really lands.
	if err := os.Chmod(destination, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("STANDIN_SETTING=a goblin's edit\n"), 0o644); err != nil {
		t.Fatalf("the forced write did not land, so it proves nothing: %v", err)
	}
	if kept, err := os.ReadFile(source); err != nil || string(kept) != overlords {
		t.Errorf("the checkout's .env = %q, %v, want it untouched by an edit in the worktree", kept, err)
	}
	if after, err := os.Stat(source); err != nil || after.Mode().Perm()&0o200 == 0 {
		t.Errorf("the checkout's .env is %v, %v, want it left writable for the Overlord", after.Mode(), err)
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

	if _, err := (Service{Commands: runner, DataDir: sharing(t, project, ".env")}).Provision(context.Background(), project, worktreePath, taskTmp, nil); err != nil {
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

// TestProvisionNamesTheInstallerAndLeavesItToTheGoblin is the spawn
// concurrency contract: an install writes tens of thousands of files, which
// under on-access scanning held one spawn, and every start behind its lock,
// for over 30 minutes. Provisioning only names the command and registers what
// it will create as ignored; the goblin runs it in its own terminal.
func TestProvisionNamesTheInstallerAndLeavesItToTheGoblin(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	writeFileLine(t, filepath.Join(worktreePath, "package-lock.json"), "{}")
	gitDir := filepath.Join(project, ".git")
	runner.results = unignoredScript(gitDir, "node_modules")

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !slices.Equal(result.Install, []string{"npm ci"}) {
		t.Errorf("Install = %q, want the npm lockfile command for the goblin", result.Install)
	}
	for _, call := range runner.calls {
		if call.Name != "git" {
			t.Errorf("Provision ran %q %q, want no installer run by the spawn", call.Name, call.Args)
		}
	}
	exclude, err := os.ReadFile(filepath.Join(gitDir, "info", "exclude"))
	if err != nil || !strings.Contains(string(exclude), "node_modules") {
		t.Errorf("info/exclude = %q, %v; want node_modules excluded before the goblin installs", exclude, err)
	}
}

func TestProvisionPinsEveryDetectedInstallerToItsLockfile(t *testing.T) {
	// A worktree holds tracked files, so an installer that resolves drift by
	// rewriting its lockfile leaves uncommitted work no goblin authored, and
	// Return then refuses to remove the worktree at all.
	for _, test := range []struct {
		lockfile string
		want     string
	}{
		{lockfile: "pnpm-lock.yaml", want: "pnpm install --frozen-lockfile"},
		{lockfile: "package-lock.json", want: "npm ci"},
		{lockfile: "yarn.lock", want: "yarn install --frozen-lockfile"},
		{lockfile: "uv.lock", want: "uv sync --locked"},
	} {
		t.Run(test.lockfile, func(t *testing.T) {
			project, worktreePath, taskTmp, runner := provisionFixture(t)
			writeFileLine(t, filepath.Join(worktreePath, test.lockfile), "lock")
			runner.results = []scriptedResult{{}}

			result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
			if err != nil {
				t.Fatalf("Provision: %v", err)
			}
			if !slices.Equal(result.Install, []string{test.want}) {
				t.Errorf("Install = %q, want %q", result.Install, test.want)
			}
		})
	}
}

func TestProvisionManifestOverridesTheInstallCommands(t *testing.T) {
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{
		Project:      "demo",
		Dependencies: Dependencies{Install: []string{"uv venv", " ", "uv pip install -r requirements.txt"}},
	})
	gitDir := filepath.Join(project, ".git")
	runner.results = unignoredScript(gitDir, ".venv")

	result, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if want := []string{"uv venv", "uv pip install -r requirements.txt"}; !slices.Equal(result.Install, want) {
		t.Errorf("Install = %q, want the manifest override %q in order, blank lines dropped", result.Install, want)
	}
	if len(runner.results) != 0 {
		t.Errorf("unused scripted answers %v, want .venv's ignore check asked", runner.results)
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

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, everyVariableSet)
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

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, everyVariableSet)
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

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, everyVariableSet)
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
	_, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, everyVariableSet)
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

func writeFileLine(t *testing.T, path, line string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionLeavesAnEnvFileTheRepositoryTracksAsCheckedOut(t *testing.T) {
	// A project that commits .env has it checked out by git worktree add.
	// Nothing is shared that no manifest names, so the worktree's file is the
	// repository's own, left as it is, and it is not named as one the
	// worktree was not given.
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	writeFileLine(t, filepath.Join(project, ".env"), "K=primary")
	writeFileLine(t, filepath.Join(worktreePath, ".env"), "K=checked-out")

	result, err := (Service{Commands: runner, DataDir: t.TempDir()}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(result.Linked) != 0 || len(result.EnvHeld) != 0 {
		t.Errorf("Linked = %v, EnvHeld = %v, want nothing shared and nothing named as withheld", result.Linked, result.EnvHeld)
	}
	data, err := os.ReadFile(filepath.Join(worktreePath, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "K=checked-out" {
		t.Errorf("worktree .env = %q, want the checked-out file left exactly as it was", data)
	}
	if len(runner.calls) != 0 {
		t.Errorf("calls = %#v, want no ignore-rule writes where nothing was shared", runner.calls)
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

	_, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath, taskTmp, nil)
	if err == nil || !strings.Contains(err.Error(), "already exists in the worktree") {
		t.Fatalf("Provision error = %v, want a refusal naming the occupied declared entry", err)
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

// sharing writes a worktree manifest for project that names names in link, in
// a data folder of its own, and returns that folder: a worktree is given a
// file of the checkout only when a manifest names it.
func sharing(t *testing.T, project string, names ...string) string {
	t.Helper()
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{Project: "demo", Link: names})
	return dataDir
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

// A worktree a build before this one provisioned shares its env files with
// the checkout as hard links. A relaunch ends each one: a file the project's
// manifest names becomes the worktree's own read-only copy, and one no
// manifest names is removed from the worktree, since a worktree is given only
// what a manifest names. The checkout's files are untouched, and so are the
// goblin's own file and a declared dependency link.
func TestOwnConfigEndsEveryHardLinkToTheCheckoutsFiles(t *testing.T) {
	// Arrange
	project, worktreePath, _, _ := provisionFixture(t)
	dataDir := t.TempDir()
	writeManifest(t, dataDir, project, Manifest{Project: "demo", Link: []string{".env"}, Dependencies: Dependencies{Strategy: StrategyLink, Paths: []string{"shared.lock"}}})
	const overlords = "STANDIN_SETTING=the Overlord's own line\n"
	for _, name := range []string{".env", ".env.production", ".env.local", "shared.lock"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(overlords), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{".env", ".env.production", "shared.lock"} {
		if err := os.Link(filepath.Join(project, name), filepath.Join(worktreePath, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(worktreePath, ".env.local"), []byte("STANDIN_SETTING=the goblin's own file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := Service{DataDir: dataDir}
	isSameFile := func(name string) bool {
		sourceInfo, sourceErr := os.Stat(filepath.Join(project, name))
		destinationInfo, destinationErr := os.Stat(filepath.Join(worktreePath, name))
		return sourceErr == nil && destinationErr == nil && os.SameFile(sourceInfo, destinationInfo)
	}

	// Act
	copied, removed, err := service.OwnConfig(project, worktreePath)
	copiedAgain, removedAgain, againErr := service.OwnConfig(project, worktreePath)

	// Assert
	if err != nil || !slices.Equal(copied, []string{".env"}) || !slices.Equal(removed, []string{".env.production"}) {
		t.Fatalf("OwnConfig = copied %v, removed %v, %v, want the named file copied and the unnamed link removed", copied, removed, err)
	}
	if againErr != nil || len(copiedAgain)+len(removedAgain) != 0 {
		t.Errorf("a second OwnConfig = %v, %v, %v, want nothing left to end", copiedAgain, removedAgain, againErr)
	}
	if isSameFile(".env") {
		t.Error("the worktree's .env is still the checkout's file")
	}
	if err := os.WriteFile(filepath.Join(worktreePath, ".env"), []byte("STANDIN_SETTING=a goblin's edit\n"), 0o644); err == nil {
		t.Error("a write to the worktree's .env went through, want it refused: the copy is read-only")
	}
	if _, err := os.Stat(filepath.Join(worktreePath, ".env.production")); !os.IsNotExist(err) {
		t.Errorf("the worktree still holds .env.production, which no manifest names (%v)", err)
	}
	for _, name := range []string{".env", ".env.production"} {
		info, err := os.Stat(filepath.Join(project, name))
		kept, readErr := os.ReadFile(filepath.Join(project, name))
		if err != nil || readErr != nil || string(kept) != overlords || info.Mode().Perm()&0o200 == 0 {
			t.Errorf("the checkout's %s = %q, %v, %v, want it there, untouched and writable", name, kept, err, readErr)
		}
	}
	if own, err := os.ReadFile(filepath.Join(worktreePath, ".env.local")); err != nil || string(own) != "STANDIN_SETTING=the goblin's own file\n" {
		t.Errorf("the worktree's own .env.local = %q, %v, want a file that was no link left as it was", own, err)
	}
	if !isSameFile("shared.lock") {
		t.Error("a dependency file the manifest links on purpose was ended too")
	}
}

// A read-only copy never strands a worktree: returning it removes the copy
// with the rest, and leaves the checkout's file where it was.
func TestReturnRemovesAWorktreeThatHoldsAReadOnlyCopy(t *testing.T) {
	// Arrange
	project := clonedProject(t)
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("STANDIN_SETTING=the Overlord's own line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "worktrees", filepath.Base(project), "task-1")
	git := RunnerGit{Commands: execx.OSRunner{}}
	if _, err := git.Acquire(context.Background(), project, path, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := (Service{Commands: execx.OSRunner{}, DataDir: sharing(t, project, ".env")}).Provision(context.Background(), project, path, t.TempDir(), nil); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if info, err := os.Stat(filepath.Join(path, ".env")); err != nil || info.Mode().Perm()&0o200 != 0 {
		t.Fatalf("the premise is a read-only copy in the worktree: %v, %v", info, err)
	}

	// Act
	err := git.Return(context.Background(), project, path)

	// Assert
	if err != nil {
		t.Fatalf("Return: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the worktree is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".env")); err != nil {
		t.Errorf("the checkout's .env went with the worktree: %v", err)
	}
}

// A run that was interrupted between writing a copy and putting it in place
// leaves a read-only staged file. The next run replaces it and leaves none
// behind, since an untracked file would make the worktree read dirty.
func TestProvisionReplacesAStagedCopyAnInterruptedRunLeft(t *testing.T) {
	// Arrange
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("STANDIN_SETTING=current\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(worktreePath, ".env.cfo-copy")
	if err := os.WriteFile(staged, []byte("STANDIN_SETTING=half written\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	runner.results = unignoredScript(filepath.Join(project, ".git"), ".env")

	// Act
	_, err := (Service{Commands: runner, DataDir: sharing(t, project, ".env")}).Provision(context.Background(), project, worktreePath, taskTmp, nil)

	// Assert
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if copied, err := os.ReadFile(filepath.Join(worktreePath, ".env")); err != nil || string(copied) != "STANDIN_SETTING=current\n" {
		t.Errorf("worktree .env = %q, %v, want the checkout's current content", copied, err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Errorf("the staged copy is still in the worktree: %v", err)
	}
}

// A checkout's env file used to reach every new worktree by silence: with no
// worktree manifest, or with one that names no link, a spawn shared .env,
// .env.local and .env.docker.local. A worktree is given an env file only when
// the project's manifest names it.
func TestProvisionSharesNoEnvFileThatNoManifestNames(t *testing.T) {
	cases := []struct {
		name     string
		manifest *Manifest
	}{
		{"no worktree manifest", nil},
		{"a manifest with no link key", &Manifest{Project: "demo", Dependencies: Dependencies{Strategy: StrategyNone}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			project, worktreePath, taskTmp, runner := provisionFixture(t)
			dataDir := t.TempDir()
			if tc.manifest != nil {
				writeManifest(t, dataDir, project, *tc.manifest)
			}
			held := []string{".env", ".env.local", ".env.docker.local"}
			for _, name := range held {
				writeFileLine(t, filepath.Join(project, name), "STANDIN_SETTING=the checkout's own line")
			}
			runner.results = unignoredScript(filepath.Join(project, ".git"), held...)

			// Act
			result, err := (Service{Commands: runner, DataDir: dataDir}).Provision(context.Background(), project, worktreePath, taskTmp, nil)

			// Assert
			if err != nil {
				t.Fatalf("Provision: %v", err)
			}
			for _, name := range held {
				if _, err := os.Stat(filepath.Join(worktreePath, name)); !os.IsNotExist(err) {
					t.Errorf("the worktree was given %s, which no manifest names (%v)", name, err)
				}
			}
			if len(result.Linked) != 0 {
				t.Errorf("Linked = %v, want nothing shared", result.Linked)
			}
			if want := []string{".env", ".env.docker.local", ".env.local"}; !slices.Equal(result.EnvHeld, want) {
				t.Errorf("EnvHeld = %v, want %v named, so the spawn can say what it did not share", result.EnvHeld, want)
			}
			if len(runner.calls) != 0 {
				t.Errorf("calls = %#v, want no ignore rule written where nothing was shared", runner.calls)
			}
		})
	}
}

// What a spawn names as not given is exactly that: an env file the manifest
// names was shared, and a file that is no env file is none of its business.
func TestProvisionNamesOnlyTheEnvFilesTheWorktreeWasNotGiven(t *testing.T) {
	// Arrange
	project, worktreePath, taskTmp, runner := provisionFixture(t)
	for _, name := range []string{".env", ".env.local", ".envrc", "notes.env", "settings.toml"} {
		writeFileLine(t, filepath.Join(project, name), "STANDIN_SETTING=the checkout's own line")
	}
	if err := os.Mkdir(filepath.Join(project, ".env.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner.results = unignoredScript(filepath.Join(project, ".git"), ".env")

	// Act
	result, err := (Service{Commands: runner, DataDir: sharing(t, project, ".env")}).Provision(context.Background(), project, worktreePath, taskTmp, nil)

	// Assert
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !slices.Equal(result.Linked, []string{".env"}) || !slices.Equal(result.EnvHeld, []string{".env.local"}) {
		t.Errorf("Linked = %v, EnvHeld = %v, want .env shared and only .env.local named as not given", result.Linked, result.EnvHeld)
	}
}
