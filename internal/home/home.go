// Package home resolves the CFO home directory and decides whether a
// directory is a genuine primary fleet home. A home without a state/
// directory is a dev checkout: every hook must stay inert there.
package home

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Home names the three directories everything else keys on.
type Home struct {
	Root  string
	State string
	Data  string
}

// Resolve returns the home from CFO_HOME or the working directory.
// It never creates directories.
func Resolve() (Home, error) {
	root := os.Getenv("CFO_HOME")
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return Home{}, err
		}
		root = wd
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Home{}, err
	}
	h := Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	if s := os.Getenv("CFO_STATE_OVERRIDE"); s != "" {
		h.State = s
	}
	return h, nil
}

// IsPrimary reports whether h is a genuine primary home: AGENTS.md present,
// state/ present, and a plain (non-worktree) git checkout. It never creates
// anything; any failure to confirm is false, never an error.
func IsPrimary(h Home) bool {
	if fi, err := os.Stat(filepath.Join(h.Root, "AGENTS.md")); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	if fi, err := os.Stat(h.State); err != nil || !fi.IsDir() {
		return false
	}
	gitDir, commonDir, err := gitPaths(h.Root)
	if err != nil {
		return false
	}
	return fsx.SamePath(gitDir, commonDir)
}

// gitPaths reads --git-dir and --git-common-dir from a single `git rev-parse`
// spawn instead of two: git prints one path per line, in the order the flags
// were given, so the two lines are read positionally rather than by a second
// invocation. Blank lines (a trailing newline, or a stray CRLF remnant) are
// dropped before positional assignment, so the parser accepts LF and CRLF
// output equally.
func gitPaths(root string) (gitDir, commonDir string, err error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--git-dir", "--git-common-dir").Output()
	if err != nil {
		return "", "", err
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 2 {
		return "", "", fmt.Errorf("home: expected 2 lines from git rev-parse --git-dir --git-common-dir, got %d", len(lines))
	}
	return cleanGitPath(root, lines[0]), cleanGitPath(root, lines[1]), nil
}

func cleanGitPath(root, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p)
}
