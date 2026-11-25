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

// fakeGitHub answers gh as GitHub would for the fixture's repository: the
// goblin's pull requests by head, the cloud's by base.
type fakeGitHub struct {
	pullRequests []pullRequest
	bases        map[string]string
	calls        [][]string
	failCreate   int
	failEdit     int
	failClose    int
	failPush     int
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
	case args[0] == "api" && args[1] == "user":
		return testAccount, 0
	case args[0] == "pr" && args[1] == "list" && flag("--head") != "":
		return listed(func(pr pullRequest) bool { return pr.HeadRefName == flag("--head") }), 0
	case args[0] == "pr" && args[1] == "list" && flag("--base") != "":
		return listed(func(pr pullRequest) bool { return f.bases[pr.URL] == flag("--base") }), 0
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
		f.pullRequests = append(f.pullRequests, pullRequest{URL: url, State: "OPEN", IsDraft: slices.Contains(args, "--draft"), Body: string(body), HeadRefName: flag("--head"), Author: author{Login: testAccount}})
		f.bases[url] = flag("--base")
		return "Creating draft pull request\n" + url, 0
	case args[0] == "pr" && args[1] == "edit":
		if f.failEdit > 0 {
			f.failEdit--
			return "GitHub is down", 1
		}
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
// repository that refuses the pushes failPush counts, and gh against
// fakeGitHub.
type runner struct{ github *fakeGitHub }

func (r runner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "git" && slices.Contains(request.Args, "push") && r.github.failPush > 0 {
		r.github.failPush--
		return execx.Result{Stderr: []byte("GitHub is down"), ExitCode: 128}, nil
	}
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
	cloud     string
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
	f := &fixture{github: &fakeGitHub{bases: map[string]string{}}, origin: filepath.Join(directory, "origin.git"), worktree: filepath.Join(directory, "worktree"), cloud: filepath.Join(directory, "cloud"), screen: tickets.Screen{Account: testAccount}, handoff: "The parser reads headers; its error messages are left."}
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

// runningChecks are a check run queued or in progress and a commit status
// pending or expected, as gh lists the checks of a head that has not
// finished.
var runningChecks = []checkState{{Status: "IN_PROGRESS"}, {Status: "QUEUED"}, {State: "PENDING"}, {State: "EXPECTED"}}

// openCloudPullRequest has a cloud session branch from the goblin's head,
// commit and open a pull request into the goblin's branch, as login.
func (f *fixture) openCloudPullRequest(t *testing.T, from, name, text, login string) pullRequest {
	t.Helper()
	if _, err := os.Stat(f.cloud); errors.Is(err, os.ErrNotExist) {
		git(t, filepath.Dir(f.cloud), "clone", "--quiet", f.origin, f.cloud)
	}
	number := len(f.github.pullRequests) + 1
	branch := fmt.Sprintf("claude/cloud-work-%d", number)
	git(t, f.cloud, "fetch", "--quiet", "origin")
	git(t, f.cloud, "checkout", "--quiet", "-B", branch, from)
	head := commit(t, f.cloud, name, text, "feat(parser): name the bad header")
	git(t, f.cloud, "push", "--quiet", "origin", branch)
	pr := pullRequest{URL: fmt.Sprintf("https://github.com/%s/pull/%d", testRepository, number), State: "OPEN", HeadRefName: branch, HeadRefOid: head, Author: author{Login: login}, Body: "Ran go test ./internal/parser: ok."}
	f.github.pullRequests = append(f.github.pullRequests, pr)
	f.github.bases[pr.URL] = testBranch
	return pr
}

func TestEachRefusalChangesNothing(t *testing.T) {
	type refusalCase struct {
		name    string
		arrange func(t *testing.T, f *fixture)
		isWIP   bool
		want    string
	}
	var ciCases []refusalCase
	for _, check := range runningChecks {
		ciCases = append(ciCases, refusalCase{name: fmt.Sprintf("CI %s%s on the branch's ready pull request", check.Status, check.State), arrange: func(_ *testing.T, f *fixture) {
			f.github.pullRequests = append(f.github.pullRequests, pullRequest{URL: "https://github.com/acme/widgets/pull/1", State: "OPEN", HeadRefName: testBranch, StatusCheckRollup: []checkState{{Status: "COMPLETED"}, check}})
		}, want: "CI is still running"})
	}
	for _, test := range append([]refusalCase{
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
		{name: "a paused goblin", arrange: func(t *testing.T, f *fixture) {
			if _, err := f.lifecycle.Run(t.Context(), lifecycle.Request{ID: f.meta.ID, Generation: f.meta.SpawnGen, Operation: "memory-1", Action: "pause", Reason: "memory"}); err != nil {
				t.Fatal(err)
			}
		}, want: "resume it first"},
	}, ciCases...) {
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

func TestReturnAndCancelCloseOnlyTheDraftTheHandoffOpened(t *testing.T) {
	for _, test := range []struct {
		name       string
		hasOwnPR   bool
		isReturn   bool
		wantClosed bool
	}{
		{name: "return closes the handoff's draft", isReturn: true, wantClosed: true},
		{name: "cancel closes the handoff's draft", wantClosed: true},
		{name: "return leaves a reused pull request", hasOwnPR: true, isReturn: true},
		{name: "cancel leaves a reused pull request", hasOwnPR: true},
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
			f.openCloudPullRequest(t, record.Head, "errors.go", "package parser\n", testAccount)

			// Act
			var ended Record
			if test.isReturn {
				ended, err = f.service.Return(t.Context(), f.meta.ID, "")
			} else {
				ended, err = f.service.Cancel(t.Context(), f.meta.ID)
			}

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
	f.openCloudPullRequest(t, record.Head, "errors.go", "package parser\n", testAccount)

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

// Only a ready pull request's CI holds a handoff back, so CI running on the
// goblin's draft does not.
func TestCIRunningOnTheGoblinsDraftDoesNotRefuseAHandoff(t *testing.T) {
	// Arrange
	f := newFixture(t)
	draft := pullRequest{URL: "https://github.com/acme/widgets/pull/1", State: "OPEN", IsDraft: true, HeadRefName: testBranch, StatusCheckRollup: runningChecks}
	f.github.pullRequests = append(f.github.pullRequests, draft)

	// Act
	record, _, err := f.service.Handoff(t.Context(), f.meta.ID, false)

	// Assert
	if err != nil || !record.IsHandedOff() || record.PullRequest != draft.URL {
		t.Fatalf("handoff = %+v, %v; want the draft carrying the packet", record, err)
	}
}

// The CI a handoff's own push starts does not refuse the second handoff that
// goes on after it, since no push is left to cut that run short.
func TestTheCIAHandoffsOwnPushStartedDoesNotRefuseItsSecondHandoff(t *testing.T) {
	// Arrange
	f := newFixture(t)
	ready := pullRequest{URL: "https://github.com/acme/widgets/pull/1", State: "OPEN", HeadRefName: testBranch, StatusCheckRollup: []checkState{{Status: "COMPLETED"}}}
	f.github.pullRequests = append(f.github.pullRequests, ready)
	f.github.failEdit = 1
	failed, _, first := f.service.Handoff(t.Context(), f.meta.ID, false)
	if first == nil || !slices.Contains(failed.Steps, StepPush) || slices.Contains(failed.Steps, StepPacket) {
		t.Fatalf("first handoff = %v with steps %v, want it failed after the push", first, failed.Steps)
	}
	f.github.pullRequests[0].StatusCheckRollup = []checkState{{Status: "IN_PROGRESS"}}

	// Act
	record, _, err := f.service.Handoff(t.Context(), f.meta.ID, false)

	// Assert
	if err != nil || !record.IsHandedOff() || !strings.Contains(f.github.find(ready.URL).Body, PacketStart) {
		t.Fatalf("second handoff = %+v, %v; want it to go on and write the packet", record, err)
	}
}

// Changes the goblin left in its worktree while it paused are never pushed
// without --wip: the handoff stops after the pause, with nothing pushed or
// written, and the goblin stays paused for it.
func TestChangesLeftAtThePauseWithoutWIPPublishNothing(t *testing.T) {
	// Arrange
	f := newFixture(t)
	prepare := f.lifecycle.Operations.Prepare
	f.lifecycle.Operations.Prepare = func(ctx context.Context, meta state.TaskMeta, path string) error {
		if err := os.WriteFile(filepath.Join(f.worktree, "draft.go"), []byte("package parser\n"), 0o600); err != nil {
			return err
		}
		return prepare(ctx, meta, path)
	}
	head := f.remoteHead(t, testBranch)

	// Act
	record, _, err := f.service.Handoff(t.Context(), f.meta.ID, false)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "--wip") {
		t.Fatalf("handoff = %v, want it stopped for the changes left at the pause", err)
	}
	if !slices.Equal(record.Steps, []string{StepPause}) || f.remoteHead(t, testBranch) != head || len(f.github.published()) > 0 {
		t.Errorf("steps %v, head %s, calls %v; want nothing committed, pushed or published", record.Steps, f.remoteHead(t, testBranch), f.github.published())
	}
	if phase, reason := f.phase(t); phase != "paused" || reason != "handoff" {
		t.Errorf("lifecycle = %s/%s, want it left paused for handoff", phase, reason)
	}
}

func TestReturnBringsTheCloudsWorkOntoTheGoblinsBranch(t *testing.T) {
	for _, test := range []struct {
		name string
		// isDiverged has the goblin's branch hold a commit the cloud
		// session did not branch from, so the return merges.
		isDiverged bool
	}{
		{name: "it fast-forwards"},
		{name: "it merges", isDiverged: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f, record := handedOff(t)
			from := record.Head
			if test.isDiverged {
				from = git(t, f.worktree, "rev-parse", "HEAD~1")
			}
			cloud := f.openCloudPullRequest(t, from, "errors.go", "package parser\n", testAccount)

			// Act
			returned, err := f.service.Return(t.Context(), f.meta.ID, "")

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			head := git(t, f.worktree, "rev-parse", "HEAD")
			isMerge := git(t, f.worktree, "rev-list", "--count", "--merges", record.Head+".."+head) != "0"
			if isMerge != test.isDiverged || f.remoteHead(t, testBranch) != head || !slices.Contains(strings.Fields(git(t, f.worktree, "rev-list", head)), cloud.HeadRefOid) {
				t.Errorf("head %s (merge %v) on GitHub %s does not carry %s as it should", head, isMerge, f.remoteHead(t, testBranch), cloud.HeadRefOid)
			}
			isCloudCommit := len(returned.CloudCommits) == 1 && returned.CloudCommits[0] != "" && strings.HasPrefix(cloud.HeadRefOid, returned.CloudCommits[0])
			if phase, _ := f.phase(t); phase != "running" || returned.Outcome != OutcomeReturned || returned.CloudPullRequest != cloud.URL || !isCloudCommit {
				t.Errorf("phase %s, record %+v; want the cloud's one commit %s named", phase, returned, cloud.HeadRefOid)
			}
			if len(f.resumes) != 1 || !strings.Contains(f.resumes[0].ResumeNote, cloud.URL) || !isCloudCommit || !strings.Contains(f.resumes[0].ResumeNote, returned.CloudCommits[0]) || !strings.Contains(f.resumes[0].ResumeNote, "validates the whole branch") {
				t.Errorf("resumes %+v", f.resumes)
			}
		})
	}
}

func TestReturnStopsOnAConflictWithTheWorktreeUnchanged(t *testing.T) {
	// Arrange
	f, record := handedOff(t)
	f.openCloudPullRequest(t, git(t, f.worktree, "rev-parse", "HEAD~1"), "parser.go", "package other\n", testAccount)

	// Act
	_, err := f.service.Return(t.Context(), f.meta.ID, "")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "merge was aborted") {
		t.Fatalf("return = %v, want it stopped on the conflict", err)
	}
	if head := git(t, f.worktree, "rev-parse", "HEAD"); head != record.Head || git(t, f.worktree, "status", "--porcelain") != "" {
		t.Errorf("worktree at %s, status %q; want it as it was at %s", head, git(t, f.worktree, "status", "--porcelain"), record.Head)
	}
	if phase, reason := f.phase(t); phase != "paused" || reason != "handoff" {
		t.Errorf("lifecycle = %s/%s, want it still handed off", phase, reason)
	}
}

// Each refusal of a return leaves the goblin's branch as it was, in the
// worktree and on GitHub, and the task handed off.
func TestEachReturnRefusalChangesNothing(t *testing.T) {
	cloudWork := func(t *testing.T, f *fixture, record Record) pullRequest {
		return f.openCloudPullRequest(t, record.Head, "errors.go", "package parser\n", testAccount)
	}
	for _, test := range []struct {
		name string
		// arrange readies the refusal and gives the pull request Return is
		// asked for with --pr.
		arrange func(*testing.T, *fixture, Record) string
		want    string
	}{
		{name: "the branch moved in the worktree", want: "moved since the handoff", arrange: func(t *testing.T, f *fixture, record Record) string {
			cloudWork(t, f, record)
			commit(t, f.worktree, "late.go", "package parser\n", "fix(parser): a late fix")
			return ""
		}},
		{name: "the branch moved on GitHub", want: "moved since the handoff", arrange: func(t *testing.T, f *fixture, record Record) string {
			cloudWork(t, f, record)
			commit(t, f.worktree, "late.go", "package parser\n", "fix(parser): a late fix")
			git(t, f.worktree, "push", "--quiet", "origin", "HEAD:refs/heads/"+testBranch)
			git(t, f.worktree, "reset", "--quiet", "--hard", record.Head)
			return ""
		}},
		{name: "a dirty worktree", want: "uncommitted changes", arrange: func(t *testing.T, f *fixture, record Record) string {
			cloudWork(t, f, record)
			if err := os.WriteFile(filepath.Join(f.worktree, "notes.txt"), []byte("half a thought\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return ""
		}},
		{name: "a pull request from a fork", want: "comes from another repository", arrange: func(t *testing.T, f *fixture, record Record) string {
			cloudWork(t, f, record)
			f.github.pullRequests[len(f.github.pullRequests)-1].IsCrossRepository = true
			return ""
		}},
		{name: "a cloud head GitHub no longer has", want: "moved while it was fetched", arrange: func(t *testing.T, f *fixture, record Record) string {
			cloudWork(t, f, record)
			f.github.pullRequests[len(f.github.pullRequests)-1].HeadRefOid = record.Head
			return ""
		}},
		{name: "several pull requests and no --pr", want: "name one with --pr", arrange: func(t *testing.T, f *fixture, record Record) string {
			cloudWork(t, f, record)
			f.openCloudPullRequest(t, record.Head, "names.go", "package parser\n", testAccount)
			return ""
		}},
		{name: "a --pr that does not target the branch", want: "is not an open pull request into " + testBranch, arrange: func(t *testing.T, f *fixture, record Record) string {
			cloudWork(t, f, record)
			return "https://github.com/" + testRepository + "/pull/99"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f, record := handedOff(t)
			url := test.arrange(t, f, record)
			head, remote, status := git(t, f.worktree, "rev-parse", "HEAD"), f.remoteHead(t, testBranch), git(t, f.worktree, "status", "--porcelain")

			// Act
			_, err := f.service.Return(t.Context(), f.meta.ID, url)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("return = %v, want a refusal for %q", err, test.want)
			}
			if git(t, f.worktree, "rev-parse", "HEAD") != head || f.remoteHead(t, testBranch) != remote || git(t, f.worktree, "status", "--porcelain") != status {
				t.Errorf("the branch changed: head %s, GitHub %s; want %s and %s", git(t, f.worktree, "rev-parse", "HEAD"), f.remoteHead(t, testBranch), head, remote)
			}
			kept, err := ReadRecord(f.service.StateDir, f.meta.ID)
			if phase, reason := f.phase(t); err != nil || !kept.IsHandedOff() || len(kept.ReturnSteps) != 0 || phase != "paused" || reason != "handoff" || len(f.resumes) != 0 {
				t.Errorf("record %+v (%v), lifecycle %s/%s, resumes %+v; want it still handed off", kept, err, phase, reason, f.resumes)
			}
		})
	}
}

func TestReturnBringsBackThePullRequestPRNames(t *testing.T) {
	// Arrange
	f, record := handedOff(t)
	other := f.openCloudPullRequest(t, record.Head, "errors.go", "package parser\n", testAccount)
	named := f.openCloudPullRequest(t, record.Head, "names.go", "package parser\n", testAccount)

	// Act
	returned, err := f.service.Return(t.Context(), f.meta.ID, named.URL)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	carried := strings.Fields(git(t, f.worktree, "rev-list", "HEAD"))
	if returned.CloudPullRequest != named.URL || !slices.Contains(carried, named.HeadRefOid) || slices.Contains(carried, other.HeadRefOid) {
		t.Fatalf("record %+v; want only %s brought back", returned, named.URL)
	}
}

// A return whose push failed pushes, when run again, only the head its
// merge made, and only while the goblin is still paused for the handoff.
func TestASecondReturnPushesOnlyWhatTheFirstMerged(t *testing.T) {
	for _, test := range []struct {
		name string
		move func(*testing.T, *fixture)
		want string
	}{
		{name: "nothing moved", move: func(*testing.T, *fixture) {}},
		{name: "a goblin of a later session runs", move: func(t *testing.T, f *fixture) { f.newSession(t) }, want: "not paused for its handoff"},
		{name: "the branch moved since the merge", move: func(t *testing.T, f *fixture) {
			commit(t, f.worktree, "late.go", "package parser\n", "fix(parser): a late fix")
		}, want: "moved since the cloud's work was merged"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f, record := handedOff(t)
			cloud := f.openCloudPullRequest(t, record.Head, "errors.go", "package parser\n", testAccount)
			f.github.failPush = 1
			first, err := f.service.Return(t.Context(), f.meta.ID, "")
			if err == nil || !slices.Contains(first.ReturnSteps, ReturnStepMerge) || slices.Contains(first.ReturnSteps, ReturnStepPush) {
				t.Fatalf("first return = %v with steps %v, want it stopped at the push", err, first.ReturnSteps)
			}
			test.move(t, f)

			// Act
			returned, err := f.service.Return(t.Context(), f.meta.ID, "")

			// Assert
			if test.want == "" {
				if phase, _ := f.phase(t); err != nil || phase != "running" || returned.Outcome != OutcomeReturned || f.remoteHead(t, testBranch) != cloud.HeadRefOid {
					t.Fatalf("second return = %v, phase %s, GitHub at %s; want %s pushed and the goblin resumed", err, phase, f.remoteHead(t, testBranch), cloud.HeadRefOid)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) || f.remoteHead(t, testBranch) != record.Head || !returned.IsHandedOff() || len(f.resumes) != 0 {
				t.Fatalf("second return = %v, GitHub at %s, record %+v; want %q refused with nothing pushed", err, f.remoteHead(t, testBranch), returned, test.want)
			}
		})
	}
}

func TestReturnRefusesWhileTheGoblinsReadyPullRequestHasCIRunning(t *testing.T) {
	// Arrange
	f := newFixture(t)
	ready := pullRequest{URL: "https://github.com/acme/widgets/pull/1", State: "OPEN", HeadRefName: testBranch}
	f.github.pullRequests = append(f.github.pullRequests, ready)
	record, _, err := f.service.Handoff(t.Context(), f.meta.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	f.openCloudPullRequest(t, record.Head, "errors.go", "package parser\n", testAccount)
	f.github.pullRequests[0].StatusCheckRollup = []checkState{{State: "PENDING"}}

	// Act
	_, err = f.service.Return(t.Context(), f.meta.ID, "")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "CI is still running") || f.remoteHead(t, testBranch) != record.Head {
		t.Fatalf("return = %v with GitHub at %s, want a refusal and nothing pushed", err, f.remoteHead(t, testBranch))
	}
}

func TestReturnRefusesACloudPullRequestOpenedByAnotherAccount(t *testing.T) {
	// Arrange
	f, record := handedOff(t)
	f.openCloudPullRequest(t, record.Head, "errors.go", "package parser\n", "teammate")

	// Act
	_, err := f.service.Return(t.Context(), f.meta.ID, "")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "the Overlord's press") || f.remoteHead(t, testBranch) != record.Head {
		t.Fatalf("return = %v, want a refusal and nothing pushed", err)
	}
}

func TestASecondReturnFinishesOnceTheFirstPushedTheCloudsWork(t *testing.T) {
	// Arrange
	f, record := handedOff(t)
	cloud := f.openCloudPullRequest(t, record.Head, "errors.go", "package parser\n", testAccount)
	f.github.failClose = 1
	if _, err := f.service.Return(t.Context(), f.meta.ID, ""); err == nil {
		t.Fatal("the first return reported no failure")
	}
	for index := range f.github.pullRequests {
		if f.github.pullRequests[index].URL == cloud.URL {
			f.github.pullRequests[index].State = "MERGED"
		}
	}

	// Act
	returned, err := f.service.Return(t.Context(), f.meta.ID, "")

	// Assert
	if phase, _ := f.phase(t); err != nil || phase != "running" || returned.Outcome != OutcomeReturned || f.github.find(record.PullRequest).State != "CLOSED" {
		t.Fatalf("second return = %v, phase %s, record %+v", err, phase, returned)
	}
}

// A goblin relaunched in a later session while its task was handed off is
// running on the branch, so bringing the cloud's work onto that branch would
// make two writers of it: return refuses and the task stays handed off.
func TestReturnRefusesWhileTheGoblinOfALaterSessionRuns(t *testing.T) {
	// Arrange
	f, record := handedOff(t)
	f.openCloudPullRequest(t, record.Head, "errors.go", "package parser\n", testAccount)
	f.newSession(t)

	// Act
	returned, err := f.service.Return(t.Context(), f.meta.ID, "")

	// Assert
	if err == nil || !returned.IsHandedOff() || git(t, f.worktree, "rev-parse", "HEAD") != record.Head || f.remoteHead(t, testBranch) != record.Head || len(f.resumes) != 0 {
		t.Fatalf("return = %v, record %+v, resumes %+v; want it refused with the branch as handed off", err, returned, f.resumes)
	}
}
