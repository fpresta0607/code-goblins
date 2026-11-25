package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

const (
	testRepository = "acme/widgets"
	testBranch     = "feat/parser"
	testAccount    = "acme"
)

// fakeGitHub answers gh as GitHub would for the fixture's repository, with
// the goblin's pull requests by head.
type fakeGitHub struct {
	pullRequests []pullRequest
	calls        [][]string
	failCreate   int
	failClose    int
}

func (f *fakeGitHub) answer(args []string) (string, int) {
	f.calls = append(f.calls, args)
	flag := func(name string) string {
		if index := slices.Index(args, name); index >= 0 && index+1 < len(args) {
			return args[index+1]
		}
		return ""
	}
	listed := func(keep func(pullRequest) bool) string {
		var prs []pullRequest
		for _, pr := range f.pullRequests {
			if pr.State == "OPEN" && keep(pr) {
				prs = append(prs, pr)
			}
		}
		data, _ := json.Marshal(prs)
		return string(data)
	}
	switch {
	case args[0] == "repo" && args[1] == "view":
		return "main", 0
	case args[0] == "pr" && args[1] == "list" && flag("--head") != "":
		return listed(func(pr pullRequest) bool { return pr.HeadRefName == flag("--head") }), 0
	case args[0] == "pr" && args[1] == "view":
		for _, pr := range f.pullRequests {
			if pr.URL == args[2] {
				data, _ := json.Marshal(pr)
				return string(data), 0
			}
		}
		return "no pull request " + args[2], 1
	case args[0] == "pr" && args[1] == "create":
		if f.failCreate > 0 {
			f.failCreate--
			return "GitHub is down", 1
		}
		body, _ := os.ReadFile(flag("--body-file"))
		url := fmt.Sprintf("https://github.com/%s/pull/%d", testRepository, len(f.pullRequests)+1)
		f.pullRequests = append(f.pullRequests, pullRequest{URL: url, State: "OPEN", IsDraft: slices.Contains(args, "--draft"), Body: string(body), HeadRefName: flag("--head")})
		return "Creating draft pull request\n" + url, 0
	case args[0] == "pr" && args[1] == "edit":
		body, _ := os.ReadFile(flag("--body-file"))
		for index := range f.pullRequests {
			if f.pullRequests[index].URL == args[2] {
				f.pullRequests[index].Body = string(body)
				return "", 0
			}
		}
		return "no pull request " + args[2], 1
	case args[0] == "pr" && args[1] == "close":
		if f.failClose > 0 {
			f.failClose--
			return "GitHub is down", 1
		}
		for index := range f.pullRequests {
			if f.pullRequests[index].URL == args[2] {
				f.pullRequests[index].State = "CLOSED"
				return "", 0
			}
		}
		return "no pull request " + args[2], 1
	}
	return "unexpected gh " + strings.Join(args, " "), 2
}

func (f *fakeGitHub) published() []string {
	var calls []string
	for _, call := range f.calls {
		if call[0] == "pr" && (call[1] == "create" || call[1] == "edit" || call[1] == "close") {
			calls = append(calls, strings.Join(call[:2], " "))
		}
	}
	return calls
}

func (f *fakeGitHub) find(url string) pullRequest {
	for _, pr := range f.pullRequests {
		if pr.URL == url {
			return pr
		}
	}
	return pullRequest{}
}

// runner runs git for real, except that origin reads as a GitHub
// repository, and gh against fakeGitHub.
type runner struct{ github *fakeGitHub }

func (r runner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "gh" {
		stdout, code := r.github.answer(request.Args)
		if code != 0 {
			return execx.Result{Stderr: []byte(stdout), ExitCode: code}, nil
		}
		return execx.Result{Stdout: []byte(stdout)}, nil
	}
	if request.Name == "git" && slices.Contains(request.Args, "get-url") {
		return execx.Result{Stdout: []byte("https://github.com/" + testRepository + ".git\n")}, nil
	}
	command := exec.CommandContext(ctx, request.Name, request.Args...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return execx.Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: exitErr.ExitCode()}, nil
	}
	return execx.Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, err
}

