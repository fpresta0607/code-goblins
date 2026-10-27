package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// intentFileRunner is a no-mistakes that takes a run's intent from a file, as
// 1.86.0 and later do: it answers the help the start asks for, and keeps what
// the file a run names held while the run started.
type intentFileRunner struct {
	*pipelineStartRunner
	started []execx.Request
	read    []string
}

// intentFixture is a home with one gated task, a no-mistakes home whose
// config matches the checked-in policy, and the no-mistakes that serves it.
func intentFixture(t *testing.T) (home.Home, string, *intentFileRunner) {
	t.Helper()
	policy, err := pipeline.Load(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	selection, err := policy.Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	nm := filepath.Join(root, "nm")
	tmp := filepath.Join(h.State, "tasktmp", "task")
	project := filepath.Join(root, "project")
	wt := filepath.Join(project, ".worktrees", "gb-task")
	for _, path := range []string{nm, tmp, project, wt} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := selection.Save(filepath.Join(tmp, "pipeline.json")); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: selection.Class, PipelineHash: selection.Hash}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	config, _, err := pipeline.Render([]byte("{}"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "config.yaml"), config, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "state.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return h, nm, &intentFileRunner{pipelineStartRunner: &pipelineStartRunner{worktree: wt}}
}

func (runner *intentFileRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "no-mistakes" && slices.Equal(request.Args, []string{"help", "axi", "run"}) {
		return execx.Result{Stdout: []byte("      --intent-file string   read intent from this file\n")}, nil
	}
	if request.Name == "no-mistakes" && len(request.Args) > 1 && request.Args[0] == "axi" && request.Args[1] == "run" {
		runner.started = append(runner.started, request)
		if at := slices.Index(request.Args, "--intent-file"); at >= 0 {
			content, err := os.ReadFile(request.Args[at+1])
			if err != nil {
				return execx.Result{}, err
			}
			runner.read = append(runner.read, string(content))
		}
		return execx.Result{}, nil
	}
	return runner.pipelineStartRunner.Run(ctx, request)
}

// cfo pipeline run takes a run's intent from the file --intent-file names, or
// as --intent, and either way starts no-mistakes with the intent in a file:
// no command line holds it.
func TestPipelineRunHandsTheIntentToNoMistakesInAFile(t *testing.T) {
	intent := "Ship the credential requests backend.\n" + strings.Repeat("Run the install in Windows PowerShell before the gate passes.\n", 40)
	for name, given := range map[string]func(t *testing.T) []string{
		"from a file": func(t *testing.T) []string {
			file := filepath.Join(t.TempDir(), "intent.md")
			if err := os.WriteFile(file, []byte(intent), 0o600); err != nil {
				t.Fatal(err)
			}
			return []string{"--intent-file", file}
		},
		"as text": func(*testing.T) []string { return []string{"--intent", intent} },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			h, nm, runner := intentFixture(t)

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, append([]string{"run", "task"}, given(t)...), &bytes.Buffer{})

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(runner.started) != 1 || len(runner.read) != 1 || runner.read[0] != intent {
				t.Fatalf("no-mistakes started %d runs and read %q, want one run whose file holds the intent as given", len(runner.started), runner.read)
			}
			args := runner.started[0].Args
			if strings.Join(args[:3], " ") != "axi run --intent-file" {
				t.Errorf("the run started as %q, want axi run --intent-file <file>", args)
			}
			for _, argument := range args {
				if strings.Contains(argument, "PowerShell") {
					t.Errorf("the intent is on no-mistakes' command line: %q", argument)
				}
			}
			if _, err := os.Stat(args[3]); !os.IsNotExist(err) {
				t.Errorf("the file the intent was handed over in is still there (%v)", err)
			}
		})
	}
}

// An intent given twice, or not at all, starts nothing.
func TestPipelineRunRefusesAnIntentGivenTwiceOrNotAtAll(t *testing.T) {
	file := filepath.Join(t.TempDir(), "intent.md")
	if err := os.WriteFile(file, []byte("ship safely"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(empty, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		args []string
		want string
	}{
		"both a file and text": {[]string{"--intent-file", file, "--intent", "ship safely"}, "not both"},
		"neither":              {nil, "is required"},
		"an empty file":        {[]string{"--intent-file", empty}, "must not be empty"},
		"a file that is gone":  {[]string{"--intent-file", filepath.Join(t.TempDir(), "gone.md")}, "read --intent-file"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			h, nm, runner := intentFixture(t)

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, append([]string{"run", "task"}, test.args...), &bytes.Buffer{})

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("pipeline run returned %v, want a refusal saying %q", err, test.want)
			}
			if len(runner.started) != 0 {
				t.Errorf("no-mistakes started %d runs, want none", len(runner.started))
			}
		})
	}
}

