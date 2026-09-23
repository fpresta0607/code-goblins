package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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

// A task can plant an innocently named link to a file outside its worktree.
// The preview refuses every link on the path instead of following it.
func TestPreviewRefusesLinksOutOfTheWorktree(t *testing.T) {
	dir := gitFixture(t)
	outside := t.TempDir()
	const secret = "outside the worktree\n"
	if err := os.WriteFile(filepath.Join(outside, "notes.txt"), []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	g, ctx := Git{}, context.Background()
	for _, path := range []string{"plain.txt", "nested/plain.txt"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), []byte("inside\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if diff, err := g.Diff(ctx, dir, "", path); err != nil || diff.Code != "inside\n" {
			t.Fatalf("regular file preview %s: %+v %v", path, diff, err)
		}
	}
	for _, c := range []struct {
		name, path string
		listed     bool // Git itself offers the linked path as untracked task work.
		link       func() error
	}{
		{"file symlink", "notes.txt", true, func() error {
			return os.Symlink(filepath.Join(outside, "notes.txt"), filepath.Join(dir, "notes.txt"))
		}},
		{"parent directory symlink", "linked/notes.txt", false, func() error {
			return os.Symlink(outside, filepath.Join(dir, "linked"))
		}},
		{"parent directory junction", "junction/notes.txt", true, func() error {
			if runtime.GOOS != "windows" {
				return errors.New("junctions exist only on Windows")
			}
			if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(dir, "junction"), outside).CombinedOutput(); err != nil {
				return fmt.Errorf("%v: %s", err, out)
			}
			return nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := c.link(); err != nil {
				t.Skipf("this link cannot be created here: %v", err)
			}
			if data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(c.path))); err != nil || string(data) != secret {
				t.Fatalf("premise: the link does not reach the outside file: %q %v", data, err)
			}
			files, err := g.Files(ctx, dir, "")
			if err != nil {
				t.Fatal(err)
			}
			if c.listed && !slices.ContainsFunc(files, func(f ChangedFile) bool { return f.Path == c.path }) {
				t.Fatalf("premise: the change set does not offer %s: %+v", c.path, files)
			}
			if code, err := readPreview(dir, c.path); err == nil || code != "" {
				t.Fatalf("preview followed the link: %q %v", code, err)
			}
			if diff, err := g.Diff(ctx, dir, "", c.path); err == nil || strings.Contains(diff.Code+diff.Patch, "outside") {
				t.Fatalf("public diff followed the link: %+v %v", diff, err)
			}
		})
	}
}

// A zero-byte untracked file has no line 1, while a lone newline is one
// empty line, so only the latter may yield a selectable range.
func TestUntrackedZeroByteFileHasNoSelectableLine(t *testing.T) {
	dir := gitFixture(t)
	g, ctx := Git{}, context.Background()
	for _, c := range []struct {
		path, content, lineOne string
		hasLine                bool
	}{
		{"empty.txt", "", "", false},
		{"newline.txt", "\n", "\n", true},
		{"text.txt", "first\nsecond\n", "first\n", true},
	} {
		if err := os.WriteFile(filepath.Join(dir, c.path), []byte(c.content), 0600); err != nil {
			t.Fatal(err)
		}
		diff, err := g.Diff(ctx, dir, "", c.path)
		if err != nil {
			t.Fatal(err)
		}
		code, err := diffRangeContext(diff.Patch, 1, 1, "new")
		if c.hasLine != (err == nil) || code != c.lineOne {
			t.Errorf("%s: line 1 = %q, %v; patch %q", c.path, code, err, diff.Patch)
		}
	}
}
