// Package home resolves the CFO home directory and decides whether a
// directory is a genuine primary fleet home. A home without a state/
// directory is a dev checkout: every hook must stay inert there.
package home

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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

// InstalledMarker is the file `cfo install` writes into a home it sets up
// outside a checkout. Such a home is in no git repository, so the checkout
// test cannot vouch for it and the marker does instead. The repository never
// tracks one, so no checkout or worktree carries it.
const InstalledMarker = ".cfo-home"

// IsPrimary reports whether h is a genuine primary home: AGENTS.md present,
// state/ present, and either a plain (non-worktree) git checkout or, where
// git places the root in no repository, a home `cfo install` set up. A linked
// worktree is never primary, marker or not. It never creates anything; any
// failure to confirm is false, never an error.
func IsPrimary(h Home) bool {
	if fi, err := os.Stat(filepath.Join(h.Root, "AGENTS.md")); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	if fi, err := os.Stat(h.State); err != nil || !fi.IsDir() {
		return false
	}
	gitDir, commonDir, err := gitPaths(h.Root)
	if err != nil {
		fi, err := os.Stat(filepath.Join(h.Root, InstalledMarker))
		return err == nil && fi.Mode().IsRegular()
	}
	// gitPaths builds both from the same files, so a plain checkout's are
	// one path and a linked worktree's two; comparing them needs no symlink
	// resolution, which costs this hot path tens of milliseconds a call.
	return strings.EqualFold(gitDir, commonDir)
}

// gitPaths finds the git directory and common directory of the repository
// holding root from its files, as `git rev-parse --git-dir --git-common-dir`
// would: the nearest .git at or above root, never climbing into a directory
// GIT_CEILING_DIRECTORIES names. A .git directory (one with HEAD and objects)
// is its own common directory. A .git file names the git directory, and a
// linked worktree's git directory holds a commondir file naming the common
// one, which a plain checkout's or a submodule's lacks. IsPrimary runs in the
// CFO's hooks on every tool call they select, and starting git there cost
// each one a process. GIT_DIR is not consulted: no session running the hooks
// sets it.
func gitPaths(root string) (gitDir, commonDir string, err error) {
	ceilings := filepath.SplitList(os.Getenv("GIT_CEILING_DIRECTORIES"))
	for dir := filepath.Clean(root); ; {
		dotGit := filepath.Join(dir, ".git")
		info, statErr := os.Stat(dotGit)
		switch {
		case statErr == nil && info.IsDir() && isGitDir(dotGit):
			return dotGit, dotGit, nil
		case statErr == nil && info.Mode().IsRegular():
			return linkedGitPaths(dir, dotGit)
		}
		parent := filepath.Dir(dir)
		if parent == dir || slices.ContainsFunc(ceilings, func(ceiling string) bool { return fsx.SamePath(ceiling, parent) }) {
			return "", "", fmt.Errorf("home: %s is in no git repository", root)
		}
		dir = parent
	}
}

// isGitDir reports whether dir holds a repository, as git decides before it
// accepts a .git directory.
func isGitDir(dir string) bool {
	head, err := os.Stat(filepath.Join(dir, "HEAD"))
	if err != nil || !head.Mode().IsRegular() {
		return false
	}
	objects, err := os.Stat(filepath.Join(dir, "objects"))
	return err == nil && objects.IsDir()
}

// linkedGitPaths reads the git and common directories a .git file in dir
// leads to.
func linkedGitPaths(dir, dotGit string) (gitDir, commonDir string, err error) {
	content, err := os.ReadFile(dotGit)
	if err != nil {
		return "", "", err
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir:")
	if !ok {
		return "", "", fmt.Errorf("home: %s is not a gitdir file", dotGit)
	}
	gitDir = cleanGitPath(dir, strings.TrimSpace(target))
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("home: %s names %s, which is not a directory", dotGit, gitDir)
	}
	common, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if errors.Is(err, fs.ErrNotExist) {
		return gitDir, gitDir, nil
	}
	if err != nil {
		return "", "", err
	}
	return gitDir, cleanGitPath(gitDir, strings.TrimSpace(string(common))), nil
}

func cleanGitPath(root, p string) string {
	p = filepath.FromSlash(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p)
}
