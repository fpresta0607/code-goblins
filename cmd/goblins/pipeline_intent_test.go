package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// intentFileRunner is a no-mistakes that takes a run's intent from a file, as
// 1.86.0 and later do: it answers the help the start asks for, and keeps what
// the file a run names held while the run started.
type intentFileRunner struct {
	*pipelineStartRunner
	// help is what its help for axi run names, the intent file alone unless
	// a test gives more.
	help    string
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
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: selection.Class, PipelineHash: selection.Hash, Harness: "codex", Model: "gpt-6.1-sol", Effort: "xhigh"}
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
		return execx.Result{Stdout: []byte(cmp.Or(runner.help, "      --intent-file string   read intent from this file\n      --no-publish-intent    keep the generated Intent section out of the PR body\n"))}, nil
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

// goblins pipeline run takes a run's intent from the file --intent-file names, or
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

// publishingNoMistakes is a no-mistakes before 1.80.0: its help names neither
// --no-publish-intent nor --intent-file, so a run it started would publish
// the gate's intent in the pull request it opens.
type publishingNoMistakes struct {
	*pipelineStartRunner
}

func (runner publishingNoMistakes) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "no-mistakes" && slices.Equal(request.Args, []string{"help", "axi", "run"}) {
		return execx.Result{Stdout: []byte("Flags:\n      --intent string   what the user set out to accomplish\n")}, nil
	}
	return runner.pipelineStartRunner.Run(ctx, request)
}

// Issue 1866: goblins pipeline run on a no-mistakes that would publish the
// gate's intent in its pull request starts no run, names the no-mistakes to
// update to, and leaves no intent file behind.
func TestPipelineRunOnANoMistakesThatWouldPublishItsIntentStartsNothing(t *testing.T) {
	// Arrange
	h, nm, fixture := intentFixture(t)
	runner := publishingNoMistakes{fixture.pipelineStartRunner}
	temp := t.TempDir()
	t.Setenv("TMP", temp)
	t.Setenv("TEMP", temp)

	// Act
	err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &bytes.Buffer{})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "no run starts") || !strings.Contains(err.Error(), "1.80.0") {
		t.Fatalf("pipeline run returned %v, want a refusal naming the no-mistakes to update to", err)
	}
	if len(runner.native) != 0 {
		t.Errorf("no-mistakes started %q, want no run", runner.native[0].Args)
	}
	if left, _ := filepath.Glob(filepath.Join(temp, "goblins-gate-intent-*")); len(left) != 0 {
		t.Errorf("the refused start left intent files %q behind", left)
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
		name          string
		intent        string
		submitted     string
		isOriginMoved bool
		wantReattach  bool
	}{
		{name: "the same branch, head and intent", intent: liveIntent, submitted: liveRunHead, wantReattach: true},
		{name: "a run that recorded no submitted head at the same head", intent: liveIntent, submitted: "", wantReattach: true},
		// A merge can land on main while the hold lasts, and a reattachment
		// starts nothing from origin, so tracking origin never fetched since
		// does not stop it (issue 1586).
		{name: "origin's default branch moved during the hold", intent: liveIntent, submitted: liveRunHead, isOriginMoved: true, wantReattach: true},
		{name: "the intent names other work", intent: "rework the billing export", submitted: liveRunHead},
		{name: "the branch moved since the run started", intent: liveIntent, submitted: movedRunHead},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, nm, runner := liveRunStart(t, "running", test.submitted, liveIntent)
			if test.isOriginMoved {
				// origin answers with a head past the one its tracking ref holds.
				runner.advance, runner.remoteRead = true, 2
			}
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
			name:    "a refusal of its own",
			result:  execx.Result{ExitCode: 1, Stdout: []byte("error: protected-path refusal requires an explicit response\n")},
			want:    "native command exited 1",
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

func acceptedHandbackStart(t *testing.T) (home.Home, string, taskWorktreesRunner, *pipelineStartRunner) {
	t.Helper()
	h, nm, branches := extraWorktreeTask(t)
	commands, start := acceptedHandbackFixture(t, h, branches)
	return h, nm, commands, start
}

// acceptedHandbackFixture answers an accepted CI handback for the task's
// registered extra, whose branches map is keyed by the recorded spelling.
func acceptedHandbackFixture(t *testing.T, h home.Home, branches map[string]string) (taskWorktreesRunner, *pipelineStartRunner) {
	t.Helper()
	meta, err := state.ReadTaskMeta(h.State, "task")
	if err != nil {
		t.Fatal(err)
	}
	caller, err := fsx.Canonical(meta.Extras[0])
	if err != nil {
		t.Fatal(err)
	}
	// The guard asks Git at the canonical caller; the recorded spelling stays
	// mapped for worktree selection.
	branches[caller] = branches[meta.Extras[0]]
	candidate, err := json.Marshal(map[string]string{"Head": liveRunHead, "BaseHead": movedRunHead, "ManagedBranch": "HEAD", "UpstreamURL": "https://example.com/widget.git", "ForkURL": "", "ReviewRoundID": "review-1"})
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]json.RawMessage{
		"receipt_round_id": json.RawMessage(`"round-1"`),
		"receipt_id":       json.RawMessage(`"receipt-1"`), "run_id": json.RawMessage(`"live"`), "repo_id": json.RawMessage(`"repo-1"`), "branch": json.RawMessage(`"fix/connections"`), "status": json.RawMessage(`"running"`),
		"head": json.RawMessage(`"` + liveRunHead + `"`), "submitted_head": json.RawMessage(`"` + movedRunHead + `"`), "intent": json.RawMessage(`"ship the correction"`),
		"released_head": json.RawMessage(`"` + movedRunHead + `"`), "accepted_head": json.RawMessage(`"` + liveRunHead + `"`), "accepted_at": json.RawMessage(`2`),
		"run_pr": json.RawMessage(`"https://example.com/widget/pull/1"`), "receipt_pr": json.RawMessage(`"https://example.com/widget/pull/1"`), "pr_state": json.RawMessage(`"open"`),
		"push_ref": json.RawMessage(`"refs/heads/fix/connections"`), "receipt_push_ref": json.RawMessage(`"refs/heads/fix/connections"`), "target": json.RawMessage(`"target-1"`), "receipt_target": json.RawMessage(`"target-1"`),
		"upstream_url": json.RawMessage(`"https://example.com/widget.git"`), "fork_url": json.RawMessage(`""`), "published_head": json.RawMessage(`"` + movedRunHead + `"`),
		"push_generation": json.RawMessage(`7`), "receipt_generation": json.RawMessage(`7`), "custody_returned": json.RawMessage(`0`), "push_active": json.RawMessage(`0`),
	}
	row["caller_dir"], err = json.Marshal(caller)
	if err != nil {
		t.Fatal(err)
	}
	row["candidate"], err = json.Marshal(string(candidate))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal([]map[string]json.RawMessage{row})
	if err != nil {
		t.Fatal(err)
	}
	start := &pipelineStartRunner{worktree: caller, head: liveRunHead, handback: data,
		previous: []byte(`[{"id":"live","status":"running","head":"` + liveRunHead + `","submitted_head":"` + movedRunHead + `","intent":"ship the correction"}]`)}
	return taskWorktreesRunner{Runner: start, branches: branches}, start
}

