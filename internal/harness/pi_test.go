package harness

import (
	"context"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestPiBuildUsesOnlyHelpAdvertisedFlags(t *testing.T) {
	registry := DefaultRegistry()
	adapter, err := registry.Get(Pi)
	if err != nil {
		t.Fatalf("Get(Pi): %v", err)
	}
	runner := &fakeRunner{run: func(request execx.Request) (execx.Result, error) {
		if request.Name != "pi" || !equalStrings(request.Args, []string{"--help"}) {
			t.Fatalf("unexpected probe: %#v", request)
		}
		return execx.Result{Stdout: []byte(`
Options:
  --model <pattern>              Model pattern
  --thinking <level>             Set thinking level: low, medium, high, xhigh, max
  --extension, -e <path>         Load an extension
  --tui-mode <mode>              TUI mode: regular (default) or fullscreen
`)}, nil
	}}
	if err := adapter.Validate(context.Background(), runner); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	defaults, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`})
	if err != nil {
		t.Fatalf("Build defaults: %v", err)
	}
	assertLaunch(t, defaults, Launch{
		Args:       []string{"--tui-mode", "regular"},
		Env:        map[string]string{"GOTMPDIR": `C:\tasks\task\gotmp`},
		PromptFile: `C:\briefs\task.md`,
	})

	explicit, err := adapter.Build(LaunchSpec{
		BriefPath:       `C:\briefs\task.md`,
		TaskTmp:         `C:\tasks\task`,
		Model:           "openai/gpt-5.2-codex",
		Effort:          "xhigh",
		PiExtensionPath: `C:\extensions\task.ts`,
	})
	if err != nil {
		t.Fatalf("Build explicit: %v", err)
	}
	if got, want := explicit.Args, []string{"--tui-mode", "regular", "--model", "openai/gpt-5.2-codex", "--thinking", "xhigh", "--extension", `C:\extensions\task.ts`}; !equalStrings(got, want) {
		t.Errorf("Args = %#v, want %#v", got, want)
	}
}

func TestPiRefusesRequestedFlagsMissingFromHelp(t *testing.T) {
	registry := DefaultRegistry()
	adapter, err := registry.Get(Pi)
	if err != nil {
		t.Fatalf("Get(Pi): %v", err)
	}
	runner := &fakeRunner{run: func(execx.Request) (execx.Result, error) {
		return execx.Result{Stdout: []byte("Usage: pi\n  --help\n")}, nil
	}}
	if err := adapter.Validate(context.Background(), runner); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	launch, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`})
	if err != nil {
		t.Fatalf("Build defaults: %v", err)
	}
	if len(launch.Args) != 0 {
		t.Errorf("default args = %#v, want no unadvertised options", launch.Args)
	}

	for _, spec := range []LaunchSpec{
		{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Model: "model"},
		{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Effort: "high"},
		{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, PiExtensionPath: `C:\extensions\task.ts`},
	} {
		if _, err := adapter.Build(spec); err == nil {
			t.Errorf("Build(%#v) returned nil error", spec)
		}
	}
}

func TestPiRefusesUnsupportedEffortAndRequiresHelpProbe(t *testing.T) {
	registry := DefaultRegistry()
	adapter, err := registry.Get(Pi)
	if err != nil {
		t.Fatalf("Get(Pi): %v", err)
	}
	if _, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`}); err == nil {
		t.Fatal("Build returned nil error before a Pi help probe")
	}

	runner := &fakeRunner{run: func(execx.Request) (execx.Result, error) {
		return execx.Result{Stdout: []byte("--thinking <level> low medium\n")}, nil
	}}
	if err := adapter.Validate(context.Background(), runner); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if _, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Effort: "xhigh"}); err == nil {
		t.Fatal("Build returned nil error for unsupported Pi effort")
	}
	assertRequests(t, runner.requests, []execx.Request{{Name: "pi", Args: []string{"--help"}}})
}

func TestPiDoesNotTreatExamplesAsAdvertisedOptions(t *testing.T) {
	registry := DefaultRegistry()
	adapter, err := registry.Get(Pi)
	if err != nil {
		t.Fatalf("Get(Pi): %v", err)
	}
	runner := &fakeRunner{run: func(execx.Request) (execx.Result, error) {
		return execx.Result{Stdout: []byte("Usage: pi\nExample: pi --model candidate --thinking high\n")}, nil
	}}
	if err := adapter.Validate(context.Background(), runner); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if _, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Model: "candidate"}); err == nil {
		t.Fatal("Build returned nil error after --model appeared only in an example")
	}
}

func TestPiIgnoresOptionLikeTextAfterOptionsSection(t *testing.T) {
	registry := DefaultRegistry()
	adapter, err := registry.Get(Pi)
	if err != nil {
		t.Fatalf("Get(Pi): %v", err)
	}
	runner := &fakeRunner{run: func(execx.Request) (execx.Result, error) {
		return execx.Result{Stdout: []byte(`Usage: pi [options]

Options:
  --help                          Show help

Examples:
  --thinking high
  --model candidate
  --extension C:\extensions\task.ts
  --tui-mode regular
`)}, nil
	}}
	if err := adapter.Validate(context.Background(), runner); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	_, err = adapter.Build(LaunchSpec{
		BriefPath:       `C:\briefs\task.md`,
		TaskTmp:         `C:\tasks\task`,
		Model:           "candidate",
		Effort:          "high",
		PiExtensionPath: `C:\extensions\task.ts`,
	})
	if err == nil {
		t.Fatal("Build returned nil error for option-like text that appeared only after Options")
	}
}