// liveRunHead is the commit a task branch and its live run both hold.
const liveRunHead = "0123456789abcdef0123456789abcdef01234567"

// movedRunHead is a commit the branch holds after the live run started.
const movedRunHead = "89abcdef0123456789abcdef0123456789abcdef"

// liveRunStart is a version 6 gated task whose branch holds one live run at
// liveRunHead with the intent, and the runner that answers its start.
func liveRunStart(t *testing.T, status, submittedHead, intent string) (home.Home, string, *gateStartRunner) {
	t.Helper()
	policy := versionSixPolicy(t, pipeline.Reviewer{})
	h, nm := gateHome(t, policy, operatorMachineConfig)
	wt, _ := gatedTask(t, h, "task", policy, claudeGoblin)
	runner := &gateStartRunner{pipelineStartRunner: pipelineStartRunner{
		worktree: wt,
		head:     liveRunHead,
		previous: []byte(`[{"id":"live","status":"` + status + `","head":"` + liveRunHead + `","submitted_head":"` + submittedHead + `","intent":"` + intent + `"}]`),
	}}
	return h, nm, runner
}

// A managed start whose native bounded hold ended with no decision reattaches
// to the live run of the same branch, head and intent, which keeps working and
// its own review budget, so the command the wait-expiry output names is not one
// the driver refuses (issue 1421). A start of other work is still refused.
func TestPipelineRunReattachesToTheLiveRunOfTheSameWork(t *testing.T) {
	const liveIntent = "ship the connection fix safely"
	for _, test := range []struct {
		name         string
		intent       string
		submitted    string
		wantReattach bool
	}{
		{name: "the same branch, head and intent", intent: liveIntent, submitted: liveRunHead, wantReattach: true},
		{name: "a run that recorded no submitted head at the same head", intent: liveIntent, submitted: "", wantReattach: true},
		{name: "the intent names other work", intent: "rework the billing export", submitted: liveRunHead},
		{name: "the branch moved since the run started", intent: liveIntent, submitted: movedRunHead},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, nm, runner := liveRunStart(t, "running", test.submitted, liveIntent)
			var out bytes.Buffer

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", test.intent}, &out)

			// Assert
			if !test.wantReattach {
				if !errors.Is(err, pipeline.ErrUnresolved) {
					t.Fatalf("pipeline run returned %v, want the unresolved earlier-run refusal", err)
				}
				if len(runner.native) != 0 {
					t.Fatalf("no-mistakes started %v, want nothing", runner.native[0].Args)
				}
				if !strings.Contains(err.Error(), "live") {
					t.Errorf("refusal %q does not name the live run", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("pipeline run: %v", err)
			}
			if len(runner.native) != 1 {
				t.Fatalf("native launches=%+v, want one", runner.native)
			}
			// Reattachment takes no intent and no launch selection: the run
			// already carries its own, and a different selection cannot change
			// an active run.
			if args := strings.Join(runner.native[0].Args, " "); args != "axi run" {
				t.Fatalf("native launch argv %q, want the bare reattachment", args)
			}
			if want := "pipeline: reattached to run live on feat/policy"; !strings.Contains(out.String(), want) {
				t.Errorf("output %q does not say %q", out.String(), want)
			}
		})
	}
}

// When the native hold ends with no decision, the driver says the run keeps
// working and names the start that reattaches, never only the exit code. Any
// other native refusal is still reported as itself.
func TestPipelineRunReportsABoundedWaitAsNotAFailure(t *testing.T) {
	const liveIntent = "ship the connection fix safely"
	for _, test := range []struct {
		name    string
		result  execx.Result
		want    string
		wantNot string
	}{
		{
			name:    "the bounded hold ended",
			result:  execx.Result{ExitCode: 1, Stdout: []byte("error: wait of 8m0s elapsed while driving the run\nhelp: Re-run `no-mistakes axi run` to reattach for another 8m0s\n")},
			want:    "Repeat this command to reattach",
			wantNot: "native command exited 1",
		},
		{
			name:   "a refusal of its own",
			result: execx.Result{ExitCode: 1, Stdout: []byte("error: protected-path refusal requires an explicit response\n")},
			want:   "native command exited 1",
			wantNot: "Repeat this command to reattach",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, nm, runner := liveRunStart(t, "running", liveRunHead, liveIntent)
			runner.nativeResult = test.result

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", liveIntent}, &bytes.Buffer{})

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("pipeline run returned %v, want a refusal saying %q", err, test.want)
			}
			if strings.Contains(err.Error(), test.wantNot) {
				t.Errorf("refusal %q says %q", err, test.wantNot)
			}
		})
	}
}
