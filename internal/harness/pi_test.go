package harness

import (
	"context"
	"strings"
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

	defaults, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Scratch: `C:\gotmp\task`})
	if err != nil {
		t.Fatalf("Build defaults: %v", err)
	}
	assertLaunch(t, defaults, Launch{
		Args:       []string{"--tui-mode", "fullscreen"},
		Env:        map[string]string{"CFO_ROLE": RoleGoblin, "GOTMPDIR": `C:\gotmp\task`, "TEMP": `C:\gotmp\task`, "TMP": `C:\gotmp\.tmp`, "TMPDIR": `C:\gotmp\task`},
		PromptFile: `C:\briefs\task.md`,
		Executable: "pi",
	})

	explicit, err := adapter.Build(LaunchSpec{
		BriefPath:       `C:\briefs\task.md`,
		TaskTmp:         `C:\tasks\task`,
		Scratch:         `C:\gotmp\task`,
		Model:           "openai/gpt-5.2-codex",
		Effort:          "xhigh",
		PiExtensionPath: `C:\extensions\task.ts`,
	})
	if err != nil {
		t.Fatalf("Build explicit: %v", err)
	}
	if got, want := explicit.Args, []string{"--tui-mode", "fullscreen", "--model", "openai/gpt-5.2-codex", "--thinking", "xhigh", "--extension", `C:\extensions\task.ts`}; !equalStrings(got, want) {
		t.Errorf("Args = %#v, want %#v", got, want)
	}
}

// A Pi that can trust a folder's project files for one run is told to, so it
// never asks, and nothing is saved to its trust store.
func TestPiTrustsProjectFilesForItsRunWhenItCanSayTo(t *testing.T) {
	adapter := DefaultRegistry().Adapters[Pi]
	runner := &fakeRunner{run: func(execx.Request) (execx.Result, error) {
		return execx.Result{Stdout: []byte(`
Options:
  --tui-mode <mode>              TUI mode: regular (default) or fullscreen
  --approve, -a                  Trust project-local files for this run
  --no-approve, -na              Ignore project-local files for this run
`)}, nil
	}}
	if err := adapter.Validate(context.Background(), runner); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	launch, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Scratch: `C:\gotmp\task`})

	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got, want := launch.Args, []string{"--approve", "--tui-mode", "fullscreen"}; !equalStrings(got, want) {
		t.Errorf("Args = %#v, want %#v", got, want)
	}
}

// The Overlord, 2026-10-17: "when you scroll on like Pi and Codex terminals,
// it doesn't keep like the bottom banner". In its regular mode pi's footer is
// output that scrolls away with the history; in its fullscreen mode, as
// Claude Code's for the CFO, pi keeps its editor and footer on the bottom
// rows and scrolls its transcript above them itself. A pi whose --tui-mode
// offers no fullscreen is given no mode, so it draws the only way it can. A
// pi CFO's screen is read from the same help, so it starts as its goblins do.
func TestPiDrawsItsOwnFullScreenWhenItsHelpOffersOne(t *testing.T) {
	cases := []struct {
		name, help string
		want       []string
	}{
		{"fullscreen offered", "Options:\n  --tui-mode <mode>              TUI mode: regular (default) or fullscreen\n", []string{"--tui-mode", "fullscreen"}},
		{"only regular", "Options:\n  --tui-mode <mode>              TUI mode: regular\n", nil},
		{"no TUI mode", "Options:\n  --help                          Show help\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			adapter := DefaultRegistry().Adapters[Pi]
			runner := &fakeRunner{run: func(execx.Request) (execx.Result, error) { return execx.Result{Stdout: []byte(tc.help)}, nil }}
			if err := adapter.Validate(context.Background(), runner); err != nil {
				t.Fatalf("Validate: %v", err)
			}

			// Act
			launch, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Scratch: `C:\gotmp\task`})
			screen, screenErr := PiScreenArgs(context.Background(), runner)

			// Assert
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if !equalStrings(launch.Args, tc.want) {
				t.Errorf("Args = %#v, want %#v", launch.Args, tc.want)
			}
			if screenErr != nil {
				t.Fatalf("PiScreenArgs: %v", screenErr)
			}
			if !equalStrings(screen, tc.want) {
				t.Errorf("PiScreenArgs = %#v, want %#v", screen, tc.want)
			}
			for _, request := range runner.requests {
				if request.Name != "pi" || !equalStrings(request.Args, []string{"--help"}) {
					t.Errorf("probe = %#v, want pi --help", request)
				}
			}
		})
	}
}

// A pi that does not answer its help is not started with a screen guessed
// for it: the CFO's start reports why instead.
func TestPiScreenArgsRefuseAPiThatDoesNotAnswerItsHelp(t *testing.T) {
	// Arrange
	runner := &fakeRunner{run: func(execx.Request) (execx.Result, error) {
		return execx.Result{ExitCode: 1, Stderr: []byte("Cannot find module")}, nil
	}}

	// Act
	screen, err := PiScreenArgs(context.Background(), runner)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "Cannot find module") {
		t.Fatalf("PiScreenArgs = %#v, %v; want the failed help reported", screen, err)
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

	launch, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Scratch: `C:\gotmp\task`})
	if err != nil {
		t.Fatalf("Build defaults: %v", err)
	}
	if len(launch.Args) != 0 {
		t.Errorf("default args = %#v, want no unadvertised options", launch.Args)
	}

	for _, spec := range []LaunchSpec{
		{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Scratch: `C:\gotmp\task`, Model: "model"},
		{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Scratch: `C:\gotmp\task`, Effort: "high"},
		{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Scratch: `C:\gotmp\task`, PiExtensionPath: `C:\extensions\task.ts`},
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
	if _, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Scratch: `C:\gotmp\task`}); err == nil {
		t.Fatal("Build returned nil error before a Pi help probe")
	}

	runner := &fakeRunner{run: func(execx.Request) (execx.Result, error) {
		return execx.Result{Stdout: []byte("--thinking <level> low medium\n")}, nil
	}}
	if err := adapter.Validate(context.Background(), runner); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if _, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Scratch: `C:\gotmp\task`, Effort: "xhigh"}); err == nil {
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
	if _, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Scratch: `C:\gotmp\task`, Model: "candidate"}); err == nil {
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
		Scratch:         `C:\gotmp\task`,
		Model:           "candidate",
		Effort:          "high",
		PiExtensionPath: `C:\extensions\task.ts`,
	})
	if err == nil {
		t.Fatal("Build returned nil error for option-like text that appeared only after Options")
	}
}
