package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// longIntent is an intent of the kind Microsoft Defender detected on a
// command line: several thousand characters of prose that name a shell.
var longIntent = "  Ship the credential requests backend.\n" + strings.Repeat("Review the launcher change and run the install in Windows PowerShell before the gate passes.\n", 60)

// helpRunner answers no-mistakes help axi run with help, and records every
// other request with the content of the intent file it names, read while the
// command runs, as no-mistakes reads it.
type helpRunner struct {
	help     string
	requests []execx.Request
	intents  []string
}

func (runner *helpRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	if slices.Equal(request.Args, []string{"help", "axi", "run"}) {
		return execx.Result{Stdout: []byte(runner.help)}, nil
	}
	runner.requests = append(runner.requests, request)
	if at := slices.Index(request.Args, "--intent-file"); at >= 0 {
		content, err := os.ReadFile(request.Args[at+1])
		if err != nil {
			return execx.Result{}, err
		}
		runner.intents = append(runner.intents, string(content))
	}
	return execx.Result{}, nil
}

// A gate run's intent reaches no-mistakes in a file, whole and unchanged, and
// never on a command line: Defender detected a goblin's long intent there as
// Trojan:Win32/ClickFix.DQ!MTB on 2026-10-02, and Windows refused the start.
func TestAGateStartHandsItsIntentOverInAFile(t *testing.T) {
	// Arrange
	runner := &helpRunner{help: "Flags:\n      --intent string        what the user set out to accomplish\n      --intent-file string   read intent from this file\n"}

	// Act
	start, err := StartOf(context.Background(), runner, t.TempDir(), nil, longIntent, nil)
	if err != nil {
		t.Fatal(err)
	}
	args := append(start.Args, "--wait", "45s")
	if _, err := runner.Run(context.Background(), execx.Request{Name: "no-mistakes", Args: args}); err != nil {
		t.Fatal(err)
	}
	start.Close()

	// Assert
	if len(args) != 6 || strings.Join(args[:3], " ") != "axi run --intent-file" || start.IsOnCommandLine {
		t.Fatalf("the run starts as %q, want axi run --intent-file <file> and what the caller adds", args)
	}
	for _, argument := range args {
		if strings.Contains(argument, "PowerShell") || strings.Contains(argument, "\n") {
			t.Errorf("the intent is on the command line: %q", argument)
		}
	}
	if len(runner.intents) != 1 || runner.intents[0] != longIntent {
		t.Errorf("no-mistakes read %q from the file, want the intent as given", runner.intents)
	}
	if _, err := os.Stat(args[3]); !os.IsNotExist(err) {
		t.Errorf("the intent file %s is still there after the start (%v)", args[3], err)
	}
}

// A no-mistakes older than 1.86.0 has no --intent-file, and would take
// --intent - as an intent of one dash. For such a build alone the intent
// stays on the command line, and the start says so.
func TestAGateStartOnANoMistakesWithoutIntentFileKeepsTheCommandLineAndSaysSo(t *testing.T) {
	for name, help := range map[string]string{
		"help that names no intent file": "Flags:\n      --intent string   what the user set out to accomplish\n",
		"no help at all":                 "",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runner := &helpRunner{help: help}

			// Act
			start, err := StartOf(context.Background(), runner, t.TempDir(), nil, "ship safely", nil)
			defer start.Close()

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(start.Args, []string{"axi", "run", "--intent", "ship safely"}) || !start.IsOnCommandLine {
				t.Errorf("the run starts as %q with IsOnCommandLine %v, want the intent on the command line and said so", start.Args, start.IsOnCommandLine)
			}
		})
	}
}

// A gate start hands no-mistakes each issue the run's pull request closes,
// which is the one place no-mistakes writes a live closing reference from:
// it neutralizes Fixes #N in every text an agent wrote, the intent included.
func TestAGateStartHandsNoMistakesTheIssuesItsPullRequestCloses(t *testing.T) {
	// Arrange
	runner := &helpRunner{help: "Flags:\n      --closes stringArray   issue this PR closes when merged\n      --intent-file string   read intent from this file\n"}

	// Act
	start, err := StartOf(context.Background(), runner, t.TempDir(), nil, "ship safely", []string{"1996", "fpresta0607/code-goblins#1998"})
	defer start.Close()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(start.Args) != 8 || strings.Join(start.Args[:3], " ") != "axi run --intent-file" || strings.Join(start.Args[4:], " ") != "--closes 1996 --closes fpresta0607/code-goblins#1998" {
		t.Fatalf("the run starts as %q, want the intent file and one --closes for each issue", start.Args)
	}
}

// A no-mistakes before 1.87.0 takes no --closes, so a run it started would
// open a pull request that closes nothing: a start that names an issue to
// close starts no run and leaves no intent file behind.
func TestAGateStartThatClosesAnIssueOnANoMistakesWithoutClosesStartsNothing(t *testing.T) {
	// Arrange
	runner := &helpRunner{help: "Flags:\n      --intent-file string   read intent from this file\n"}
	before, err := filepath.Glob(filepath.Join(os.TempDir(), "goblins-gate-intent-*"))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	start, err := StartOf(context.Background(), runner, t.TempDir(), nil, "ship safely", []string{"1996"})
	defer start.Close()

	// Assert
	if err == nil || !strings.Contains(err.Error(), "no run starts") || !strings.Contains(err.Error(), "1.87.0") || len(start.Args) != 0 {
		t.Fatalf("StartOf = %q, %v; want no start and the version that takes --closes named", start.Args, err)
	}
	after, err := filepath.Glob(filepath.Join(os.TempDir(), "goblins-gate-intent-*"))
	if err != nil || len(after) != len(before) {
		t.Fatalf("intent files %v after the refusal, want the %d there before it", after, len(before))
	}
}
