package installscript

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// git runs git in dir and returns what it printed, trimmed.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := execx.CommandContext(context.Background(), "git", append([]string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

// A release is built only from a commit main holds: a tag on a branch main
// does not hold is refused, and so is a commit git cannot find, never taken
// for a pass.
func TestReleaseRefusesATagOffMain(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	git(t, dir, "init", "--quiet", "--initial-branch=main")
	git(t, dir, "commit", "--quiet", "--allow-empty", "-m", "on main")
	onMain := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "--quiet", "-b", "side")
	git(t, dir, "commit", "--quiet", "--allow-empty", "-m", "off main")
	offMain := git(t, dir, "rev-parse", "HEAD")
	guard, err := filepath.Abs(filepath.Join("..", "..", "tools", "release-on-main.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	for name, row := range map[string]struct {
		commit string
		isOK   bool
		want   string
	}{
		"a commit on main":   {commit: onMain, isOK: true, want: "is on main"},
		"a commit off main":  {commit: offMain, want: "is not on main, so nothing is built"},
		"a commit git lacks": {commit: strings.Repeat("1", 40), want: "could not say whether"},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			command := execx.CommandContext(context.Background(), shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", guard, "-Commit", row.commit, "-Main", "main")
			command.Dir = dir
			output, err := command.CombinedOutput()

			// Assert
			if (err == nil) != row.isOK || !strings.Contains(string(output), row.want) {
				t.Errorf("the guard = %v with output %q; want ok %v and %q", err, output, row.isOK, row.want)
			}
		})
	}
}
