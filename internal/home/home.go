// Package home resolves the CFO home directory, names the folders inside it,
// and decides whether a directory is a genuine primary fleet home. Every
// install, the desktop installer and `cfo install` alike, uses one per-user
// home, %LOCALAPPDATA%\CodeGoblins, and a source checkout is never one:
// every hook stays inert in a folder that is not a home.
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

// The folders a home holds beside state\ and data\, the one layout every
// install shares, so a CFO or goblin in any harness on any machine finds
// things in the same place.
const (
	// BinDir holds the installed binary under both its names, the desktop
	// window, and the previous builds an update keeps.
	BinDir = "bin"
	// WorktreesDir holds every goblin worktree, one folder per project named
	// for its checkout's folder, then one per task.
	WorktreesDir = "worktrees"
	// ScratchDir holds one disposable folder per task, which its pane's
	// TEMP, TMP and GOTMPDIR name and which goes with the task.
	ScratchDir = "scratch"
	// CachesDir is the shared package-cache root every goblin builds
	// against.
	CachesDir = "caches"
)

// Bin is the folder holding the home's installed binaries.
func (h Home) Bin() string { return filepath.Join(h.Root, BinDir) }

// Worktrees is the folder holding every goblin worktree.
func (h Home) Worktrees() string { return filepath.Join(h.Root, WorktreesDir) }

// Scratch is the folder holding every task's scratch folder.
func (h Home) Scratch() string { return filepath.Join(h.Root, ScratchDir) }

// Caches is the shared package-cache root.
func (h Home) Caches() string { return filepath.Join(h.Root, CachesDir) }

// DefaultRoot is the per-user home every install uses,
// %LOCALAPPDATA%\CodeGoblins. CFO_HOME overrides it, for a test or a
// deliberate choice.
func DefaultRoot() (string, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return "", errors.New("home: LOCALAPPDATA is not set, so there is no per-user folder for a CFO home; set CFO_HOME")
	}
	return filepath.Abs(filepath.Join(local, "CodeGoblins"))
}

// inheritedRoot and inheritedState capture the fleet home the process was
// launched with, before any test can change it. Every goblin pane exports
// CFO_HOME and CFO_STATE_OVERRIDE so `cfo` works from a worktree, and `go
// test` inherits both. inheritedDefault is the per-user home the launch
// environment names, which a test that clears CFO_HOME would otherwise
// resolve to.
var inheritedRoot, inheritedState = os.Getenv("CFO_HOME"), os.Getenv("CFO_STATE_OVERRIDE")

var inheritedDefault, _ = DefaultRoot()

// Resolve returns the home CFO_HOME names, or the per-user home when it is
// unset. It never creates directories.
//
// A test binary is refused the fleet home it inherited. A test that resolves
// it writes its status lines and wake records into the running fleet, which
// is not hypothetical: it has happened twice, and the second time a test's PR
// notification arrived in the CFO's queue as a real goblin report. Per-package
// TestMain guards only protect packages that remember to add one, so the
// refusal lives here, where every caller passes. A test that points CFO_HOME
// at its own directory is unaffected, including one that builds a home which
// looks primary. A test that clears CFO_HOME is refused the per-user home the
// machine's own environment names, which on an installed machine is the
// running fleet; one that points LOCALAPPDATA at its own directory resolves a
// per-user home there.
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
//
// The per-user home is also compared by its spelling, because it need not
// exist yet: a test that resolves it on a machine with no install would
// create the real one by writing into it.
func usesTheInheritedFleet(h Home) bool {
	if inheritedRoot != "" && fsx.SamePath(h.Root, inheritedRoot) {
		return true
	}
	if inheritedDefault != "" && (fsx.SamePath(h.Root, inheritedDefault) || strings.EqualFold(filepath.Clean(h.Root), filepath.Clean(inheritedDefault))) {
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
		var err error
		if root, err = DefaultRoot(); err != nil {
			return Home{}, err
		}
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

// InstalledMarker is the file `cfo install` writes into every home it sets
// up, and what makes a folder a home: a source checkout holds AGENTS.md and
// may hold a state folder a test or an older build left, and is never one.
// The repository never tracks one, so no checkout or worktree carries it.
const InstalledMarker = ".cfo-home"

// IsPrimary reports whether h is a genuine primary home: AGENTS.md present,
// state/ present, and the marker `cfo install` writes. A home inside a linked
// git worktree is never primary, marker or not. It never creates anything;
// any failure to confirm is false, never an error.
func IsPrimary(h Home) bool {
	if fi, err := os.Stat(filepath.Join(h.Root, "AGENTS.md")); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	if fi, err := os.Stat(h.State); err != nil || !fi.IsDir() {
		return false
	}
	if fi, err := os.Stat(filepath.Join(h.Root, InstalledMarker)); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	gitDir, commonDir, err := gitPaths(h.Root)
	if err != nil {
		return true
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
	content, err := fsx.ReadFile(dotGit)
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
	common, err := fsx.ReadFile(filepath.Join(gitDir, "commondir"))
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

// WorktreePlace is what a directory inside a fleet worktree says about that
// worktree.
type WorktreePlace struct {
	// Root is the worktree's own folder.
	Root string
	// Project is the name of the project folder it belongs to.
	Project string
	// Name is its folder's name without a prefix: a task id, or a task id
	// and the name of an extra worktree after a dash.
	Name string
}

// LegacyWorktreesDir and LegacyWorktreePrefix are where an older build put a
// goblin's worktree, <checkout>\.worktrees\gb-<task id>; a task it spawned
// keeps working there.
const (
	LegacyWorktreesDir   = ".worktrees"
	LegacyWorktreePrefix = "gb-"
)

// LocateWorktree finds the fleet worktree dir is inside, the directory itself
// or one of its parents, in either layout: <worktreesRoot>\<project>\<name>
// in the home, or <checkout>\.worktrees\gb-<name> where an older build put
// it. A path inside neither is no fleet worktree, so a project checkout or an
// unrelated directory never gets a task attributed to it.
func LocateWorktree(worktreesRoot, dir string) (WorktreePlace, bool) {
	cleaned := strings.TrimRight(filepath.Clean(dir), `\/`)
	if worktreesRoot != "" {
		root := strings.TrimRight(filepath.Clean(worktreesRoot), `\/`)
		if rel, err := filepath.Rel(root, cleaned); err == nil && filepath.IsLocal(rel) {
			if parts := strings.Split(rel, string(filepath.Separator)); len(parts) >= 2 {
				return WorktreePlace{Root: filepath.Join(root, parts[0], parts[1]), Project: parts[0], Name: parts[1]}, true
			}
		}
	}
	for cleaned != "" {
		parent := filepath.Dir(cleaned)
		if strings.EqualFold(filepath.Base(parent), LegacyWorktreesDir) {
			name := filepath.Base(cleaned)
			if len(name) <= len(LegacyWorktreePrefix) || !strings.EqualFold(name[:len(LegacyWorktreePrefix)], LegacyWorktreePrefix) {
				return WorktreePlace{}, false
			}
			return WorktreePlace{Root: cleaned, Project: filepath.Base(filepath.Dir(parent)), Name: name[len(LegacyWorktreePrefix):]}, true
		}
		if parent == cleaned {
			break
		}
		cleaned = parent
	}
	return WorktreePlace{}, false
}
