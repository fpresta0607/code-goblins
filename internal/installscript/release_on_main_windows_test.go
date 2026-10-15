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
	command := execx.CommandContext(context.Background(), "git", append([]string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

// A release is built only from a tag that is a version, on a commit main
// holds that is newer than the last release's, under a higher version: a
// tag of any other shape, as one that would run as script, a commit on a
// branch main does not hold, an old commit, the last release's own, a
// version no higher than the last, and a commit git cannot find are each
// refused, never taken for a pass.
func TestReleaseRefusesATagOffMain(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	git(t, dir, "init", "--quiet", "--initial-branch=main")
	git(t, dir, "commit", "--quiet", "--allow-empty", "-m", "before the last release")
	old := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "commit", "--quiet", "--allow-empty", "-m", "the last release")
	released := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "tag", "-a", "v0.1.0", "-m", "the last release")
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
		tag, commit string
		isOK        bool
		want        string
	}{
		"a new version on main":           {tag: "v0.2.0", commit: onMain, isOK: true, want: "on main and newer than the last release, v0.1.0"},
		"a tag that would run as script":  {tag: "v0.2.0;Write-Host x;#", commit: onMain, want: "is not a version"},
		"a tag of no version":             {tag: "v0.2", commit: onMain, want: "is not a version"},
		"a commit off main":               {tag: "v0.2.0", commit: offMain, want: "is not on main, so nothing is built"},
		"a commit older than the release": {tag: "v0.2.0", commit: old, want: "is older than the last release, v0.1.0"},
		"the last release's commit":       {tag: "v0.2.0", commit: released, want: "is the last release's, v0.1.0"},
		"a version no higher":             {tag: "v0.0.9", commit: onMain, want: "is no higher than the last release, v0.1.0"},
		"a commit git lacks":              {tag: "v0.2.0", commit: strings.Repeat("1", 40), want: "could not say whether"},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			command := execx.CommandContext(context.Background(), shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", guard, "-Tag", row.tag, "-Commit", row.commit, "-Main", "main")
			command.Dir = dir
			output, err := command.CombinedOutput()

			// Assert
			if (err == nil) != row.isOK || !strings.Contains(string(output), row.want) {
				t.Errorf("the guard = %v with output %q; want ok %v and %q", err, output, row.isOK, row.want)
			}
		})
	}
}
