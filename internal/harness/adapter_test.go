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

	for _, kind := range []Kind{Claude, Codex, Pi, Kimi} {
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

func TestPowerShellPrefixEscapesLiteralValues(t *testing.T) {
	launch := Launch{
		Env: map[string]string{
			"GOTMPDIR": `C:\task tmp\O'Brien\gotmp`,
			"PROMPT":   "100% O'Brien",
		},
		Dir: `C:\work\O'Brien\task`,
	}

	got, err := launch.PowerShellPrefix()
	if err != nil {
		t.Fatalf("PowerShellPrefix: %v", err)
	}
	want := `Set-Location -LiteralPath 'C:\work\O''Brien\task'; $env:GOTMPDIR = 'C:\task tmp\O''Brien\gotmp'; $env:PROMPT = '100% O''Brien'`
	if got != want {
		t.Errorf("PowerShellPrefix() = %q\nwant %q", got, want)
	}
}

func TestPowerShellPrefixRejectsRelativeDir(t *testing.T) {
	launch := Launch{Env: map[string]string{"GOTMPDIR": `C:\tmp\gotmp`}, Dir: "worktree"}
	if _, err := launch.PowerShellPrefix(); err == nil {
		t.Fatal("PowerShellPrefix returned nil error for relative dir")
	}
}

// TestControlContractForSwitch pins the stop/resume contract each real
// adapter advertises, which switch relies on to stop a harness on its own
// terms and to know whether a model-or-effort-only change can resume the
// harness's own session. The resume-arg shape is load-bearing: codex takes
// its resume as a subcommand and pi has none, so switch hands pi a written
// handoff instead.
func TestControlContractForSwitch(t *testing.T) {
	cases := []struct {
		kind          Kind
		stopKeys      []string
		stop          string
		resumeArgs    []string
		resumeMarkers []string
	}{
		{Claude, []string{"escape"}, "/exit", []string{"--continue"}, []string{"Resume from summary", "Resume full session as-is"}},
		{Codex, []string{"escape"}, "/quit", []string{"resume", "--last"}, nil},
		{Pi, []string{"escape"}, "/quit", nil, nil},
		{Kimi, []string{"escape"}, "/quit", []string{"--continue"}, nil},
	}
	registry := DefaultRegistry()
	for _, test := range cases {
		adapter, err := registry.Get(test.kind)
		if err != nil {
			t.Fatalf("Get(%q): %v", test.kind, err)
		}
		control := adapter.Control()
		if !equalStrings(control.StopKeys, test.stopKeys) {
			t.Errorf("%s StopKeys = %v, want %v", test.kind, control.StopKeys, test.stopKeys)
		}
		if control.StopCommand != test.stop {
			t.Errorf("%s StopCommand = %q, want %q", test.kind, control.StopCommand, test.stop)
		}
		if !equalStrings(control.ResumeArgs, test.resumeArgs) {
			t.Errorf("%s ResumeArgs = %v, want %v", test.kind, control.ResumeArgs, test.resumeArgs)
		}
		if !equalStrings(control.ResumeMarkers, test.resumeMarkers) {
			t.Errorf("%s ResumeMarkers = %v, want %v", test.kind, control.ResumeMarkers, test.resumeMarkers)
		}
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
