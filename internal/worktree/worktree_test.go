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
	acquired    [][3]string
	tops        map[string]string
	topErr      error
	returned    [][2]string
	returnErr   error
	seeded      []string
	seedErr     error
	seedValue   bool
}

func (g *gitStub) Acquire(_ context.Context, project, path, ref string) (string, error) {
	g.acquired = append(g.acquired, [3]string{project, path, ref})
	return g.acquirePath, g.acquireErr
}

func (g *gitStub) Landing(context.Context, string) (Landing, error) {
	return Landing{}, nil
}

func (g *gitStub) ArchiveTag(context.Context, string, Landing, string) (string, error) {
	return "", nil
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
	// Arrange
	root := t.TempDir()
	project := filepath.Join(root, "project")
	worktreePath := filepath.Join(root, "worktree")
	for _, dir := range []string{project, worktreePath} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git := &gitStub{acquirePath: worktreePath}
	service := Service{Git: git, Root: filepath.Join(root, "home", "worktrees")}

	// Act
	got, err := service.Acquire(context.Background(), project, "task")

	// Assert
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !fsx.SamePath(got.Path, worktreePath) {
		t.Errorf("Worktree.Path = %q, want %q", got.Path, worktreePath)
	}
	if len(git.acquired) != 1 {
		t.Fatalf("Acquire calls = %v, want 1", git.acquired)
	}
	want := filepath.Join(root, "home", "worktrees", "project", "task")
	if !fsx.SamePath(git.acquired[0][0], project) || !strings.EqualFold(git.acquired[0][1], want) || git.acquired[0][2] != "" {
		t.Errorf("Acquire call = %v, want the canonical project, %s and the default branch", git.acquired[0], want)
	}
}

func TestAddExtraPlacesItBesideTheTasksOwnUnderTheHome(t *testing.T) {
	// Arrange
	root := t.TempDir()
	project := filepath.Join(root, "app")
	extra := filepath.Join(root, "extra")
	for _, dir := range []string{project, extra} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git := &gitStub{acquirePath: extra}
	service := Service{Git: git, Root: filepath.Join(root, "worktrees")}

	// Act
	if _, err := service.AddExtra(context.Background(), project, "task", "proof", "abc123"); err != nil {
		t.Fatalf("AddExtra: %v", err)
	}

	// Assert
	want := filepath.Join(root, "worktrees", "app", "task-proof")
	if len(git.acquired) != 1 || !strings.EqualFold(git.acquired[0][1], want) || git.acquired[0][2] != "abc123" {
		t.Fatalf("Acquire calls = %v, want one at %s on abc123", git.acquired, want)
	}
	for _, name := range []string{"", "..", "a/b", `a\b`, "has space", strings.Repeat("x", 33)} {
		if _, err := service.AddExtra(context.Background(), project, "task", name, ""); err == nil {
			t.Errorf("AddExtra(%q) = nil, want the name refused", name)
		}
	}
}

func TestAcquireRefusesAMissingWorktreesRoot(t *testing.T) {
	_, err := (Service{Git: &gitStub{}}).Acquire(context.Background(), t.TempDir(), "task")
	if err == nil || !strings.Contains(err.Error(), "worktrees folder") {
		t.Fatalf("Acquire error = %v, want the missing root named", err)
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
			service := Service{Git: test.git, Root: t.TempDir()}
			_, err := service.Acquire(context.Background(), project, "task")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Acquire error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestAcquireRequiresGitOrCommandRunner(t *testing.T) {
	_, err := (Service{Root: t.TempDir()}).Acquire(context.Background(), t.TempDir(), "task")
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
