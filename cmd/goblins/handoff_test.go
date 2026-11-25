package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatehome"
	"github.com/fpresta0607/code-goblins/internal/handoff"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

func TestGoblinsHandoffRefusesAMissingOrInvalidTaskBeforeItReadsTheHome(t *testing.T) {
	for _, args := range [][]string{{"handoff"}, {"handoff", "Not A Task"}, {"handoff", "return"}, {"handoff", "cancel"}, {"handoff", "cancel", "../outside"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			// Arrange
			isHomeRead := false
			runtime := commandRuntime{resolveHome: func() (home.Home, error) {
				isHomeRead = true
				return home.Home{}, nil
			}}
			var output, failure bytes.Buffer

			// Act
			code := runWithRuntime(args, &output, &failure, runtime)

			// Assert
			if code != 2 || isHomeRead || !strings.Contains(failure.String(), handoffUsage) {
				t.Fatalf("code=%d home read=%v error=%q, want the usage", code, isHomeRead, failure.String())
			}
		})
	}
}

func TestGoblinsHandoffRefusesAHomeThatIsNotPrimary(t *testing.T) {
	// Arrange
	h := home.Home{Root: t.TempDir(), State: t.TempDir()}
	runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}
	var output, failure bytes.Buffer

	// Act
	code := runWithRuntime([]string{"handoff", "task"}, &output, &failure, runtime)

	// Assert
	if code != 1 || !strings.Contains(failure.String(), "primary home") {
		t.Fatalf("code=%d error=%q, want a primary home required", code, failure.String())
	}
}

// handoffRuntime is issueRuntime's home, whose projects root holds widgets
// and secret-project, a private one, with acme/widgets private too and a
// teammate kept, and a gate home of its own.
func handoffRuntime(t *testing.T) (commandRuntime, home.Home, state.TaskMeta) {
	t.Helper()
	t.Setenv(gatehome.Variable, t.TempDir())
	runtime, h := issueRuntime(t, &issueGH{private: map[string]bool{"acme/widgets": true, "acme/secret-project": true}})
	runtime.taskLifecycle = func(context.Context, home.Home, lifecycle.Request, string) (state.Lifecycle, error) {
		return state.Lifecycle{}, nil
	}
	if err := tickets.RememberTeammates(h.State, []tickets.Actor{{Login: "jdoe", Name: "Jane Doe"}}); err != nil {
		t.Fatal(err)
	}
	root, err := runtime.projectsRoot()
	if err != nil {
		t.Fatal(err)
	}
	return runtime, h, state.TaskMeta{ID: "task", Project: filepath.Join(root, "widgets")}
}

// A packet is public wherever it is published, so on a private repository
// too it may name no teammate and no private project.
func TestTheHandoffsScreenRefusesATeammateOrAPrivateProjectOnAPrivateRepository(t *testing.T) {
	for _, test := range []struct{ name, text, want string }{
		{name: "a teammate", text: "Ask Jane Doe how the parser reads headers.", want: "Jane Doe, a teammate"},
		{name: "a private project", text: "The parser mirrors secret-project's reader.", want: "secret-project, whose repository is not public"},
		{name: "neither", text: "The parser reads headers; its error messages are left.", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			runtime, h, meta := handoffRuntime(t)
			service, err := handoffService(runtime, h)
			if err != nil {
				t.Fatal(err)
			}

			// Act
			screen, err := service.Screen(context.Background(), meta, "acme/widgets", test.text)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			refusal := handoff.Refusal(screen, test.text)
			if test.want == "" && refusal != "" || !strings.Contains(refusal, test.want) {
				t.Fatalf("refusal = %q, want %q", refusal, test.want)
			}
		})
	}
}