func TestPipelineRunReattachesToAcceptedCIHandback(t *testing.T) {
	h, nm, commands, start := acceptedHandbackStart(t)
	requireAcceptedHandbackReattachment(t, h, nm, commands, start)
}

// A hosted Windows runner can record a worktree under a spelling, such as a
// short name, that differs from its physical path; selection asks Git at the
// recorded spelling and the handback guard at the canonical one, so both must
// answer with the same checked-out branch.
func TestPipelineRunReattachesToAcceptedCIHandbackWithEquivalentCallerPath(t *testing.T) {
	// Arrange
	h, nm, branches := extraWorktreeTask(t)
	meta, err := state.ReadTaskMeta(h.State, "task")
	if err != nil {
		t.Fatal(err)
	}
	physical := meta.Extras[0]
	recorded := filepath.Dir(physical) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(physical)
	branches[recorded] = branches[physical]
	delete(branches, physical)
	meta.Extras[0] = recorded
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	commands, start := acceptedHandbackFixture(t, h, branches)

	// Act and Assert
	requireAcceptedHandbackReattachment(t, h, nm, commands, start)
}

func requireAcceptedHandbackReattachment(t *testing.T, h home.Home, nm string, commands taskWorktreesRunner, start *pipelineStartRunner) {
	t.Helper()
	// Arrange
	intentFile := filepath.Join(t.TempDir(), "intent.txt")
	if err := os.WriteFile(intentFile, []byte("ship the correction"), 0o600); err != nil {
		t.Fatal(err)
	}
	meta, err := state.ReadTaskMeta(h.State, "task")
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(h.State, "task.meta"), filepath.Join(meta.TaskTmp, "pipeline.json"), filepath.Join(nm, "config.yaml"), filepath.Join(nm, "state.sqlite")}
	before := make([][]byte, len(paths))
	for i, path := range paths {
		before[i], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer

	// Act
	err = pipelineCommand(t.Context(), h, nm, commands, []string{"run", "task", "--branch", "fix/connections", "--intent-file", intentFile}, &out)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(start.native) != 1 || !slices.Equal(start.native[0].Args, []string{"axi", "run"}) || !fsx.SamePath(start.native[0].Dir, meta.Extras[0]) {
		t.Fatalf("native calls = %+v; want exactly one bare reattachment in the registered extra", start.native)
	}
	if !strings.Contains(out.String(), "reattached to run live on fix/connections") {
		t.Fatalf("missing same-run report: %s", out.String())
	}
	for i, path := range paths {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before[i], after) {
			t.Fatalf("reattachment changed retained metadata, policy or state at %s: %v", path, err)
		}
	}
}