type fixture struct {
	service   Service
	meta      state.TaskMeta
	github    *fakeGitHub
	origin    string
	worktree  string
	lifecycle *lifecycle.Service
	gateRun   string
	screen    tickets.Screen
	// handoff is what the goblin writes when its pause asks, or "" for a
	// goblin that writes none.
	handoff string
	resumes []lifecycle.Request
	// failPause has the lifecycle report its next pauses failed although
	// they took hold, as a pause whose answer was lost does.
	failPause int
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "core.autocrlf=false", "-c", "init.defaultBranch=main"}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func commit(t *testing.T, dir, name, text, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", name)
	git(t, dir, "commit", "--quiet", "-m", message)
	return git(t, dir, "rev-parse", "HEAD")
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	directory := t.TempDir()
	f := &fixture{github: &fakeGitHub{}, origin: filepath.Join(directory, "origin.git"), worktree: filepath.Join(directory, "worktree"), screen: tickets.Screen{Account: testAccount}, handoff: "The parser reads headers; its error messages are left."}
	git(t, directory, "init", "--quiet", "--bare", f.origin)
	git(t, directory, "clone", "--quiet", f.origin, f.worktree)
	for _, setting := range [][]string{{"user.name", "test"}, {"user.email", "test@example.invalid"}, {"core.autocrlf", "false"}} {
		git(t, f.worktree, "config", setting[0], setting[1])
	}
	git(t, f.worktree, "checkout", "--quiet", "-b", "main")
	commit(t, f.worktree, "README.md", "widgets\n", "chore: start")
	git(t, f.worktree, "push", "--quiet", "origin", "main")
	git(t, f.worktree, "checkout", "--quiet", "-b", testBranch)
	commit(t, f.worktree, "parser.go", "package parser\n", "feat(parser): read the header")
	git(t, f.worktree, "push", "--quiet", "origin", testBranch)

	stateDir, dataDir := filepath.Join(directory, "state"), filepath.Join(directory, "data")
	f.meta = state.TaskMeta{ID: "task", SpawnGen: "generation-1", Backend: "native", Worktree: f.worktree, TaskTmp: filepath.Join(directory, "tasktmp"), Mode: "no-mistakes"}
	for _, dir := range []string{f.meta.TaskTmp, stateDir, filepath.Join(dataDir, f.meta.ID)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.WriteTaskMeta(stateDir, f.meta); err != nil {
		t.Fatal(err)
	}
	f.lifecycle = &lifecycle.Service{StateDir: stateDir, PrepareWait: 10 * time.Millisecond, PauseWait: 10 * time.Millisecond, Operations: lifecycle.Operations{
		Prepare: func(_ context.Context, _ state.TaskMeta, path string) error {
			if f.handoff == "" {
				return nil
			}
			return os.WriteFile(path, []byte(f.handoff), 0o600)
		},
		Handoff: func(context.Context, state.TaskMeta) []byte {
			return []byte("You wrote no handoff of your own. Last status: working on C:\\private\\path")
		},
		Stop:   func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) { return nil, nil },
		Resume: func(context.Context, state.TaskMeta, *state.Lifecycle) error { return nil },
		Archive: func(context.Context, state.TaskMeta, *state.Lifecycle) (lifecycle.Preservation, error) {
			return lifecycle.Preservation{}, nil
		},
		Notify: func(state.Lifecycle) error { return nil },
	}}
	f.service = Service{
		StateDir: stateDir,
		DataDir:  dataDir,
		Commands: runner{github: f.github},
		Lifecycle: func(ctx context.Context, request lifecycle.Request) (state.Lifecycle, error) {
			if request.Action == "resume" {
				f.resumes = append(f.resumes, request)
			}
			record, err := f.lifecycle.Run(ctx, request)
			if request.Action == "pause" && f.failPause > 0 {
				f.failPause--
				return record, errors.New("the pause's answer was lost")
			}
			return record, err
		},
		GateRun: func(context.Context, state.TaskMeta, string) (string, error) { return f.gateRun, nil },
		Screen: func(context.Context, state.TaskMeta, string, string) (tickets.Screen, error) {
			return f.screen, nil
		},
	}
	return f
}

