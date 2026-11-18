package pipeline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// A branch's size is the lines it changes since its merge base, counted
// without the binary files, the files .gitattributes marks generated in
// either form, and the lock files it changes, and without what the default
// branch changed after the branch left it (#2002).
func TestBranchSizeCountsWhatTheBranchChangesSinceItsMergeBase(t *testing.T) {
	// Arrange
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "core.autocrlf=false"}, args...)...)
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, text string) {
		t.Helper()
		full := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lines := func(count int) string { return strings.Repeat("a line\n", count) }
	git("init", "-q", "-b", "main")
	write(".gitattributes", "api.pb.go linguist-generated\nschema/** linguist-generated=true\nhand.go linguist-generated=false\n")
	write("old.go", lines(40))
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	git("checkout", "-q", "-b", "feature")
	write("hand.go", lines(30))
	write("small.go", lines(5))
	write("api.pb.go", lines(500))
	write("schema/types.go", lines(400))
	write("go.sum", lines(300))
	write("web/package-lock.json", lines(200))
	write("logo.png", "\x89PNG\x00\x01\x02")
	if err := os.Remove(filepath.Join(repo, "old.go")); err != nil {
		t.Fatal(err)
	}
	write("moved.go", lines(40)+lines(2))
	git("add", "-A")
	git("commit", "-q", "-m", "feature")
	git("checkout", "-q", "main")
	write("later.go", lines(900))
	git("add", "-A")
	git("commit", "-q", "-m", "main moves on")
	main := git("rev-parse", "HEAD")
	git("checkout", "-q", "feature")

	// Act
	size, err := BranchSizeOf(context.Background(), execx.OSRunner{}, repo, main)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []ChangedFile{{"hand.go", 30}, {"small.go", 5}, {"moved.go", 2}}
	if size.Lines != 37 || !slices.Equal(size.Files, want) {
		t.Errorf("BranchSizeOf = %+v; want 37 lines in %+v, largest first, with the rename counted by what it changed", size, want)
	}
}

// A count git cannot give refuses rather than reads as a small branch.
func TestBranchSizeRefusesABaseItCannotRead(t *testing.T) {
	// Arrange
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	// Act
	_, err := BranchSizeOf(context.Background(), execx.OSRunner{}, repo, "0123456789abcdef0123456789abcdef01234567")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "size is unknown and no run started") {
		t.Errorf("BranchSizeOf with no merge base = %v; want a refusal that says the size is unknown", err)
	}
}
