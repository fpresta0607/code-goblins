package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func gitFixture(t *testing.T, destination ...string) string {
	t.Helper()
	dir := t.TempDir()
	if len(destination) > 0 {
		dir = destination[0]
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.invalid"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
	}
	for index, content := range []string{"package main\nfunc main() {}\n", "package main\nfunc main() { println(1) }\n"} {
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "main.go"}, {"commit", "-qm", "Test commit"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v", out, err)
			}
		}
		if index == 0 {
			for _, args := range [][]string{{"update-ref", "refs/remotes/origin/main", "HEAD"}, {"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"}} {
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("base: %s %v", out, err)
				}
			}
		}
	}
	return dir
}

func TestFullTaskDiffDoesNotGuessBase(t *testing.T) {
	dir := gitFixture(t)
	cmd := exec.Command("git", "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := (Git{}).Files(context.Background(), dir, ""); err == nil {
		t.Fatal("missing base silently omitted earlier commits")
	}
}

func TestRetainedTaskBaseSurvivesRemoteAdvance(t *testing.T) {
	s, h := testStore(t)
	dir := gitFixture(t, filepath.Join(h.Root, "work"))
	ctx := context.Background()
	base, err := (Git{}).base(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Tasks["task-1"] = Evaluation{Base: base, Generation: "g1"}
	service := &Service{Store: s}
	for _, args := range [][]string{{"update-ref", "refs/remotes/origin/main", "HEAD"}, {"symbolic-ref", "--delete", "refs/remotes/origin/HEAD"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
	}
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	diff, err := service.previewGit(meta).Diff(ctx, dir, "", "main.go")
	if err != nil || !strings.Contains(diff.Patch, "+func main() { println(1) }") {
		t.Fatalf("retained full task diff disappeared: %+v %v", diff, err)
	}
}

func TestGitDiffHistoryAndSafeFilePreview(t *testing.T) {
	dir := gitFixture(t)
	g := Git{}
	ctx := context.Background()
	files, err := g.Files(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "main.go" {
		t.Fatalf("files: %+v", files)
	}
	diff, err := g.Diff(ctx, dir, "", "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Patch, "+func main() { println(1) }") || !strings.Contains(diff.Code, "println") {
		t.Fatalf("diff: %+v", diff)
	}
	history, err := g.History(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history: %+v", history)
	}
	if _, err := g.Diff(ctx, dir, history[0].SHA, "main.go"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside", ".git/config", ".env", "sub/.env.local", "auth.ps1", "C:/windows/win.ini"} {
		if _, err := g.Diff(ctx, dir, "", path); err == nil {
			t.Fatalf("accepted unsafe file %s", path)
		}
	}
	if _, err := g.Files(ctx, dir, "--output=bad"); err == nil {
		t.Fatal("accepted option as revision")
	}
}
