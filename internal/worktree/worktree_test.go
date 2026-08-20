package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

type gitStub struct {
	acquirePath string
	acquireErr  error
	acquired    [][2]string
	tops        map[string]string
	topErr      error
	returned    [][2]string
	returnErr   error
	seeded      []string
	seedErr     error
	seedValue   bool
}

func (g *gitStub) Acquire(_ context.Context, project, holder string) (string, error) {
	g.acquired = append(g.acquired, [2]string{project, holder})
	return g.acquirePath, g.acquireErr
}

func (g *gitStub) WorktreeTop(_ context.Context, dir string) (string, error) {
	if g.topErr != nil {
		return "", g.topErr
	}
	return g.tops[dir], nil
}

func (g *gitStub) Return(_ context.Context, project, worktree string) error {
	g.returned = append(g.returned, [2]string{project, worktree})
	return g.returnErr
}

func (g *gitStub) EnsureSeeded(_ context.Context, project string) (bool, error) {
	g.seeded = append(g.seeded, project)
	return g.seedValue, g.seedErr
}

func TestAcquireCreatesWorktreeThroughGit(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	worktreePath := filepath.Join(root, "worktree")
	for _, dir := range []string{project, worktreePath} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	git := &gitStub{acquirePath: worktreePath}
	service := Service{Git: git}

	got, err := service.Acquire(context.Background(), project, "gb-task")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !fsx.SamePath(got.Path, worktreePath) {
		t.Errorf("Worktree.Path = %q, want %q", got.Path, worktreePath)
	}
	if len(git.acquired) != 1 {
		t.Fatalf("Acquire calls = %v, want 1", git.acquired)
	}
	if !fsx.SamePath(git.acquired[0][0], project) || git.acquired[0][1] != "gb-task" {
		t.Errorf("Acquire call = %v, want canonical project and exact holder", git.acquired[0])
	}
}

func TestAcquireRejectsFailuresAndThePrimaryCheckout(t *testing.T) {
	project := t.TempDir()
	tests := []struct {
		name string
		git  *gitStub
		want string
	}{
		{
			name: "git failure",
			git:  &gitStub{acquireErr: errors.New("worktree add refused")},
			want: "worktree add refused",
		},
		{
			name: "primary checkout acquired",
			git:  &gitStub{acquirePath: project},
			want: "is the primary project",
		},
		{
			name: "missing worktree",
			git:  &gitStub{acquirePath: filepath.Join(t.TempDir(), "gone")},
			want: "canonicalize acquired worktree",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := Service{Git: test.git}
			_, err := service.Acquire(context.Background(), project, "gb-task")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Acquire error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestAcquireRequiresGitOrCommandRunner(t *testing.T) {
	_, err := (Service{}).Acquire(context.Background(), t.TempDir(), "gb-task")
	if err == nil || !strings.Contains(err.Error(), "Git or command runner is required") {
		t.Fatalf("Acquire error = %v, want dependency diagnostic", err)
	}
}

func TestValidateRejectsNonIsolatedDirectories(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	worktreePath := filepath.Join(root, "worktree")
	subdir := filepath.Join(worktreePath, "subdir")
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
		{name: "subdirectory", worktree: subdir, git: &gitStub{tops: map[string]string{subdir: worktreePath}}, wantError: true},
		{name: "primary checkout", worktree: project, git: &gitStub{tops: map[string]string{project: project}}, wantError: true},
		{name: "non Git directory", worktree: nonGit, git: &gitStub{topErr: errors.New("not a git repository")}, wantError: true},
		{name: "isolated worktree", worktree: worktreePath, git: &gitStub{tops: map[string]string{worktreePath: worktreePath}}},
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

func TestValidateRequiresGit(t *testing.T) {
	if err := Validate(context.Background(), nil, t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("Validate returned nil without a Git dependency")
	}
}

func TestServiceDelegatesReturnToGit(t *testing.T) {
	git := &gitStub{}
	service := Service{Git: git}
	project := filepath.Join(t.TempDir(), "project")
	worktreePath := filepath.Join(t.TempDir(), "worktree")

	if err := service.Return(context.Background(), project, worktreePath); err != nil {
		t.Fatalf("Return: %v", err)
	}
	if len(git.returned) != 1 || git.returned[0] != [2]string{project, worktreePath} {
		t.Errorf("Return calls = %q, want project and worktree", git.returned)
	}
}
