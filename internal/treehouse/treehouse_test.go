package treehouse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

func leaseReply(path string) scriptedResult {
	return scriptedResult{result: execx.Result{Stdout: []byte(`{"path":` + quote(path) + `,"lease_id":"abc123","lease_holder":"fm-task"}`)}}
}

func quote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func TestAcquireLeasesWorktreeThroughNonInteractiveTreehouseGet(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	worktree := filepath.Join(root, "worktree")
	for _, dir := range []string{project, worktree} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	runner := &scriptedRunner{results: []scriptedResult{leaseReply(worktree)}}
	service := Service{Commands: runner}

	got, err := service.Acquire(context.Background(), project, "fm-task")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !fsx.SamePath(got.Path, worktree) {
		t.Errorf("Worktree.Path = %q, want %q", got.Path, worktree)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(runner.calls))
	}
	call := runner.calls[0]
	if call.Name != "treehouse" {
		t.Errorf("call.Name = %q, want treehouse", call.Name)
	}
	if !fsx.SamePath(call.Dir, project) {
		t.Errorf("call.Dir = %q, want %q", call.Dir, project)
	}
	wantArgs := []string{"get", "--lease", "--json", "--lease-holder", "fm-task"}
	if strings.Join(call.Args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("call.Args = %q, want %q", call.Args, wantArgs)
	}
}

func TestAcquireOmitsLeaseHolderWhenEmpty(t *testing.T) {
	project := t.TempDir()
	worktree := t.TempDir()
	runner := &scriptedRunner{results: []scriptedResult{leaseReply(worktree)}}
	service := Service{Commands: runner}

	if _, err := service.Acquire(context.Background(), project, ""); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	wantArgs := []string{"get", "--lease", "--json"}
	if strings.Join(runner.calls[0].Args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("call.Args = %q, want %q", runner.calls[0].Args, wantArgs)
	}
}

func TestAcquireRejectsLeaseFailuresAndMalformedResponses(t *testing.T) {
	project := t.TempDir()
	worktree := t.TempDir()
	tests := []struct {
		name    string
		results []scriptedResult
		want    string
	}{
		{
			name:    "treehouse exits nonzero",
			results: []scriptedResult{{result: execx.Result{ExitCode: 1, Stderr: []byte("pool is empty")}}},
			want:    "pool is empty",
		},
		{
			name:    "runner error",
			results: []scriptedResult{{err: errors.New("executable not found")}},
			want:    "executable not found",
		},
		{
			name:    "malformed JSON",
			results: []scriptedResult{{result: execx.Result{Stdout: []byte("not json")}}},
			want:    "decode lease response",
		},
		{
			name:    "missing lease identity",
			results: []scriptedResult{{result: execx.Result{Stdout: []byte(`{"path":"` + strings.ReplaceAll(worktree, `\`, `\\`) + `"}`)}}},
			want:    "missing path or lease_id",
		},
		{
			name:    "primary checkout leased",
			results: []scriptedResult{leaseReply(project)},
			want:    "is the primary project",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &scriptedRunner{results: test.results}
			service := Service{Commands: runner}
			_, err := service.Acquire(context.Background(), project, "fm-task")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Acquire error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestAcquireRequiresCommandRunner(t *testing.T) {
	_, err := (Service{}).Acquire(context.Background(), t.TempDir(), "fm-task")
	if err == nil || !strings.Contains(err.Error(), "command runner is required") {
		t.Fatalf("Acquire error = %v, want command runner diagnostic", err)
	}
}

type gitStub struct {
	tops       map[string]string
	topErr     error
	freshened  []string
	returned   [][2]string
	freshenErr error
	returnErr  error
}

func (g *gitStub) WorktreeTop(_ context.Context, dir string) (string, error) {
	if g.topErr != nil {
		return "", g.topErr
	}
	return g.tops[dir], nil
}

func (g *gitStub) FetchAndFreshen(_ context.Context, dir string) error {
	g.freshened = append(g.freshened, dir)
	return g.freshenErr
}

func (g *gitStub) Return(_ context.Context, project, worktree string) error {
	g.returned = append(g.returned, [2]string{project, worktree})
	return g.returnErr
}

func TestValidateRejectsNonIsolatedDirectories(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	worktree := filepath.Join(root, "worktree")
	subdir := filepath.Join(worktree, "subdir")
	nonGit := filepath.Join(root, "not-git")
	for _, dir := range []string{project, subdir, nonGit} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name      string
		worktree  string
		git       *gitStub
		wantError bool
	}{
		{name: "subdirectory", worktree: subdir, git: &gitStub{tops: map[string]string{subdir: worktree}}, wantError: true},
		{name: "primary checkout", worktree: project, git: &gitStub{tops: map[string]string{project: project}}, wantError: true},
		{name: "non Git directory", worktree: nonGit, git: &gitStub{topErr: errors.New("not a git repository")}, wantError: true},
		{name: "isolated worktree", worktree: worktree, git: &gitStub{tops: map[string]string{worktree: worktree}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(context.Background(), test.git, project, test.worktree)
			if (err != nil) != test.wantError {
				t.Fatalf("Validate error = %v, want error = %t", err, test.wantError)
			}
		})
	}
}

func TestServiceDelegatesFreshenAndReturnToGit(t *testing.T) {
	git := &gitStub{}
	service := Service{Git: git}
	project := filepath.Join(t.TempDir(), "project")
	worktree := filepath.Join(t.TempDir(), "worktree")

	if err := service.Freshen(context.Background(), worktree); err != nil {
		t.Fatalf("Freshen: %v", err)
	}
	if err := service.Return(context.Background(), project, worktree); err != nil {
		t.Fatalf("Return: %v", err)
	}
	if len(git.freshened) != 1 || git.freshened[0] != worktree {
		t.Errorf("Freshen calls = %q, want %q", git.freshened, []string{worktree})
	}
	if len(git.returned) != 1 || git.returned[0] != [2]string{project, worktree} {
		t.Errorf("Return calls = %q, want project and worktree", git.returned)
	}
}