func (f *fixture) remoteHead(t *testing.T, branch string) string {
	t.Helper()
	listed := git(t, f.worktree, "ls-remote", "origin", "refs/heads/"+branch)
	head, _, _ := strings.Cut(listed, "\t")
	return head
}

func (f *fixture) phase(t *testing.T) (string, string) {
	t.Helper()
	record, err := state.ReadLifecycle(f.service.StateDir, f.meta.ID)
	if errors.Is(err, os.ErrNotExist) {
		return "", ""
	}
	if err != nil {
		t.Fatal(err)
	}
	if record.Pause == nil {
		return record.Phase, ""
	}
	return record.Phase, record.Pause.Reason
}

func TestEachRefusalChangesNothing(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(t *testing.T, f *fixture)
		isWIP   bool
		want    string
	}{
		{name: "a local-only task", arrange: func(t *testing.T, f *fixture) {
			f.writeMeta(t, func(meta *state.TaskMeta) { meta.Mode = "local-only" })
		}, want: "local-only"},
		{name: "a helper", arrange: func(t *testing.T, f *fixture) { f.writeMeta(t, func(meta *state.TaskMeta) { meta.Parent = "other" }) }, want: "helper of other"},
		{name: "a task with a working helper", arrange: func(t *testing.T, f *fixture) {
			if err := state.WriteTaskMeta(f.service.StateDir, state.TaskMeta{ID: "task-h1", SpawnGen: "generation-1", Parent: f.meta.ID}); err != nil {
				t.Fatal(err)
			}
		}, want: "task-h1"},
		{name: "a gate run on the branch", arrange: func(_ *testing.T, f *fixture) { f.gateRun = "run-1" }, want: "gate run run-1"},
		{name: "a dirty worktree", arrange: func(t *testing.T, f *fixture) {
			if err := os.WriteFile(filepath.Join(f.worktree, "draft.go"), []byte("package parser\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "--wip"},
		{name: "CI running on the branch's ready pull request", arrange: func(_ *testing.T, f *fixture) {
			f.github.pullRequests = append(f.github.pullRequests, pullRequest{URL: "https://github.com/acme/widgets/pull/1", State: "OPEN", HeadRefName: testBranch, StatusCheckRollup: []checkState{{Status: "IN_PROGRESS"}}})
		}, want: "CI is still running"},
		{name: "a paused goblin", arrange: func(t *testing.T, f *fixture) {
			if _, err := f.lifecycle.Run(t.Context(), lifecycle.Request{ID: f.meta.ID, Generation: f.meta.SpawnGen, Operation: "memory-1", Action: "pause", Reason: "memory"}); err != nil {
				t.Fatal(err)
			}
		}, want: "resume it first"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t)
			test.arrange(t, f)
			phase, reason := f.phase(t)
			head := f.remoteHead(t, testBranch)

			// Act
			_, _, err := f.service.Handoff(t.Context(), f.meta.ID, test.isWIP)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("handoff = %v, want a refusal naming %q", err, test.want)
			}
			if gotPhase, gotReason := f.phase(t); gotPhase != phase || gotReason != reason {
				t.Errorf("lifecycle went from %s/%s to %s/%s", phase, reason, gotPhase, gotReason)
			}
			if got := f.remoteHead(t, testBranch); got != head {
				t.Errorf("the branch was pushed: %s, was %s", got, head)
			}
			if published := f.github.published(); len(published) > 0 {
				t.Errorf("published %v", published)
			}
			if _, err := ReadRecord(f.service.StateDir, f.meta.ID); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a handoff record was written: %v", err)
			}
		})
	}
}

func (f *fixture) writeMeta(t *testing.T, change func(*state.TaskMeta)) {
	t.Helper()
	change(&f.meta)
	if err := state.WriteTaskMeta(f.service.StateDir, f.meta); err != nil {
		t.Fatal(err)
	}
}

