package main

import (
	"bytes"
	"context"
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
