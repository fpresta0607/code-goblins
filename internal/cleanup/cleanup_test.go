package cleanup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/treehouse"
)

type cleanupRunner struct {
	gitStatus    string
	snapshotBody string
	snapshotErr  bool
	tabCloseErr  bool
	tabClosed    []string
	requests     []execx.Request
}

func (r *cleanupRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, req)
	if req.Name == "git" {
		if len(req.Args) != 3 || req.Args[0] != "status" || req.Args[1] != "--porcelain=v1" || req.Args[2] != "--untracked-files=all" {
			return execx.Result{}, fmt.Errorf("unexpected git request: %#v", req)
		}
		return execx.Result{Stdout: []byte(r.gitStatus)}, nil
	}
	if req.Name != "herdr" || len(req.Args) < 2 || req.Args[len(req.Args)-2] != "--session" {
		return execx.Result{}, fmt.Errorf("unexpected request: %#v", req)
	}
	args := req.Args[:len(req.Args)-2]
	if len(args) == 2 && args[0] == "api" && args[1] == "snapshot" {
		if r.snapshotErr {
			return execx.Result{ExitCode: 1, Stderr: []byte(`{"error":{"code":"server_unavailable"}}`)}, nil
		}
		return execx.Result{Stdout: []byte(r.snapshotBody)}, nil
	}
	if len(args) == 3 && args[0] == "tab" && args[1] == "close" {
		if r.tabCloseErr {
			return execx.Result{ExitCode: 1, Stderr: []byte("tab close refused")}, nil
		}
		r.tabClosed = append(r.tabClosed, args[2])
		return jsonResultOK(), nil
	}
	return execx.Result{}, fmt.Errorf("unexpected Herdr args: %q", args)
}

func jsonResultOK() execx.Result {
	return execx.Result{Stdout: []byte(`{"result":{"type":"ok"}}`)}
}

type cleanupGit struct {
	top       string
	returnErr error
	returned  [][2]string
}

func (g *cleanupGit) WorktreeTop(_ context.Context, dir string) (string, error) {
	return g.top, nil
}

func (g *cleanupGit) FetchAndFreshen(context.Context, string) error {
	return nil
}

func (g *cleanupGit) Return(_ context.Context, project, worktree string) error {
	g.returned = append(g.returned, [2]string{project, worktree})
	return g.returnErr
}

type cleanupFixture struct {
	stateDir string
	project  string
	worktree string
	runner   *cleanupRunner
	git      *cleanupGit
	service  Service
	meta     state.TaskMeta
}

func cleanupSnapshot(panes, agents string) string {
	return `{"result":{"type":"session_snapshot","snapshot":{"protocol":19,"workspaces":[{"workspace_id":"ws","label":"cfo"}],"tabs":[{"tab_id":"tab-g1","workspace_id":"ws","label":"gb-g1"}],"panes":[` + panes + `],"agents":[` + agents + `]}}}`
}

func newCleanupFixture(t *testing.T) *cleanupFixture {
	t.Helper()
	root := t.TempDir()
	makeCanonicalDir := func(name string) string {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		canonical, err := fsx.Canonical(path)
		if err != nil {
			t.Fatal(err)
		}
		return canonical
	}
	stateDir := makeCanonicalDir("state")
	project := makeCanonicalDir("project")
	worktree := makeCanonicalDir("worktree")

	meta := state.TaskMeta{
		ID:               "g1",
		Window:           "fleet:pane-g1",
		Worktree:         worktree,
		Project:          project,
		Harness:          "claude",
		Backend:          "herdr",
		HerdrSession:     "fleet",
		HerdrWorkspaceID: "ws",
		HerdrTabID:       "tab-g1",
		HerdrPaneID:      "pane-g1",
	}
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}

	runner := &cleanupRunner{snapshotBody: cleanupSnapshot(`{"pane_id":"pane-g1","tab_id":"tab-g1","workspace_id":"ws"}`, "")}
	git := &cleanupGit{top: worktree}
	fixture := &cleanupFixture{
		stateDir: stateDir,
		project:  project,
		worktree: worktree,
		runner:   runner,
		git:      git,
		meta:     meta,
	}
	fixture.service = Service{
		StateDir:  stateDir,
		Commands:  runner,
		Herdr:     &herdr.Client{Commands: runner, Session: "fleet"},
		Treehouse: treehouse.Service{Git: git},
	}
	return fixture
}

func (f *cleanupFixture) assertNoLifecycleRequests(t *testing.T) {
	t.Helper()
	for _, request := range f.runner.requests {
		for _, argument := range request.Args {
			switch argument {
			case "close", "delete", "remove", "restart", "send-text", "send-keys", "stop":
				t.Fatalf("cleanup issued a lifecycle command: %s %q", request.Name, request.Args)
			}
		}
	}
}