// The handoff reads the gate run on the task's branch from the gate's
// database: only a run that has not finished holds the branch, and a
// database that cannot be read may hide one.
func TestTheHandoffsGateRunIsTheUnfinishedRunOnTheBranch(t *testing.T) {
	const head = "0123456789abcdef0123456789abcdef01234567"
	for _, test := range []struct {
		name, status string
		isUnreadable bool
		want         string
		wantErr      bool
	}{
		{name: "no run on the branch"},
		{name: "a finished run", status: "completed"},
		{name: "a running run", status: "running", want: "run-1"},
		{name: "a parked run", status: "awaiting_approval", want: "run-1"},
		{name: "an unreadable database", isUnreadable: true, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			runtime, h, meta := handoffRuntime(t)
			database := filepath.Join(os.Getenv(gatehome.Variable), "state.sqlite")
			if test.isUnreadable {
				if err := os.WriteFile(database, []byte("not a database"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				writer, err := sql.Open("sqlite", database)
				if err != nil {
					t.Fatal(err)
				}
				_, err = writer.Exec(`CREATE TABLE repos(id TEXT, working_path TEXT); CREATE TABLE runs(id TEXT, repo_id TEXT, branch TEXT, status TEXT, head_sha TEXT, last_pushed_sha TEXT, intent TEXT, worktree_dir TEXT, created_at INTEGER); INSERT INTO repos VALUES ('repo-1', ?);`, filepath.ToSlash(meta.Project))
				if err == nil && test.status != "" {
					_, err = writer.Exec(`INSERT INTO runs VALUES ('run-1', 'repo-1', 'feat/parser', ?, ?, '', '', '', 1)`, test.status, head)
				}
				if err := errors.Join(err, writer.Close()); err != nil {
					t.Fatal(err)
				}
			}
			service, err := handoffService(runtime, h)
			if err != nil {
				t.Fatal(err)
			}

			// Act
			run, err := service.GateRun(context.Background(), meta, "feat/parser")

			// Assert
			if (err != nil) != test.wantErr || run != test.want {
				t.Fatalf("gate run = %q, %v; want %q with an error %v", run, err, test.want, test.wantErr)
			}
		})
	}
}

func TestTheHandoffsLifecycleIsTheTasksLifecycleInItsHome(t *testing.T) {
	// Arrange
	runtime, h, meta := handoffRuntime(t)
	var gotHome home.Home
	var gotRequest lifecycle.Request
	runtime.taskLifecycle = func(_ context.Context, called home.Home, request lifecycle.Request, _ string) (state.Lifecycle, error) {
		gotHome, gotRequest = called, request
		return state.Lifecycle{ID: request.ID, Phase: "paused"}, nil
	}
	service, err := handoffService(runtime, h)
	if err != nil {
		t.Fatal(err)
	}
	request := lifecycle.Request{ID: meta.ID, Generation: "generation-1", Operation: "handoff-1", Action: "pause", Reason: "handoff"}

	// Act
	paused, err := service.Lifecycle(context.Background(), request)

	// Assert
	if err != nil || paused.Phase != "paused" || gotHome.State != h.State || gotRequest != request {
		t.Fatalf("lifecycle = %+v, %v; called in %+v with %+v", paused, err, gotHome, gotRequest)
	}
}

// returnGH answers the git requests goblins handoff return makes of a
// worktree on feat/parser of acme/widgets, and lists listed as the open pull
// requests into that branch, with gh working as acme; every other gh fails
// as GitHub down would.
type returnGH struct{ listed string }

func (f returnGH) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	asked := strings.Join(request.Args, " ")
	switch {
	case request.Name == "git" && strings.HasSuffix(asked, "remote get-url origin"):
		return execx.Result{Stdout: []byte("https://github.com/acme/widgets.git\n")}, nil
	case request.Name == "git" && strings.HasSuffix(asked, "symbolic-ref --quiet --short HEAD"):
		return execx.Result{Stdout: []byte("feat/parser\n")}, nil
	case request.Name == "gh" && strings.HasPrefix(asked, "pr list --repo acme/widgets --base feat/parser "):
		return execx.Result{Stdout: []byte(f.listed)}, nil
	case request.Name == "gh" && asked == "api user --jq .login":
		return execx.Result{Stdout: []byte("acme\n")}, nil
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("GitHub is down")}, nil
}

// handedOffRuntime is a primary home whose task is handed off as record
// says, read through gh.
func handedOffRuntime(t *testing.T, gh returnGH, record handoff.Record) commandRuntime {
	t.Helper()
	t.Setenv(gatehome.Variable, t.TempDir())
	h := primaryHomeFixture(t)
	meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", Backend: "native", Worktree: t.TempDir(), TaskTmp: t.TempDir(), Mode: "no-mistakes"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	handedOff := time.Now().UTC()
	record.Task, record.Started, record.HandedOff = meta.ID, handedOff, &handedOff
	record.Steps = []string{handoff.StepPause, handoff.StepPush, handoff.StepPullRequest, handoff.StepPacket}
	record.PullRequest, record.IsOpenedByHandoff = "https://github.com/acme/widgets/pull/1", true
	if err := handoff.WriteRecord(h.State, record); err != nil {
		t.Fatal(err)
	}
	return commandRuntime{
		resolveHome: func() (home.Home, error) { return h, nil },
		github:      gh,
		taskLifecycle: func(context.Context, home.Home, lifecycle.Request, string) (state.Lifecycle, error) {
			return state.Lifecycle{}, nil
		},
	}
}

func TestGoblinsHandoffReturnBringsBackThePullRequestPRNames(t *testing.T) {
	const listed = `[{"url":"https://github.com/acme/widgets/pull/2","state":"OPEN","headRefName":"claude/a","author":{"login":"teammate"}},{"url":"https://github.com/acme/widgets/pull/3","state":"OPEN","headRefName":"claude/b","author":{"login":"teammate"}}]`
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "no --pr", want: "2 open pull requests target feat/parser, so name one with --pr"},
		{name: "--pr the first", args: []string{"--pr", "https://github.com/acme/widgets/pull/2"}, want: "https://github.com/acme/widgets/pull/2 was opened by teammate"},
		{name: "--pr the second", args: []string{"--pr", "https://github.com/acme/widgets/pull/3"}, want: "https://github.com/acme/widgets/pull/3 was opened by teammate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			runtime := handedOffRuntime(t, returnGH{listed: listed}, handoff.Record{Head: "0123456789abcdef0123456789abcdef01234567"})
			var output, failure bytes.Buffer

			// Act
			code := runWithRuntime(append([]string{"handoff", "return", "task"}, test.args...), &output, &failure, runtime)

			// Assert
			if code != 1 || !strings.Contains(failure.String(), test.want) {
				t.Fatalf("code=%d error=%q, want %q", code, failure.String(), test.want)
			}
		})
	}
}

// A return that fails part-way names the steps of the return that ran, not
// those of the handoff.
func TestGoblinsHandoffReturnNamesTheReturnsStepsWhenItFails(t *testing.T) {
	// Arrange
	runtime := handedOffRuntime(t, returnGH{}, handoff.Record{Head: "0123456789abcdef0123456789abcdef01234567", ReturnSteps: []string{handoff.ReturnStepMerge, handoff.ReturnStepPush}})
	var output, failure bytes.Buffer

	// Act
	code := runWithRuntime([]string{"handoff", "return", "task"}, &output, &failure, runtime)

	// Assert
	if code != 1 || !strings.Contains(failure.String(), "steps that ran: [merge push]\n") {
		t.Fatalf("code=%d error=%q, want the return's steps named", code, failure.String())
	}
}
