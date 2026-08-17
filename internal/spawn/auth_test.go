package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubPreflight stands in for the auth package so spawn's contribution -
// where the credentials go and what the pane sees - is tested on its own.
type stubPreflight struct {
	env      map[string]string
	warning  string
	err      error
	projects []string
}

func (p *stubPreflight) Preflight(_ context.Context, project string) (map[string]string, string, error) {
	p.projects = append(p.projects, project)
	return p.env, p.warning, p.err
}

func TestSpawnInjectsProjectCredentialsThroughAFileTheShellSources(t *testing.T) {
	fixture := newFixture(t)
	preflight := &stubPreflight{
		env:     map[string]string{"STRIPE_SECRET_KEY": "sk_live_do_not_print", "DATABASE_URL": "postgres://db"},
		warning: "auth: 2/2 services green for primary",
	}
	fixture.service.Auth = preflight

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// The pane shell only ever sees a path. A credential typed inline would
	// sit in the pane's scrollback and in every `cfo peek`.
	if strings.Contains(fixture.runner.literal, "sk_live_do_not_print") {
		t.Fatalf("the typed launch line disclosed a credential: %q", fixture.runner.literal)
	}
	secrets := filepath.Join(result.Meta.TaskTmp, "auth.ps1")
	if !strings.Contains(fixture.runner.literal, ". '"+secrets+"'") {
		t.Fatalf("literal = %q, want it to dot-source %q", fixture.runner.literal, secrets)
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
	fixture.service.Auth = &stubPreflight{
		env:     map[string]string{"DATABASE_URL": "postgres://db"},
		warning: "auth: 1/2 services green for primary; BLOCKING: stripe (expired)",
	}

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
	if _, statErr := os.Stat(filepath.Join(result.Meta.TaskTmp, "auth.ps1")); !os.IsNotExist(statErr) {
		t.Errorf("a project with no credentials still got a secrets script: %v", statErr)
	}
	if strings.Contains(fixture.runner.literal, "auth.ps1") {
		t.Errorf("literal = %q, want no dot-source with nothing to inject", fixture.runner.literal)
	}
}

func TestSpawnKeepsTheHarnessEnvironmentAuthoritative(t *testing.T) {
	fixture := newFixture(t)
	// A manifest must not be able to redirect the harness's own launch
	// contract by declaring a variable the adapter already owns.
	fixture.service.Auth = &stubPreflight{env: map[string]string{"GOTMPDIR": "C:\\hijacked", "SAFE_KEY": "value"}}

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
	if !strings.Contains(fixture.runner.literal, "$env:GOTMPDIR = '"+filepath.Join(result.Meta.TaskTmp, "gotmp")+"'") {
		t.Errorf("literal = %q, want the harness GOTMPDIR intact", fixture.runner.literal)
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
