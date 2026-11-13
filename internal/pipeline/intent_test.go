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

// currentHelp is no-mistakes help axi run as 1.86.0 and later print it.
const currentHelp = "Flags:\n      --intent string        what the user set out to accomplish\n      --intent-file string   read intent from this file\n      --no-publish-intent    keep the generated Intent section out of the PR body for this run\n"

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
	runner := &helpRunner{help: currentHelp}

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
	if len(args) != 7 || strings.Join(args[:3], " ") != "axi run --intent-file" || args[4] != "--no-publish-intent" || start.IsOnCommandLine {
		t.Fatalf("the run starts as %q, want axi run --intent-file <file> --no-publish-intent and what the caller adds", args)
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

// A no-mistakes from 1.80.0 to before 1.86.0 has no --intent-file, and would
// take --intent - as an intent of one dash. For such a build alone the intent
// stays on the command line, and the start says so.
func TestAGateStartOnANoMistakesWithoutIntentFileKeepsTheCommandLineAndSaysSo(t *testing.T) {
	// Arrange
	runner := &helpRunner{help: "Flags:\n      --intent string        what the user set out to accomplish\n      --no-publish-intent    keep the generated Intent section out of the PR body\n"}

	// Act
	start, err := StartOf(context.Background(), runner, t.TempDir(), nil, "ship safely", nil)
	defer start.Close()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(start.Args, []string{"axi", "run", "--intent", "ship safely", "--no-publish-intent"}) || !start.IsOnCommandLine {
		t.Errorf("the run starts as %q with IsOnCommandLine %v, want the intent on the command line, kept out of the pull request, and said so", start.Args, start.IsOnCommandLine)
	}
}

// Issue 1866: on 2026-10-30 the pull request a gate opened, #1865, carried
// the task's whole intent as its Intent section: machine-local paths,
// ownership decisions and routing the brief kept private. Every gate start
// now keeps the intent out of the pull request, and no-mistakes keeps that on
// the run for every later body refresh, while its steps still read the whole
// intent.
func TestAGateStartKeepsItsPrivateIntentOutOfThePullRequest(t *testing.T) {
	// Arrange
	intent := "Fix the private intent leak.\nPRIVATE-INTENT-SENTINEL-1866 C:\\dev\\code-goblins\\state\\tasktmp\\brief.md\n"
	runner := &helpRunner{help: currentHelp}

	// Act
	start, err := StartOf(context.Background(), runner, t.TempDir(), nil, intent, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), execx.Request{Name: "no-mistakes", Args: start.Args}); err != nil {
		t.Fatal(err)
	}
	start.Close()

	// Assert
	if !slices.Contains(start.Args, "--no-publish-intent") {
		t.Fatalf("the run starts as %q, which publishes its intent in the pull request", start.Args)
	}
	if strings.Contains(strings.Join(start.Args, " "), "SENTINEL") {
		t.Errorf("the run starts as %q, with its private intent on the command line", start.Args)
	}
	if len(runner.intents) != 1 || runner.intents[0] != intent {
		t.Errorf("no-mistakes read %q, want the whole intent, sentinel included, for its steps", runner.intents)
	}
}

// A no-mistakes that cannot keep the intent out of the pull request, or whose
// help names nothing, starts no run and leaves no intent file behind.
func TestAGateStartOnANoMistakesThatWouldPublishItsIntentStartsNothing(t *testing.T) {
	for name, help := range map[string]string{
		"help that names no such flag": "Flags:\n      --intent string        what the user set out to accomplish\n      --intent-file string   read intent from this file\n",
		"no help at all":               "",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runner := &helpRunner{help: help}
			temp := t.TempDir()
			t.Setenv("TMP", temp)
			t.Setenv("TEMP", temp)

			// Act
			start, err := StartOf(context.Background(), runner, t.TempDir(), nil, "ship safely", nil)
			defer start.Close()

			// Assert
			if err == nil || !strings.Contains(err.Error(), "no run starts") || !strings.Contains(err.Error(), "1.80.0") {
				t.Fatalf("StartOf = %q, %v; want a refusal naming the no-mistakes to update to", start.Args, err)
			}
			if left, _ := os.ReadDir(temp); len(left) != 0 {
				t.Errorf("the refused start left %d files in the temporary folder", len(left))
			}
		})
	}
}

// A gate start hands no-mistakes each issue the run's pull request closes,
// which is the one place no-mistakes writes a live closing reference from:
// it neutralizes Fixes #N in every text an agent wrote, the intent included.
func TestAGateStartHandsNoMistakesTheIssuesItsPullRequestCloses(t *testing.T) {
	// Arrange
	runner := &helpRunner{help: "Flags:\n      --closes stringArray   issue this PR closes when merged\n      --intent-file string   read intent from this file\n      --no-publish-intent    keep the generated Intent section out of the PR body\n"}

	// Act
	start, err := StartOf(context.Background(), runner, t.TempDir(), nil, "ship safely", []string{"1996", "fpresta0607/code-goblins#1998"})
	defer start.Close()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(start.Args) != 9 || strings.Join(start.Args[:3], " ") != "axi run --intent-file" || strings.Join(start.Args[4:], " ") != "--no-publish-intent --closes 1996 --closes fpresta0607/code-goblins#1998" {
		t.Fatalf("the run starts as %q, want the intent file, the intent kept out of the pull request and one --closes for each issue", start.Args)
	}
}

// A no-mistakes before 1.87.0 takes no --closes, so a run it started would
// open a pull request that closes nothing: a start that names an issue to
// close starts no run and leaves no intent file behind.
func TestAGateStartThatClosesAnIssueOnANoMistakesWithoutClosesStartsNothing(t *testing.T) {
	// Arrange
	runner := &helpRunner{help: "Flags:\n      --intent-file string   read intent from this file\n      --no-publish-intent    keep the generated Intent section out of the PR body\n"}
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
