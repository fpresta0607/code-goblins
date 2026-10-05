package spawn

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/harness"
)

// stubPreflight stands in for the auth package so spawn's contribution -
// where the credentials go and what the goblin sees - is tested on its own.
type stubPreflight struct {
	result   auth.Result
	err      error
	projects []string
}

func (p *stubPreflight) Preflight(_ context.Context, project string) (auth.Result, error) {
	p.projects = append(p.projects, project)
	return p.result, p.err
}

// A goblin's project credentials ride the environment its terminal's host
// starts with: no credential is typed, shown on its screen, or written to a
// file at spawn.
func TestSpawnHandsProjectCredentialsToTheGoblinThroughItsTerminalsEnvironment(t *testing.T) {
	f := newQuickFixture(t)
	preflight := &stubPreflight{result: auth.Result{
		Env:     map[string]string{"FIXTURE_TOKEN": "sk_live_do_not_print"},
		Warning: "auth: 1/1 services green for primary",
	}}
	f.service.Auth = preflight

	result, err := f.service.Spawn(context.Background(), f.request)

	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := named(f.events(t), "env")[0].Env["FIXTURE_TOKEN"]; got == nil || *got != "sk_live_do_not_print" {
		t.Errorf("the goblin started with FIXTURE_TOKEN = %v, want the project credential", got)
	}
	for _, event := range f.events(t) {
		if event.Event != "env" && strings.Contains(event.Text, "sk_live_do_not_print") {
			t.Errorf("the credential reached the goblin's screen: %+v", event)
		}
	}
	if _, err := os.Stat(filepath.Join(result.Meta.TaskTmp, "auth.ps1")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a credential script was written at spawn: %v", err)
	}
	if len(preflight.projects) != 1 || preflight.projects[0] != result.Meta.Project {
		t.Errorf("preflight projects = %v, want the canonical project once", preflight.projects)
	}
}

