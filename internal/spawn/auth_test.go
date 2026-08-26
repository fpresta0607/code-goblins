package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

// stubPreflight stands in for the auth package so spawn's contribution -
// where the credentials go and what the pane sees - is tested on its own.
type stubPreflight struct {
	result   auth.Result
	err      error
	projects []string
}

func (p *stubPreflight) Preflight(_ context.Context, project string) (auth.Result, error) {
	p.projects = append(p.projects, project)
	return p.result, p.err
}

func TestSpawnInjectsProjectCredentialsThroughAFileTheShellSources(t *testing.T) {
	fixture := newFixture(t)
	preflight := &stubPreflight{result: auth.Result{
		Env:     map[string]string{"STRIPE_SECRET_KEY": "sk_live_do_not_print", "DATABASE_URL": "postgres://db"},
		Warning: "auth: 2/2 services green for primary",
	}}
	fixture.service.Auth = preflight

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// The pane shell only ever sees a path. A credential typed inline would
	// sit in the pane's scrollback and in every `cfo peek`.
	if strings.Contains(fixture.runner.literals[0], "sk_live_do_not_print") {
		t.Fatalf("the typed launch line disclosed a credential: %q", fixture.runner.literals[0])
	}
	secrets := filepath.Join(result.Meta.TaskTmp, "auth.ps1")
	if !strings.Contains(fixture.runner.literals[0], ". '"+secrets+"'") {
		t.Fatalf("literal = %q, want it to dot-source %q", fixture.runner.literals[0], secrets)
	}

	script, err := os.ReadFile(secrets)
	if err != nil {
		t.Fatalf("read secrets script: %v", err)
	}
	for _, want := range []string{"$env:DATABASE_URL = 'postgres://db'", "$env:STRIPE_SECRET_KEY = 'sk_live_do_not_print'"} {
		if !strings.Contains(string(script), want) {
			t.Errorf("secrets script lacks %q:\n%s", want, script)
		}
	}
	if len(preflight.projects) != 1 || preflight.projects[0] != result.Meta.Project {
		t.Errorf("preflight projects = %v, want the canonical project once", preflight.projects)
	}
}

