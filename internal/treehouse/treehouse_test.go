package treehouse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type scriptedPane struct {
	target string
	cwds   []string
	runs   []string
	reads  int
}

func (p *scriptedPane) Run(_ context.Context, text string) error {
	p.runs = append(p.runs, text)
	return nil
}

func (p *scriptedPane) ForegroundCWD(context.Context) (string, error) {
	if p.reads >= len(p.cwds) {
		return "", errors.New("unexpected foreground cwd read")
	}
	cwd := p.cwds[p.reads]
	p.reads++
	return cwd, nil
}

func (p *scriptedPane) String() string {
	return p.target
}

func noWait(ctx context.Context, _ time.Duration) error {
	return ctx.Err()
}

func TestAcquireRequiresTwoConsecutiveNonPrimaryCanonicalReads(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	stale := filepath.Join(root, "stale")
	worktree := filepath.Join(root, "worktree")
	for _, dir := range []string{project, stale, worktree} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	pane := &scriptedPane{
		target: "fleet:task-2",
		cwds:   []string{project, stale, filepath.Join(worktree, "."), worktree},
	}
	service := Service{Poll: time.Second, Timeout: 4 * time.Second, Sleep: noWait}

	got, err := service.Acquire(context.Background(), pane, project)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got.Path != worktree {
		t.Errorf("Worktree.Path = %q, want %q", got.Path, worktree)
	}
	if len(pane.runs) != 1 || pane.runs[0] != "treehouse get" {
		t.Errorf("Pane.Run calls = %q, want exactly [treehouse get]", pane.runs)
	}
	if pane.reads != 4 {
		t.Errorf("ForegroundCWD reads = %d, want 4", pane.reads)
	}
}

func TestAcquireResetsCandidateWhenPaneReturnsPrimary(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	worktree := filepath.Join(root, "worktree")
	for _, dir := range []string{project, worktree} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	pane := &scriptedPane{target: "fleet:reset", cwds: []string{worktree, project, worktree}}
	service := Service{Poll: time.Second, Timeout: 3 * time.Second, Sleep: noWait}

	_, err := service.Acquire(context.Background(), pane, project)
	if err == nil {
		t.Fatal("Acquire returned nil after primary cwd reset a single candidate read")
	}
	if !strings.Contains(err.Error(), "treehouse get did not enter a worktree within 60s") {
		t.Errorf("Acquire error = %q, want timeout diagnostic", err)
	}
}

func TestAcquireTimeoutIncludesTargetAfterOneCandidateRead(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	foreign := filepath.Join(root, "foreign")
	worktree := filepath.Join(root, "worktree")
	for _, dir := range []string{project, foreign, worktree} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	pane := &scriptedPane{target: "fleet:timeout", cwds: []string{project, foreign, worktree}}
	service := Service{Poll: time.Second, Timeout: 3 * time.Second, Sleep: noWait}

	_, err := service.Acquire(context.Background(), pane, project)
	if err == nil {
		t.Fatal("Acquire returned nil after only one candidate worktree read")
	}
	if !strings.Contains(err.Error(), "treehouse get did not enter a worktree within 60s") {
		t.Errorf("Acquire error = %q, want exact timeout diagnostic", err)
	}
	if !strings.Contains(err.Error(), "fleet:timeout") {
		t.Errorf("Acquire error = %q, want Herdr target", err)
	}
}

func TestAcquireUsesPaneStringForHerdrTarget(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}

	pane := &scriptedPane{target: "fleet:string-target", cwds: []string{project}}
	service := Service{Poll: time.Second, Timeout: time.Second, Sleep: noWait}

	_, err := service.Acquire(context.Background(), pane, project)
	if err == nil || !strings.Contains(err.Error(), "fleet:string-target") {
		t.Fatalf("Acquire error = %v, want String target", err)
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
