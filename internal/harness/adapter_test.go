package harness

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type fakeRunner struct {
	requests []execx.Request
	run      func(execx.Request) (execx.Result, error)
}

func (r *fakeRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	if r.run == nil {
		return execx.Result{}, nil
	}
	return r.run(request)
}

func TestDefaultRegistryAcceptsOnlyPlan3Harnesses(t *testing.T) {
	registry := DefaultRegistry()

	for _, kind := range []Kind{Claude, Codex, Pi} {
		adapter, err := registry.Get(kind)
		if err != nil {
			t.Fatalf("Get(%q): %v", kind, err)
		}
		if got := adapter.Kind(); got != kind {
			t.Errorf("Get(%q).Kind() = %q", kind, got)
		}
	}

	for _, kind := range []Kind{"grok", "opencode", "raw command", "unknown"} {
		if _, err := registry.Get(kind); err == nil {
			t.Errorf("Get(%q) returned nil error", kind)
		}
	}
}

func TestPowerShellLineEscapesLiteralValues(t *testing.T) {
	launch := Launch{
		Executable: `C:\Program Files\O'Brien\claude.exe`,
		Args: []string{
			"--model",
			"model with spaces and 100%",
			`C:\work\O'Brien\input`,
		},
		Env: map[string]string{
			"GOTMPDIR": `C:\task tmp\O'Brien\gotmp`,
			"PROMPT":   "100% O'Brien",
		},
		PromptFile: `C:\briefs\O'Brien\100% ready.md`,
	}

	got, err := launch.PowerShellLine()
	if err != nil {
		t.Fatalf("PowerShellLine: %v", err)
	}
	want := "$env:GOTMPDIR = 'C:\\task tmp\\O''Brien\\gotmp'; $env:PROMPT = '100% O''Brien'; & 'C:\\Program Files\\O''Brien\\claude.exe' '--model' 'model with spaces and 100%' 'C:\\work\\O''Brien\\input' (Get-Content -Raw -LiteralPath 'C:\\briefs\\O''Brien\\100% ready.md')"
	if got != want {
		t.Errorf("PowerShellLine() = %q\nwant %q", got, want)
	}
}

func TestPowerShellLineRejectsRelativePrompt(t *testing.T) {
	launch := Launch{Executable: "claude", Env: map[string]string{"GOTMPDIR": `C:\tmp\gotmp`}, PromptFile: "brief.md"}
	if _, err := launch.PowerShellLine(); err == nil {
		t.Fatal("PowerShellLine returned nil error for relative prompt")
	}
}

func TestValidateExecutablePreservesRunnerFailure(t *testing.T) {
	registry := DefaultRegistry()
	adapter, err := registry.Get(Claude)
	if err != nil {
		t.Fatalf("Get(Claude): %v", err)
	}
	want := errors.New("not found")
	runner := &fakeRunner{run: func(execx.Request) (execx.Result, error) {
		return execx.Result{}, want
	}}
	if err := adapter.Validate(context.Background(), runner); err == nil || !errors.Is(err, want) {
		t.Fatalf("Validate error = %v, want runner error", err)
	}
}

func assertLaunch(t *testing.T, got, want Launch) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Launch = %#v\nwant %#v", got, want)
	}
}

func assertRequests(t *testing.T, got, want []execx.Request) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %#v\nwant %#v", got, want)
	}
}

func equalStrings(left, right []string) bool {
	return reflect.DeepEqual(left, right)
}