func TestSpawnReportsABlockedPreflightInItsOutput(t *testing.T) {
	fixture := newFixture(t)
	fixture.service.Auth = &stubPreflight{result: auth.Result{
		Env:     map[string]string{"DATABASE_URL": "postgres://db"},
		Warning: "auth: 1/2 services green for primary; BLOCKING: stripe (expired)",
	}}

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
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

func TestSpawnProceedsWhenAProjectDeclaresNothing(t *testing.T) {
	fixture := newFixture(t)
	fixture.service.Auth = &stubPreflight{}

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// A project with nothing to inject still gets the secrets script, because
	// that script is also what strips every harness billing key from the pane
	// before the harness starts. The strip must run whether or not the project
	// declared credentials: a key inherited from the user environment is the
	// case with nothing declared at all.
	script, err := os.ReadFile(filepath.Join(result.Meta.TaskTmp, "auth.ps1"))
	if err != nil {
		t.Fatalf("a project with no credentials must still get the billing-key strip script: %v", err)
	}
	if !strings.Contains(string(script), "Remove-Item -Path Env:ANTHROPIC_API_KEY") {
		t.Errorf("secrets script does not strip ANTHROPIC_API_KEY: %q", script)
	}
	if strings.Contains(string(script), "$env:") {
		t.Errorf("secrets script assigned a value with nothing to inject: %q", script)
	}
	if !strings.Contains(fixture.runner.literals[0], "auth.ps1") {
		t.Errorf("literal = %q, want the strip script dot-sourced even with nothing to inject", fixture.runner.literals[0])
	}
}

func TestSpawnKeepsTheHarnessEnvironmentAuthoritative(t *testing.T) {
	fixture := newFixture(t)
	// A manifest must not be able to redirect the harness's own launch
	// contract by declaring a variable the adapter already owns.
	fixture.service.Auth = &stubPreflight{result: auth.Result{Env: map[string]string{"GOTMPDIR": "C:\\hijacked", "SAFE_KEY": "value"}}}

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	script, err := os.ReadFile(filepath.Join(result.Meta.TaskTmp, "auth.ps1"))
	if err != nil {
		t.Fatalf("read secrets script: %v", err)
	}
	if strings.Contains(string(script), "hijacked") {
		t.Errorf("secrets script overrode the harness environment:\n%s", script)
	}
	if !strings.Contains(string(script), "$env:SAFE_KEY = 'value'") {
		t.Errorf("secrets script dropped an unrelated credential:\n%s", script)
	}
	if !strings.Contains(fixture.runner.literals[0], "$env:GOTMPDIR = '"+filepath.Join(result.Meta.TaskTmp, "gotmp")+"'") {
		t.Errorf("literal = %q, want the harness GOTMPDIR intact", fixture.runner.literals[0])
	}
}

func TestSpawnDropsCaseAliasedReservedCredentials(t *testing.T) {
	fixture := newFixture(t)
	// Credentials reach the pane through the same PowerShell the launch
	// contract does, so a case-aliased reserved name would redirect it just
	// as an exact one would; both are dropped, and names the launch writes
	// only at harness start are reserved all the same.
	fixture.service.Auth = &stubPreflight{result: auth.Result{Env: map[string]string{
		"gotmpdir":           `C:\hijacked`,
		"cfo_state_override": `C:\hijacked`,
		"SAFE_KEY":           "value",
	}}}

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	script, err := os.ReadFile(filepath.Join(result.Meta.TaskTmp, "auth.ps1"))
	if err != nil {
		t.Fatalf("read secrets script: %v", err)
	}
	if strings.Contains(string(script), "hijacked") {
		t.Errorf("secrets script carries a case-aliased reserved name:\n%s", script)
	}
	if !strings.Contains(string(script), "$env:SAFE_KEY = 'value'") {
		t.Errorf("secrets script dropped an unrelated credential:\n%s", script)
	}
	if !strings.Contains(fixture.runner.literals[0], "$env:CFO_STATE_OVERRIDE = '"+fixture.stateDir+"'") {
		t.Errorf("literal = %q, want the harness CFO_STATE_OVERRIDE intact", fixture.runner.literals[0])
	}
}

func TestSpawnFailsLoudlyWhenThePreflightItselfBreaks(t *testing.T) {
	fixture := newFixture(t)
	fixture.service.Auth = &stubPreflight{err: errTestPreflight}

	_, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err == nil || !strings.Contains(err.Error(), "project auth preflight") {
		t.Fatalf("err = %v, want the preflight failure surfaced", err)
	}
}

var errTestPreflight = &preflightError{}

type preflightError struct{}

func (*preflightError) Error() string { return "credential store is unreachable" }

func TestSpawnRefusesARedBlockingServiceAndPrintsTheFixCommand(t *testing.T) {
	fixture := newFixture(t)
	fixture.request.Yolo = false
	fixture.service.Auth = &stubPreflight{result: auth.Result{
		Warning: "auth: 0/1 services green for primary; BLOCKING: postgres (missing)",
		Refusal: "1 blocking service(s) for primary; fix these or pass --yolo to dispatch anyway\n  postgres (missing): did not resolve: DATABASE_URL\n    cfo auth store --project primary DATABASE_URL",
	}}

	_, err := fixture.service.Spawn(context.Background(), fixture.request)
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
	fixture := newFixture(t)
	fixture.request.Yolo = true
	fixture.service.Auth = &stubPreflight{result: auth.Result{
		Env:     map[string]string{"SAFE_KEY": "value"},
		Warning: "auth: 0/1 services green for primary; BLOCKING: postgres (missing)",
		Refusal: "1 blocking service(s) for primary; fix these or pass --yolo to dispatch anyway\n  postgres (missing): did not resolve: DATABASE_URL",
	}}

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// The override is recorded rather than swallowing what it overrode.
	if !strings.Contains(result.Output, "dispatched with --yolo over") {
		t.Errorf("output = %q, want the override recorded", result.Output)
	}
}

func TestSpawnRunsTheCredentialPreflightExactlyOnce(t *testing.T) {
	fixture := newFixture(t)
	preflight := &stubPreflight{result: auth.Result{Env: map[string]string{"SAFE_KEY": "value"}}}
	fixture.service.Auth = preflight

	if _, err := fixture.service.Spawn(context.Background(), fixture.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Probing twice doubles the preflight's wall clock and can report two
	// different answers for one dispatch.
	if len(preflight.projects) != 1 {
		t.Errorf("preflight ran %d times, want exactly one probe run per spawn", len(preflight.projects))
	}
}

func TestSpawnCarriesTheSharedCachesInThePaneEnvironmentNotTheSecretsFile(t *testing.T) {
	fixture := newFixture(t)
	// Cache locations are paths on this machine, not credentials, so they
	// ride the launch environment the pane shell writes. Routing them through
	// the restricted secrets file would make an audit of what a goblin holds
	// unreadable, and redacting a directory helps nobody.
	fixture.service.Auth = &stubPreflight{result: auth.Result{
		Env:    map[string]string{"SAFE_KEY": "value"},
		Caches: map[string]string{"UV_CACHE_DIR": `C:\cfo\caches\uv`, "GOTMPDIR": `C:\hijacked`},
	}}

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !strings.Contains(fixture.runner.literals[0], `$env:UV_CACHE_DIR = 'C:\cfo\caches\uv'`) {
		t.Errorf("literal = %q, want the shared uv cache redirected in the pane", fixture.runner.literals[0])
	}
	// The launch contract still wins: a cache redirect cannot take a name the
	// adapter already owns.
	if strings.Contains(fixture.runner.literals[0], "hijacked") {
		t.Errorf("literal = %q, want the harness GOTMPDIR intact", fixture.runner.literals[0])
	}
	script, err := os.ReadFile(filepath.Join(result.Meta.TaskTmp, "auth.ps1"))
	if err != nil {
		t.Fatalf("read secrets script: %v", err)
	}
	if strings.Contains(string(script), "UV_CACHE_DIR") {
		t.Errorf("a cache path was written into the restricted secrets file:\n%s", script)
	}
}
