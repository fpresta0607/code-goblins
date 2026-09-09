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

// inheritedRoot and inheritedState capture the fleet home the process was
// launched with, before any test can change it. Every goblin pane exports
// CFO_HOME and CFO_STATE_OVERRIDE so `cfo` works from a worktree, and `go
// test` inherits both.
var inheritedRoot, inheritedState = os.Getenv("CFO_HOME"), os.Getenv("CFO_STATE_OVERRIDE")

// Resolve returns the home from CFO_HOME or the working directory.
// It never creates directories.
//
// A test binary is refused the fleet home it inherited. A test that resolves
// it writes its status lines and wake records into the running fleet, which
// is not hypothetical: it has happened twice, and the second time a test's PR
// notification arrived in the CFO's queue as a real goblin report. Per-package
// TestMain guards only protect packages that remember to add one, so the
// refusal lives here, where every caller passes. A test that points CFO_HOME
// at its own directory is unaffected, including one that builds a home which
// looks primary.
func Resolve() (Home, error) {
	h, err := resolve()
	if err != nil {
		return Home{}, err
	}
	if isTestBinary() && usesTheInheritedFleet(h) {
		return Home{}, fmt.Errorf("home: a test resolved the inherited fleet home %s (state %s); point CFO_HOME and CFO_STATE_OVERRIDE at the test's own directory rather than inheriting the pane's", h.Root, h.State)
	}
	return h, nil
}

// Inherited returns the fleet home this process was launched with, captured
// before any test could change the environment. A test that hands a child
// process its own environment uses this to prove it is not about to give that
// child the running fleet; Resolve's refusal cannot help there, because the
// real cfo binary is not a test binary.
func Inherited() (root, state string) {
	return inheritedRoot, inheritedState
}

// usesTheInheritedFleet reports whether h is the home this process inherited
// rather than one the test chose. Comparing against the inherited value is
// exact where a path heuristic is not: an operator's temporary or cache
// directory can sit anywhere, a checkout included, so "under the checkout"
// would condemn correct fixtures, and a fixture there inherits the checkout's
// .git and so looks primary too.
func usesTheInheritedFleet(h Home) bool {
	if inheritedRoot != "" && fsx.SamePath(h.Root, inheritedRoot) {
		return true
	}
	return inheritedState != "" && fsx.SamePath(h.State, inheritedState)
}

// isTestBinary reports whether this process was built by `go test`, which
// names the binary <package>.test. The name is checked rather than a -test.*
// flag because a test binary can be run with no flags at all.
func isTestBinary() bool {
	name := strings.ToLower(filepath.Base(os.Args[0]))
	return strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".test.exe")
}

func resolve() (Home, error) {
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