// assertOnlyTabCloseLifecycle proves the one lifecycle action a successful
// cleanup takes: closing the recorded tab after the endpoint proved inactive.
func (f *cleanupFixture) assertOnlyTabCloseLifecycle(t *testing.T) {
	t.Helper()
	if len(f.runner.tabClosed) != 1 || f.runner.tabClosed[0] != "tab-g1" {
		t.Fatalf("tab closes = %v, want exactly [tab-g1]", f.runner.tabClosed)
	}
	for _, request := range f.runner.requests {
		for _, argument := range request.Args {
			switch argument {
			case "delete", "remove", "restart", "send-text", "send-keys", "stop", "close":
				if argument == "close" && request.Name == "herdr" {
					continue
				}
				t.Fatalf("cleanup issued an unexpected lifecycle command: %s %q", request.Name, request.Args)
			}
		}
	}
}

func (f *cleanupFixture) assertMetadataPreserved(t *testing.T) {
	t.Helper()
	meta, err := state.ReadTaskMeta(f.stateDir, "g1")
	if err != nil {
		t.Fatalf("metadata lost after refused cleanup: %v", err)
	}
	if meta.Worktree == "" {
		t.Fatalf("metadata = %+v, want the task record retained for a safe retry", meta)
	}
	if len(f.git.returned) != 0 {
		t.Fatalf("treehouse return calls = %v, want none for a refused cleanup", f.git.returned)
	}
	f.assertNoLifecycleRequests(t)
}

func TestCleanupReturnsCleanInactiveWorktree(t *testing.T) {
	fixture := newCleanupFixture(t)

	result, err := fixture.service.Cleanup(context.Background(), "g1")
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	wantOutput := "cleaned g1 worktree=" + fixture.worktree
	if result.Output != wantOutput {
		t.Errorf("Output = %q, want %q", result.Output, wantOutput)
	}
	if len(fixture.git.returned) != 1 || fixture.git.returned[0] != [2]string{fixture.project, fixture.worktree} {
		t.Fatalf("treehouse return calls = %v, want one return of the exact canonical project and worktree", fixture.git.returned)
	}
	if _, err := os.Stat(filepath.Join(fixture.stateDir, "g1.meta")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata survives successful cleanup: %v", err)
	}
	status, err := state.TailStatus(fixture.stateDir, "g1", 5)
	if err != nil || len(status) != 1 || status[0] != "done: returned worktree "+fixture.worktree+" via cfo cleanup" {
		t.Fatalf("status = %v, %v; want one done line recording the returned worktree", status, err)
	}
	fixture.assertOnlyTabCloseLifecycle(t)
	if _, err := lock.AcquireExclusiveNamed(fixture.stateDir, cleanupLockName("g1")); err != nil {
		t.Fatalf("task lock still held after cleanup: %v", err)
	}
	if err := lock.ReleaseExclusiveNamed(fixture.stateDir, cleanupLockName("g1")); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupAcceptsMissingPaneAsInactiveEvidence(t *testing.T) {
	fixture := newCleanupFixture(t)
	fixture.runner.snapshotBody = cleanupSnapshot("", "")

	if _, err := fixture.service.Cleanup(context.Background(), "g1"); err != nil {
		t.Fatalf("Cleanup with a structurally missing pane: %v", err)
	}
	if len(fixture.git.returned) != 1 {
		t.Fatalf("treehouse return calls = %v, want one", fixture.git.returned)
	}
	fixture.assertOnlyTabCloseLifecycle(t)
}

func TestCleanupTabCloseFailurePreservesMetadataAndSkipsReturn(t *testing.T) {
	fixture := newCleanupFixture(t)
	fixture.runner.tabCloseErr = true

	_, err := fixture.service.Cleanup(context.Background(), "g1")
	if err == nil || !strings.Contains(err.Error(), "close task tab") {
		t.Fatalf("Cleanup error = %v, want tab close refusal", err)
	}
	if _, metaErr := state.ReadTaskMeta(fixture.stateDir, "g1"); metaErr != nil {
		t.Fatalf("metadata lost after tab close failure: %v", metaErr)
	}
	if len(fixture.git.returned) != 0 {
		t.Fatalf("treehouse return calls = %v, want none when the tab close fails", fixture.git.returned)
	}
}

func TestCleanupRefusals(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*cleanupFixture)
		want  string
	}{
		{
			name: "primary checkout",
			setup: func(f *cleanupFixture) {
				f.meta.Worktree = f.project
				if err := state.WriteTaskMeta(f.stateDir, f.meta); err != nil {
					t.Fatal(err)
				}
			},
			want: "primary checkout",
		},
		{
			name: "nested non-isolated path",
			setup: func(f *cleanupFixture) {
				f.git.top = f.project
			},
			want: "not worktree root",
		},
		{
			name: "dirty tracked file",
			setup: func(f *cleanupFixture) {
				f.runner.gitStatus = " M tracked.go\n"
			},
			want: "uncommitted or untracked changes",
		},
		{
			name: "dirty untracked file",
			setup: func(f *cleanupFixture) {
				f.runner.gitStatus = "?? notes.txt\n"
			},
			want: "uncommitted or untracked changes",
		},
		{
			name: "active agent",
			setup: func(f *cleanupFixture) {
				f.runner.snapshotBody = cleanupSnapshot(
					`{"pane_id":"pane-g1","tab_id":"tab-g1","workspace_id":"ws"}`,
					`{"pane_id":"pane-g1","tab_id":"tab-g1","workspace_id":"ws","agent":"claude","agent_status":"working"}`)
			},
			want: "still has agent",
		},
		{
			name: "done agent is still present",
			setup: func(f *cleanupFixture) {
				f.runner.snapshotBody = cleanupSnapshot(
					`{"pane_id":"pane-g1","tab_id":"tab-g1","workspace_id":"ws"}`,
					`{"pane_id":"pane-g1","tab_id":"tab-g1","workspace_id":"ws","agent":"claude","agent_status":"done"}`)
			},
			want: "still has agent",
		},
		{
			name: "mismatched endpoint identity",
			setup: func(f *cleanupFixture) {
				f.runner.snapshotBody = cleanupSnapshot(`{"pane_id":"pane-g1","tab_id":"tab-other","workspace_id":"ws"}`, "")
			},
			want: "not recorded tab",
		},
		{
			name: "duplicate endpoint identity",
			setup: func(f *cleanupFixture) {
				pane := `{"pane_id":"pane-g1","tab_id":"tab-g1","workspace_id":"ws"}`
				f.runner.snapshotBody = cleanupSnapshot(pane+","+pane, "")
			},
			want: "ambiguous",
		},
		{
			name: "unreadable Herdr evidence",
			setup: func(f *cleanupFixture) {
				f.runner.snapshotErr = true
			},
			want: "unreadable",
		},
		{
			name: "session drift",
			setup: func(f *cleanupFixture) {
				f.service.Herdr = &herdr.Client{Commands: f.runner, Session: "other"}
			},
			want: "does not match the Herdr client session",
		},
		{
			name: "incomplete metadata",
			setup: func(f *cleanupFixture) {
				if err := state.WriteMeta(filepath.Join(f.stateDir, "g1.meta"), map[string]string{
					"backend":            "herdr",
					"herdr_session":      "fleet",
					"herdr_tab_id":       "tab-g1",
					"herdr_workspace_id": "ws",
					"project":            f.project,
					"worktree":           f.worktree,
				}); err != nil {
					t.Fatal(err)
				}
			},
			want: "missing herdr_pane_id",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCleanupFixture(t)
			test.setup(fixture)

			_, err := fixture.service.Cleanup(context.Background(), "g1")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Cleanup error = %v, want %q", err, test.want)
			}
			fixture.assertMetadataPreserved(t)
		})
	}
}

