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

	for _, kind := range []Kind{"kimi", "grok", "opencode", "raw command", "unknown"} {
		if _, err := registry.Get(kind); err == nil {
			t.Errorf("Get(%q) returned nil error", kind)
		}
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
		kind       Kind
		stopKeys   []string
		stop       string
		resumeArgs []string
	}{
		{Claude, []string{"escape"}, "/exit", []string{"--continue"}},
		{Codex, []string{"escape"}, "/quit", []string{"resume", "--last"}},
		{Pi, []string{"escape"}, "/quit", nil},
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

// TestEveryAdapterStampsTheGoblinRole is the terminal half of the guard that
// keeps the CFO's hooks out of a goblin's session. It lives in the launch
// contract rather than in the project credentials a preflight returns,
// because a project that declares no services returns no credentials at all
// and its goblin would start unstamped.
func TestEveryAdapterStampsTheGoblinRole(t *testing.T) {
	registry := DefaultRegistry()
	for _, kind := range []Kind{Claude, Codex, Pi} {
		adapter, err := registry.Get(kind)
		if err != nil {
			t.Fatalf("Get(%s): %v", kind, err)
		}
		if kind == Pi {
			// Pi is the one adapter that refuses to build before it has
			// read its own --help.
			runner := &fakeRunner{run: func(execx.Request) (execx.Result, error) {
				return execx.Result{Stdout: []byte("  --tui-mode <mode>  TUI mode\n")}, nil
			}}
			if err := adapter.Validate(context.Background(), runner); err != nil {
				t.Fatalf("Validate(pi): %v", err)
			}
		}
		launch, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Scratch: `C:\gotmp\task`})
		if err != nil {
			t.Fatalf("Build(%s): %v", kind, err)
		}
		if got := launch.Env[RoleVariable]; got != RoleGoblin {
			t.Errorf("%s launch %s = %q, want %q", kind, RoleVariable, got, RoleGoblin)
		}
	}
}

// A launch with no Go temporary directory would leave the goblin inheriting
// the operator's own %TEMP%, which is what the per-task directory exists to
// prevent, so the build refuses it rather than falling back.
func TestBuildRequiresAnAbsoluteScratch(t *testing.T) {
	for _, goTmp := range []string{"", "   ", `gotmp\task`} {
		if _, err := DefaultRegistry().Adapters[Claude].Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Scratch: goTmp}); err == nil {
			t.Errorf("Build(Scratch=%q) = nil, want refusal", goTmp)
		}
	}
}