func TestPipelineRunRefusesInvalidCIHandbackReattachment(t *testing.T) {
	for _, name := range []string{"changed intent", "changed head", "foreign caller", "absent receipt"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			h, nm, commands, start := acceptedHandbackStart(t)
			intent := "ship the correction"
			switch name {
			case "changed intent":
				intent = "other work"
			case "changed head":
				start.head = strings.Repeat("d", 40)
			case "foreign caller":
				var rows []map[string]json.RawMessage
				if err := json.Unmarshal(start.handback, &rows); err != nil {
					t.Fatal(err)
				}
				rows[0]["caller_dir"] = json.RawMessage(`"foreign"`)
				data, err := json.Marshal(rows)
				if err != nil {
					t.Fatal(err)
				}
				start.handback = data
			case "absent receipt":
				start.handback = nil
			}
			intentFile := filepath.Join(t.TempDir(), "intent.txt")
			if err := os.WriteFile(intentFile, []byte(intent), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			err := pipelineCommand(t.Context(), h, nm, commands, []string{"run", "task", "--branch", "fix/connections", "--intent-file", intentFile}, &bytes.Buffer{})

			// Assert
			if !errors.Is(err, pipeline.ErrUnresolved) || len(start.native) != 0 {
				t.Fatalf("got %v, native calls %+v; want refusal without dispatch", err, start.native)
			}
		})
	}
}

// goblins pipeline run hands no-mistakes each issue --closes names, so the
// pull request the gate opens closes it on merge: no-mistakes neutralizes Fixes
// #N in every text an agent wrote, the intent included, and writes a live
// closing reference only from axi run --closes. A no-mistakes that takes no
// --closes, or a value that is not an issue, starts no run.
func TestPipelineRunHandsNoMistakesTheIssuesItsPullRequestCloses(t *testing.T) {
	withCloses := "      --closes stringArray   issue this PR closes when merged\n      --intent-file string   read intent from this file\n      --no-publish-intent    keep the generated Intent section out of the PR body\n"
	for _, row := range []struct {
		name   string
		help   string
		closes []string
		want   string
		failed string
	}{
		{"each issue given", withCloses, []string{"--closes", "1996", "--closes", "fpresta0607/code-goblins#1998"}, "--closes 1996 --closes fpresta0607/code-goblins#1998", ""},
		{"a no-mistakes that takes no closes", "", []string{"--closes", "1996"}, "", "1.87.0"},
		{"a value that is not an issue", withCloses, []string{"--closes", "1996,1998"}, "", "is not an issue number"},
		{"a path deeper than owner/repo", withCloses, []string{"--closes", "a/b/c#1"}, "", "is not an issue number"},
		{"a relative path for owner/repo", withCloses, []string{"--closes", "../..#5"}, "", "is not an issue number"},
	} {
		t.Run(row.name, func(t *testing.T) {
			// Arrange
			h, nm, runner := intentFixture(t)
			runner.help = row.help

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, append([]string{"run", "task", "--intent", "ship safely"}, row.closes...), &bytes.Buffer{})

			// Assert
			if row.failed != "" {
				if err == nil || !strings.Contains(err.Error(), row.failed) || len(runner.started) != 0 {
					t.Fatalf("err = %v with %d runs started, want no run and %q", err, len(runner.started), row.failed)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(runner.started) != 1 || !strings.Contains(strings.Join(runner.started[0].Args, " "), row.want) {
				t.Fatalf("no-mistakes started %+v, want one run with %q", runner.started, row.want)
			}
		})
	}
}