func TestCleanupPreservesMetadataWhenReturnFails(t *testing.T) {
	fixture := newCleanupFixture(t)
	fixture.git.returnErr = errors.New("treehouse: ambiguous return refused")

	_, err := fixture.service.Cleanup(context.Background(), "g1")
	if err == nil || !strings.Contains(err.Error(), "ambiguous return refused") {
		t.Fatalf("Cleanup error = %v, want the treehouse return failure", err)
	}
	if len(fixture.git.returned) != 1 || fixture.git.returned[0] != [2]string{fixture.project, fixture.worktree} {
		t.Fatalf("treehouse return calls = %v, want one attempted canonical return", fixture.git.returned)
	}
	meta, readErr := state.ReadTaskMeta(fixture.stateDir, "g1")
	if readErr != nil {
		t.Fatalf("metadata lost after a failed return: %v", readErr)
	}
	if meta.Worktree != fixture.worktree {
		t.Fatalf("metadata = %+v, want the exact task retained for retry", meta)
	}
	status, statusErr := state.TailStatus(fixture.stateDir, "g1", 5)
	if statusErr != nil || len(status) != 0 {
		t.Fatalf("status = %v, %v; want no done record after a failed return", status, statusErr)
	}
}

func TestCleanupRefusesContendedTaskLock(t *testing.T) {
	fixture := newCleanupFixture(t)
	if _, err := lock.AcquireExclusiveNamed(fixture.stateDir, cleanupLockName("g1")); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.ReleaseExclusiveNamed(fixture.stateDir, cleanupLockName("g1")); err != nil {
			t.Fatal(err)
		}
	}()

	_, err := fixture.service.Cleanup(context.Background(), "g1")
	if !errors.Is(err, lock.ErrHeld) {
		t.Fatalf("Cleanup error = %v, want ErrHeld", err)
	}
	fixture.assertMetadataPreserved(t)
}

func TestCleanupRejectsInvalidIDBeforeAnyMutation(t *testing.T) {
	fixture := newCleanupFixture(t)
	if _, err := fixture.service.Cleanup(context.Background(), "../escape"); err == nil || !strings.Contains(err.Error(), "task ID") {
		t.Fatalf("Cleanup invalid ID error = %v, want task ID refusal", err)
	}
	if len(fixture.runner.requests) != 0 || len(fixture.git.returned) != 0 {
		t.Fatalf("invalid ID reached external tools: requests=%v returned=%v", fixture.runner.requests, fixture.git.returned)
	}
}