func TestWriteAuthScriptSwapsAExistingScriptAtomically(t *testing.T) {
	taskTmp := t.TempDir()
	old := filepath.Join(taskTmp, "auth.ps1")
	if err := os.WriteFile(old, []byte("$env:OLD = 'stale'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	path, vars, err := writeAuthScript(taskTmp, map[string]string{"FLY_API_TOKEN": "fresh"})
	if err != nil {
		t.Fatalf("writeAuthScript: %v", err)
	}
	if path != old || vars != 1 {
		t.Fatalf("path = %q vars = %d, want %q and 1", path, vars, old)
	}
	script, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "$env:FLY_API_TOKEN = 'fresh'") || strings.Contains(string(script), "stale") {
		t.Errorf("script =\n%s\nwant the rewrite to fully replace the old content", script)
	}
	entries, err := os.ReadDir(taskTmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "auth.ps1" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("tasktmp = %v, want only auth.ps1 with no temp file left behind", names)
	}
}

func TestWriteAuthScriptFailureLeavesNoArtifacts(t *testing.T) {
	root := t.TempDir()
	// A tasktmp that cannot be created: the swap must fail before any temp
	// file or partial script appears.
	blocker := filepath.Join(root, "tasktmp")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := writeAuthScript(blocker, map[string]string{"FLY_API_TOKEN": "x"}); err == nil {
		t.Fatal("writeAuthScript = nil error, want a failure for an unusable tasktmp")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "tasktmp" {
		t.Errorf("root changed by the failed write: %d entries, want only the blocker", len(entries))
	}
}

func TestSpawnReportsABlockedPreflightInItsOutput(t *testing.T) {
	f := newQuickFixture(t)
	f.service.Auth = &stubPreflight{result: auth.Result{
		Env:     map[string]string{"FIXTURE_TOKEN": "t0ken"},
		Warning: "auth: 1/2 services green for primary; BLOCKING: stripe (expired)",
	}}

	result, err := f.service.Spawn(context.Background(), f.request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// The CFO has to read this at dispatch; the alternative is the goblin
	// discovering it mid-task.
	if !strings.Contains(result.Output, "BLOCKING: stripe (expired)") {
		t.Errorf("output = %q, want the blocking service named", result.Output)
	}
	if !strings.HasPrefix(result.Output, "spawned ") {
		t.Errorf("output = %q, want the spawn line kept first", result.Output)
	}
}

// A project with nothing to inject still starts its goblin with no harness
// billing key: a key inherited from the user environment is the case with
// nothing declared at all, and a harness that finds one bills it instead of
// the subscription.
func TestSpawnStartsAGoblinWithNoBillingKeyWhenAProjectDeclaresNothing(t *testing.T) {
	f := newQuickFixture(t)
	f.service.Auth = &stubPreflight{}
	f.userEnv = append(f.userEnv, "OPENAI_API_KEY=a user-scope billing key")

	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := named(f.events(t), "env")[0].Env["OPENAI_API_KEY"]; got != nil {
		t.Errorf("the goblin started with OPENAI_API_KEY = %q, want no billing key", *got)
	}
}

// Credentials are merged under the launch contract, so a credential can never
// redirect a variable the launch owns, whether it names it exactly or by
// another case, which Windows reads as the same variable; names the launch
// writes only at harness start are reserved all the same.
func TestSpawnKeepsTheLaunchContractOverItsCredentials(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"exact names":     {"GOTMPDIR": `C:\hijacked`, "CFO_STATE_OVERRIDE": `C:\hijacked`, "FIXTURE_TOKEN": "t0ken"},
		"case-aliased":    {"gotmpdir": `C:\hijacked`, "cfo_state_override": `C:\hijacked`, "FIXTURE_TOKEN": "t0ken"},
		"the goblin role": {"Cfo_Role": "cfo", "FIXTURE_TOKEN": "t0ken"},
		"the task's own":  {"cfo_task_id": "another-task", "FIXTURE_TOKEN": "t0ken"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newQuickFixture(t)
			f.service.Auth = &stubPreflight{result: auth.Result{Env: env}}

			result, err := f.service.Spawn(context.Background(), f.request)

			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			started := named(f.events(t), "env")[0].Env
			for variable, want := range map[string]string{
				"GOTMPDIR":           taskScratch(f.stateDir, result.Meta.ID),
				"CFO_STATE_OVERRIDE": f.stateDir,
				"CFO_ROLE":           harness.RoleGoblin,
				"CFO_TASK_ID":        "task-7",
				"FIXTURE_TOKEN":      "t0ken",
			} {
				if got := started[variable]; got == nil || *got != want {
					t.Errorf("the goblin started with %s = %v, want %q", variable, got, want)
				}
			}
		})
	}
}

func TestSpawnFailsLoudlyWhenThePreflightItselfBreaks(t *testing.T) {
	f := newFixture(t)
	f.service.Auth = &stubPreflight{err: errTestPreflight}

	_, err := f.service.Spawn(context.Background(), f.request)
	if err == nil || !strings.Contains(err.Error(), "project auth preflight") {
		t.Fatalf("err = %v, want the preflight failure surfaced", err)
	}
}

var errTestPreflight = &preflightError{}

type preflightError struct{}

func (*preflightError) Error() string { return "credential store is unreachable" }

func TestSpawnRefusesARedBlockingServiceAndPrintsTheFixCommand(t *testing.T) {
	f := newFixture(t)
	f.request.Yolo = false
	f.service.Auth = &stubPreflight{result: auth.Result{
		Warning: "auth: 0/1 services green for primary; BLOCKING: postgres (missing)",
		Refusal: "1 blocking service(s) for primary; fix these or pass --yolo to dispatch anyway\n  postgres (missing): did not resolve: DATABASE_URL\n    cfo auth store --project primary DATABASE_URL",
	}}

	_, err := f.service.Spawn(context.Background(), f.request)
	if err == nil {
		t.Fatal("Spawn = nil, want a red blocking service to stop the dispatch")
	}
	// The warning scrolled past every goblin spawned that night. A refusal
	// the operator cannot ignore is the control; the fix has to be in it.
	if !strings.Contains(err.Error(), "cfo auth store --project primary DATABASE_URL") {
		t.Errorf("err = %v, want the exact fix command printed", err)
	}
}

func TestSpawnDispatchesOverARedServiceUnderYolo(t *testing.T) {
	f := newQuickFixture(t)
	f.request.Yolo = true
	f.service.Auth = &stubPreflight{result: auth.Result{
		Env:     map[string]string{"FIXTURE_TOKEN": "t0ken"},
		Warning: "auth: 0/1 services green for primary; BLOCKING: postgres (missing)",
		Refusal: "1 blocking service(s) for primary; fix these or pass --yolo to dispatch anyway\n  postgres (missing): did not resolve: DATABASE_URL",
	}}

	result, err := f.service.Spawn(context.Background(), f.request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// The override is recorded rather than swallowing what it overrode.
	if !strings.Contains(result.Output, "dispatched with --yolo over") {
		t.Errorf("output = %q, want the override recorded", result.Output)
	}
}

func TestSpawnRunsTheCredentialPreflightExactlyOnce(t *testing.T) {
	f := newQuickFixture(t)
	preflight := &stubPreflight{result: auth.Result{Env: map[string]string{"FIXTURE_TOKEN": "t0ken"}}}
	f.service.Auth = preflight

	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Probing twice doubles the preflight's wall clock and can report two
	// different answers for one dispatch.
	if len(preflight.projects) != 1 {
		t.Errorf("preflight ran %d times, want exactly one probe run per spawn", len(preflight.projects))
	}
}

// Cache locations are paths on this machine, not credentials, and they reach
// the goblin's environment all the same; the launch contract still wins, so a
// cache redirect cannot take a name the adapter already owns.
func TestSpawnCarriesTheSharedCachesInTheGoblinsEnvironment(t *testing.T) {
	f := newQuickFixture(t)
	f.service.Auth = &stubPreflight{result: auth.Result{
		Env:    map[string]string{"FIXTURE_TOKEN": "t0ken"},
		Caches: map[string]string{"UV_CACHE_DIR": `C:\cfo\caches\uv`, "GOTMPDIR": `C:\hijacked`},
	}}

	result, err := f.service.Spawn(context.Background(), f.request)

	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	started := named(f.events(t), "env")[0].Env
	if got := started["UV_CACHE_DIR"]; got == nil || *got != `C:\cfo\caches\uv` {
		t.Errorf("the goblin started with UV_CACHE_DIR = %v, want the shared uv cache", got)
	}
	if got := started["GOTMPDIR"]; got == nil || *got != taskScratch(f.stateDir, result.Meta.ID) {
		t.Errorf("the goblin started with GOTMPDIR = %v, want the task's own", got)
	}
}

// A server that authenticates by bearerTokenEnvVar is handed to the goblin
// only when that variable is set in the environment its terminal starts with:
// a declared project credential, or one of the user's own variables. A
// harness billing key never counts, because no goblin starts with one. A kept
// server's token reaches the file Claude reads as a reference Claude expands,
// never as its value, and a withheld server is named with its variable on the
// spawned line.
func TestSpawnHandsATokenServerToTheGoblinOnlyWhenItsVariableIsSet(t *testing.T) {
	for _, test := range []struct {
		name         string
		variable     string
		value        string
		isDeclared   bool
		isUserScoped bool
		isKept       bool
	}{
		{name: "declared as a project credential", variable: "NEON_API_KEY", value: "declared-token", isDeclared: true, isKept: true},
		{name: "set in the user's environment", variable: "NEON_API_KEY", value: "users-token", isUserScoped: true, isKept: true},
		{name: "a harness billing key", variable: "OPENAI_API_KEY", value: "users-token", isUserScoped: true},
		{name: "set nowhere", variable: "NEON_API_KEY"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.variable, "")
			f := newQuickFixture(t)
			specs := []harness.LaunchSpec{}
			f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, specs: &specs}}}
			preflight := &stubPreflight{}
			if test.isDeclared {
				preflight.result.Env = map[string]string{test.variable: test.value}
			}
			if test.isUserScoped {
				f.userEnv = append(f.userEnv, test.variable+"="+test.value)
			}
			f.service.Auth = preflight
			writeFile(t, filepath.Join(f.project, ".mcp.json"), `{"mcpServers":{"neon":{"url":"https://mcp.neon.tech/mcp","bearerTokenEnvVar":"`+test.variable+`"}}}`)

			result, err := f.service.Spawn(context.Background(), f.request)

			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			withheld := "withheld servers whose token variable is not set for the goblin: neon (" + test.variable + ")"
			if got := strings.Contains(result.Output, withheld); got == test.isKept {
				t.Errorf("output names neon withheld = %v, want %v:\n%s", got, !test.isKept, result.Output)
			}
			handed := specs[len(specs)-1].MCPConfig
			if !test.isKept {
				if handed != "" {
					t.Errorf("MCPConfig = %q, want none once neon is withheld", handed)
				}
				return
			}
			data, err := os.ReadFile(handed)
			if err != nil {
				t.Fatalf("read the handed MCP config: %v", err)
			}
			var document struct {
				Servers map[string]struct {
					Type    string            `json:"type"`
					Headers map[string]string `json:"headers"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatalf("parse %s: %v", handed, err)
			}
			if neon := document.Servers["neon"]; neon.Type != "http" || neon.Headers["Authorization"] != "Bearer ${"+test.variable+"}" {
				t.Errorf("neon = %+v, want type http with the Authorization header Bearer ${%s}", neon, test.variable)
			}
			if strings.Contains(string(data), test.value) {
				t.Errorf("%s holds the token's value, want only the reference", handed)
			}
		})
	}
}
