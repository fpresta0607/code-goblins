package pipeline

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestOriginDefaultHeadWithUnrelatedHEADNamedBranch(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	project := filepath.Join(root, "project")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(root, "init", "--bare", "--initial-branch=main", origin)
	git(root, "init", "--initial-branch=main", project)
	git(project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	head := git(project, "rev-parse", "HEAD")
	git(project, "remote", "add", "origin", origin)
	git(project, "push", "origin", "refs/heads/main", head+":refs/heads/HEAD")
	reader := Reader{Commands: execx.OSRunner{}}
	got, err := reader.originDefaultHead(context.Background(), project, "main")
	if err != nil || got != head {
		t.Fatalf("unrelated HEAD-named branch disabled gate: %s %v", got, err)
	}
	if _, err := reader.originDefaultHead(context.Background(), project, "other"); err == nil {
		t.Fatal("mismatched registered default branch accepted")
	}
}