func TestATaskAlreadyHandedOffIsRefused(t *testing.T) {
	// Arrange
	f := newFixture(t)
	if _, _, err := f.service.Handoff(t.Context(), f.meta.ID, false); err != nil {
		t.Fatal(err)
	}
	calls := len(f.github.published())

	// Act
	_, _, err := f.service.Handoff(t.Context(), f.meta.ID, false)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "already handed off") || len(f.github.published()) != calls {
		t.Fatalf("second handoff = %v, published %v", err, f.github.published())
	}
}

func TestAHandoffPausesThePushesAndPublishesThePacketInADraft(t *testing.T) {
	// Arrange
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.worktree, "notes.go"), []byte("package parser\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	record, prompt, err := f.service.Handoff(t.Context(), f.meta.ID, true)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if phase, reason := f.phase(t); phase != "paused" || reason != "handoff" {
		t.Errorf("lifecycle = %s/%s, want paused for handoff", phase, reason)
	}
	head := git(t, f.worktree, "rev-parse", "HEAD")
	if subject := git(t, f.worktree, "log", "-1", "--format=%s"); subject != "wip(handoff): task" || f.remoteHead(t, testBranch) != head || record.Head != head {
		t.Errorf("head %s %q, on GitHub %s, recorded %s", head, subject, f.remoteHead(t, testBranch), record.Head)
	}
	draft := f.github.find(record.PullRequest)
	if !draft.IsDraft || !record.IsOpenedByHandoff || !record.IsHandedOff() || record.PacketDigest == "" {
		t.Errorf("record %+v, draft %+v", record, draft)
	}
	for _, want := range []string{PacketStart, f.handoff, "`" + testBranch + "` at `" + head + "`", "read the header", "never merge"} {
		if !strings.Contains(draft.Body, want) {
			t.Errorf("draft body does not hold %q:\n%s", want, draft.Body)
		}
	}
	if !strings.Contains(prompt, record.PullRequest) || !strings.Contains(prompt, testRepository) {
		t.Errorf("prompt %q does not name the pull request and repository", prompt)
	}
}

func TestAHandoffWritesItsPacketIntoTheGoblinsOpenPullRequestAndKeepsItsBody(t *testing.T) {
	// Arrange
	f := newFixture(t)
	existing := pullRequest{URL: "https://github.com/acme/widgets/pull/1", State: "OPEN", HeadRefName: testBranch, Body: "## What Changed\n\nThe parser."}
	f.github.pullRequests = append(f.github.pullRequests, existing)

	// Act
	record, _, err := f.service.Handoff(t.Context(), f.meta.ID, false)

	// Assert
	body := f.github.find(existing.URL).Body
	if err != nil || record.PullRequest != existing.URL || record.IsOpenedByHandoff || !strings.HasPrefix(body, existing.Body) || !strings.Contains(body, PacketStart) {
		t.Fatalf("handoff = %v, record %+v, body %q", err, record, body)
	}
}

func TestAPacketCarriesNoneOfTheBriefTheDecisionsOrTheStatusLines(t *testing.T) {
	// Arrange
	f := newFixture(t)
	brief := filepath.Join(f.meta.TaskTmp, "brief.md")
	if err := os.WriteFile(brief, []byte("BRIEF-MARKER private routing"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.writeMeta(t, func(meta *state.TaskMeta) { meta.Brief = brief })
	if err := os.WriteFile(filepath.Join(f.service.DataDir, f.meta.ID, "answers-from-ceo.md"), []byte("DECISION-MARKER"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(f.service.StateDir, f.meta.ID, "working: STATUS-MARKER"); err != nil {
		t.Fatal(err)
	}

	// Act
	record, _, err := f.service.Handoff(t.Context(), f.meta.ID, false)

	// Assert
	body := f.github.find(record.PullRequest).Body
	if err != nil || body == "" {
		t.Fatalf("handoff = %v, body %q", err, body)
	}
	for _, marker := range []string{"BRIEF-MARKER", "DECISION-MARKER", "STATUS-MARKER"} {
		if strings.Contains(body, marker) {
			t.Errorf("the packet carries %s:\n%s", marker, body)
		}
	}
}

func TestAPauseThatWroteTheGoblinsHandoffItselfPublishesNothing(t *testing.T) {
	// Arrange
	f := newFixture(t)
	f.handoff = ""
	head := f.remoteHead(t, testBranch)

	// Act
	_, _, first := f.service.Handoff(t.Context(), f.meta.ID, false)
	_, _, second := f.service.Handoff(t.Context(), f.meta.ID, false)

	// Assert
	for _, err := range []error{first, second} {
		if err == nil || !strings.Contains(err.Error(), "wrote no handoff of its own") {
			t.Fatalf("handoff = %v, want it stopped for the fallback handoff", err)
		}
	}
	if phase, reason := f.phase(t); phase != "paused" || reason != "handoff" {
		t.Errorf("lifecycle = %s/%s, want it left paused for handoff", phase, reason)
	}
	if f.remoteHead(t, testBranch) != head || len(f.github.published()) > 0 {
		t.Errorf("pushed or published: head %s, calls %v", f.remoteHead(t, testBranch), f.github.published())
	}
}

func TestAScreenedPacketIsNeverPublished(t *testing.T) {
	// Arrange
	f := newFixture(t)
	f.handoff = `The parser is left; its log is at C:\Users\someone\parser.log.`
	head := f.remoteHead(t, testBranch)

	// Act
	_, _, err := f.service.Handoff(t.Context(), f.meta.ID, false)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "path on this machine") {
		t.Fatalf("handoff = %v, want the packet refused", err)
	}
	if f.remoteHead(t, testBranch) != head || len(f.github.published()) > 0 {
		t.Errorf("pushed or published: head %s, calls %v", f.remoteHead(t, testBranch), f.github.published())
	}
	if phase, reason := f.phase(t); phase != "paused" || reason != "handoff" {
		t.Errorf("lifecycle = %s/%s, want it left paused for handoff", phase, reason)
	}
}

func TestAFailureAfterThePauseIsRecordedAndASecondHandoffGoesOnFromThere(t *testing.T) {
	// Arrange
	f := newFixture(t)
	f.github.failCreate = 1

	// Act
	failed, _, first := f.service.Handoff(t.Context(), f.meta.ID, false)
	pauses := f.pauses(t)
	record, _, second := f.service.Handoff(t.Context(), f.meta.ID, false)

	// Assert
	if first == nil || !slices.Equal(failed.Steps, []string{StepPause, StepCommit, StepPush}) {
		t.Fatalf("first handoff = %v with steps %v, want it failed after the push", first, failed.Steps)
	}
	if phase, reason := f.phase(t); phase != "paused" || reason != "handoff" {
		t.Errorf("lifecycle = %s/%s, want it paused for handoff", phase, reason)
	}
	if second != nil || !record.IsHandedOff() || f.pauses(t) != pauses {
		t.Fatalf("second handoff = %v, record %+v; paused again %v", second, record, f.pauses(t) != pauses)
	}
}

// pauses counts the pause operations the task's lifecycle record went
// through, by the operation it last names.
func (f *fixture) pauses(t *testing.T) string {
	t.Helper()
	record, err := state.ReadLifecycle(f.service.StateDir, f.meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	return record.Operation
}

func handedOff(t *testing.T) (*fixture, Record) {
	t.Helper()
	f := newFixture(t)
	record, _, err := f.service.Handoff(t.Context(), f.meta.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return f, record
}

func TestCancelClosesOnlyTheDraftTheHandoffOpened(t *testing.T) {
	for _, test := range []struct {
		name       string
		hasOwnPR   bool
		wantClosed bool
	}{
		{name: "the handoff's draft", wantClosed: true},
		{name: "a reused pull request", hasOwnPR: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t)
			if test.hasOwnPR {
				f.github.pullRequests = append(f.github.pullRequests, pullRequest{URL: "https://github.com/acme/widgets/pull/1", State: "OPEN", HeadRefName: testBranch})
			}
			record, _, err := f.service.Handoff(t.Context(), f.meta.ID, false)
			if err != nil {
				t.Fatal(err)
			}

			// Act
			ended, err := f.service.Cancel(t.Context(), f.meta.ID)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if isClosed := f.github.find(record.PullRequest).State == "CLOSED"; isClosed != test.wantClosed {
				t.Errorf("pull request closed %v, want %v", isClosed, test.wantClosed)
			}
			if phase, _ := f.phase(t); phase != "running" || ended.IsActive() {
				t.Errorf("phase %s, record %+v", phase, ended)
			}
		})
	}
}

func TestCancelMarksThePacketCancelledAndKeepsTheGoblinsBranch(t *testing.T) {
	// Arrange
	f, record := handedOff(t)

	// Act
	cancelled, err := f.service.Cancel(t.Context(), f.meta.ID)

	// Assert
	if err != nil || cancelled.Outcome != OutcomeCancelled || git(t, f.worktree, "rev-parse", "HEAD") != record.Head || f.remoteHead(t, testBranch) != record.Head {
		t.Fatalf("cancel = %v, record %+v", err, cancelled)
	}
	if body := f.github.find(record.PullRequest).Body; !strings.Contains(body, cancelledLine) || !strings.Contains(body, PacketStart) {
		t.Errorf("body %q does not keep the packet marked cancelled", body)
	}
	if len(f.resumes) != 1 || !f.resumes[0].IsHandoffEnd {
		t.Errorf("resumes %+v", f.resumes)
	}
}

func TestAPauseThatTookHoldThoughItsAnswerWasLostIsTakenUpByASecondHandoff(t *testing.T) {
	// Arrange
	f := newFixture(t)
	f.failPause = 1

	// Act
	_, _, first := f.service.Handoff(t.Context(), f.meta.ID, false)
	record, _, second := f.service.Handoff(t.Context(), f.meta.ID, false)

	// Assert
	if first == nil {
		t.Fatal("the first handoff reported no failure")
	}
	if second != nil || !record.IsHandedOff() {
		t.Fatalf("second handoff = %v, record %+v, want it to go on from the pause that took hold", second, record)
	}
}

func TestCancelEndsAHandoffWhosePauseAnswerWasLost(t *testing.T) {
	// Arrange
	f := newFixture(t)
	f.failPause = 1
	if _, _, err := f.service.Handoff(t.Context(), f.meta.ID, false); err == nil {
		t.Fatal("the handoff reported no failure")
	}

	// Act
	cancelled, err := f.service.Cancel(t.Context(), f.meta.ID)

	// Assert
	if phase, _ := f.phase(t); err != nil || phase != "running" || cancelled.Outcome != OutcomeCancelled {
		t.Fatalf("cancel = %v, phase %s, record %+v, want the goblin resumed", err, phase, cancelled)
	}
}

// newSession has the task's goblin relaunched outside its lifecycle in a
// session of its own, as goblins switch relaunches one, leaving the earlier
// session's lifecycle record as it was.
func (f *fixture) newSession(t *testing.T) {
	t.Helper()
	f.meta.SpawnGen = "generation-2"
	if err := state.WriteTaskMeta(f.service.StateDir, f.meta); err != nil {
		t.Fatal(err)
	}
}

// A handoff whose pause no longer holds, because a plain resume ended a
// pause that wrote only the fallback or the goblin runs in a later session,
// takes its pause again rather than reading the old one.
func TestAHandoffWhosePauseNoLongerHoldsPausesAgain(t *testing.T) {
	for _, test := range []struct {
		name    string
		release func(*testing.T, *fixture)
	}{
		{name: "a plain resume after only the fallback", release: func(t *testing.T, f *fixture) {
			if _, err := f.lifecycle.Run(t.Context(), lifecycle.Request{ID: f.meta.ID, Generation: f.meta.SpawnGen, Operation: "plain-resume", Action: "resume", Reason: "overlord"}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a later session", release: func(t *testing.T, f *fixture) { f.newSession(t) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t)
			written := f.handoff
			f.handoff = ""
			if _, _, err := f.service.Handoff(t.Context(), f.meta.ID, false); err == nil || !strings.Contains(err.Error(), "wrote no handoff of its own") {
				t.Fatalf("first handoff = %v, want it stopped for the fallback handoff", err)
			}
			test.release(t, f)
			f.handoff = written

			// Act
			record, _, err := f.service.Handoff(t.Context(), f.meta.ID, false)

			// Assert
			if err != nil || !record.IsHandedOff() || !strings.Contains(f.github.find(record.PullRequest).Body, written) {
				t.Fatalf("second handoff = %+v, %v; want the goblin's own handoff published", record, err)
			}
		})
	}
}

// A lifecycle record of an earlier session says nothing of the goblin that
// runs now: its pause neither refuses a handoff as of a goblin not running,
// nor is resumed by a cancel.
func TestAnEarlierSessionsPauseNeitherRefusesAHandoffNorIsResumedByCancel(t *testing.T) {
	t.Run("a handoff", func(t *testing.T) {
		// Arrange
		f := newFixture(t)
		if _, err := f.lifecycle.Run(t.Context(), lifecycle.Request{ID: f.meta.ID, Generation: f.meta.SpawnGen, Operation: "overlord-pause", Action: "pause", Reason: "overlord"}); err != nil {
			t.Fatal(err)
		}
		f.newSession(t)

		// Act
		record, _, err := f.service.Handoff(t.Context(), f.meta.ID, false)

		// Assert
		if err != nil || !record.IsHandedOff() {
			t.Fatalf("handoff = %+v, %v; want the goblin of the later session handed off", record, err)
		}
	})
	t.Run("a cancel", func(t *testing.T) {
		// Arrange
		f := newFixture(t)
		f.handoff = ""
		if _, _, err := f.service.Handoff(t.Context(), f.meta.ID, false); err == nil {
			t.Fatal("first handoff published the fallback handoff")
		}
		f.newSession(t)

		// Act
		record, err := f.service.Cancel(t.Context(), f.meta.ID)

		// Assert
		if err != nil || record.Outcome != OutcomeCancelled || len(f.resumes) != 0 {
			t.Fatalf("cancel = %+v, %v, resumes %+v; want it ended with no resume of the earlier session", record, err, f.resumes)
		}
	})
}

// A handoff that copies its task's brief, a decision answered for it or one
// of its status lines word for word publishes nothing: the packet is
// public, and those are not.
func TestAHandoffCopyingTheBriefADecisionOrAStatusLineIsRefused(t *testing.T) {
	const copied = "Route the private rollout through the second staging account first"
	for _, test := range []struct {
		name  string
		write func(*testing.T, *fixture)
	}{
		{name: "the brief", write: func(t *testing.T, f *fixture) {
			brief := filepath.Join(f.meta.TaskTmp, "brief.md")
			if err := os.WriteFile(brief, []byte("# Brief\n"+copied+".\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			f.writeMeta(t, func(meta *state.TaskMeta) { meta.Brief = brief })
		}},
		{name: "a decision", write: func(t *testing.T, f *fixture) {
			if err := os.WriteFile(filepath.Join(f.service.DataDir, f.meta.ID, "answers-from-ceo.md"), []byte("Decision 12: "+copied+".\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a status line", write: func(t *testing.T, f *fixture) {
			if err := state.AppendStatus(f.service.StateDir, f.meta.ID, "working: "+copied); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t)
			test.write(t, f)
			f.handoff = "The parser reads headers.\nLeft: " + strings.ToLower(copied) + "."
			head := f.remoteHead(t, testBranch)

			// Act
			_, _, err := f.service.Handoff(t.Context(), f.meta.ID, false)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "copies") {
				t.Fatalf("handoff = %v, want it refused as a copy", err)
			}
			if phase, reason := f.phase(t); phase != "paused" || reason != "handoff" {
				t.Errorf("lifecycle = %s/%s, want it left paused for handoff", phase, reason)
			}
			if f.remoteHead(t, testBranch) != head || len(f.github.published()) > 0 {
				t.Errorf("pushed or published: head %s, calls %v", f.remoteHead(t, testBranch), f.github.published())
			}
		})
	}
}
