// Package home resolves the CFO home directory, names the folders inside it,
// and decides whether a directory is a genuine primary fleet home. Every
// install, the desktop installer and `cfo install` alike, uses one per-user
// home, %LOCALAPPDATA%\CodeGoblins, and a source checkout is never one:
// every hook stays inert in a folder that is not a home.
package home

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Home names the three directories everything else keys on, and the folder
// on a Dev Drive its heavy folders moved to, if they did.
type Home struct {
	Root  string
	State string
	Data  string
	// DevDrive is the folder on a Dev Drive that holds the home's
	// worktrees, scratch and caches, which config\dev-drive.json names;
	// empty while they live in Root, as on every machine without one.
	DevDrive string
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

// Worktrees is the folder new goblin worktrees go to.
func (h Home) Worktrees() string { return filepath.Join(h.heavy(), WorktreesDir) }

// Scratch is the folder new tasks' scratch folders go to.
func (h Home) Scratch() string { return filepath.Join(h.heavy(), ScratchDir) }

// Caches is the shared package-cache root.
func (h Home) Caches() string { return filepath.Join(h.heavy(), CachesDir) }

// WorktreeRoots are every folder a goblin worktree of this home can be in:
// the one new worktrees go to, then, once they moved to a Dev Drive, the
// home's own, where a task made before the move keeps its worktree until it
// ends. Anything that finds a task from a path, or checks a task's recorded
// path before acting on it, checks them all.
func (h Home) WorktreeRoots() []string { return h.roots(WorktreesDir) }

// ScratchRoots are every folder a task's scratch folder can be in, as
// WorktreeRoots are for worktrees.
func (h Home) ScratchRoots() []string { return h.roots(ScratchDir) }

// OwnWorktrees are every place spawn can have put task id's own worktree of
// project: <root>\<project folder>\<id> under each worktree root, then
// <project>\.worktrees\gb-<id>, where an older build put it. A task record
// naming any other folder is not to be trusted as that task's worktree.
func (h Home) OwnWorktrees(project, id string) []string {
	var own []string
	for _, root := range h.WorktreeRoots() {
		own = append(own, filepath.Join(root, filepath.Base(filepath.Clean(project)), id))
	}
	return append(own, filepath.Join(project, LegacyWorktreesDir, LegacyWorktreePrefix+id))
}

func (h Home) heavy() string {
	if h.DevDrive != "" {
		return h.DevDrive
	}
	return h.Root
}

func (h Home) roots(folder string) []string {
	roots := []string{filepath.Join(h.heavy(), folder)}
	if h.DevDrive != "" {
		roots = append(roots, filepath.Join(h.Root, folder))
	}
	return roots
}

// DevDriveFile is the file in the home's config folder that moves its
// worktrees, scratch and caches to a folder on a Dev Drive. Without it they
// live in the home.
const DevDriveFile = "dev-drive.json"

// DevDriveConfig is config\dev-drive.json. A reader keeps the keys it knows
// and passes over the rest, so a build from before a key was added still
// reads a file a later build wrote.
type DevDriveConfig struct {
	// Root is the folder on the Dev Drive the heavy folders moved to, empty
	// until they did.
	Root string `json:"root,omitempty"`
	// MovedAt is when they moved: a goblin's terminal started before it
	// still builds against the home's own caches.
	MovedAt time.Time `json:"moved_at,omitzero"`
	// Choice is the person's answer to the offer of a Dev Drive:
	// DevDriveWanted or DevDriveDeclined, empty while nobody answered.
	Choice string `json:"choice,omitempty"`
	// AskedAt is when the person last asked for the next step: set up, try
	// again, attach.
	AskedAt time.Time `json:"asked_at,omitzero"`
	// Offered is when the Command Center item for each step was last made,
	// so one ask makes one item per step.
	Offered map[string]time.Time `json:"offered,omitempty"`
	// VHD is the file the create step made the Dev Drive in, which the
	// attach step attaches again.
	VHD string `json:"vhd,omitempty"`
}

// The answers to the offer of a Dev Drive.
const (
	DevDriveWanted   = "wanted"
	DevDriveDeclined = "declined"
)

// AnswerDevDrive records the person's answer to the offer of a Dev Drive in
// root's config\dev-drive.json, from the setup or the board: wanted, asked at
// now, so the board makes the next step's Command Center item, or declined.
func AnswerDevDrive(root string, want bool, now time.Time) error {
	config, err := ReadDevDriveConfig(root)
	if err != nil {
		return err
	}
	config.Choice = DevDriveDeclined
	if want {
		config.Choice, config.AskedAt = DevDriveWanted, now
	}
	return WriteDevDriveConfig(root, config)
}

// WriteDevDriveConfig writes root's config\dev-drive.json.
func WriteDevDriveConfig(root string, config DevDriveConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(DevDriveConfigPath(root)), 0o755); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(DevDriveConfigPath(root), append(data, '\n'))
}

// DevDriveConfigPath is root's config\dev-drive.json.
func DevDriveConfigPath(root string) string {
	return filepath.Join(root, "config", DevDriveFile)
}

// ReadDevDriveConfig reads root's config\dev-drive.json; a missing file is
// the zero config. A file that does not mean one JSON object naming an
// absolute folder is refused rather than read as "no Dev Drive", which would
// put new worktrees back in the home while the old ones sit on the drive.
func ReadDevDriveConfig(root string) (DevDriveConfig, error) {
	path := DevDriveConfigPath(root)
	data, err := fsx.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return DevDriveConfig{}, nil
	}
	if err != nil {
		return DevDriveConfig{}, err
	}
	var config DevDriveConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&config); err != nil {
		return DevDriveConfig{}, fmt.Errorf("home: %s: %w", path, err)
	}
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return DevDriveConfig{}, fmt.Errorf("home: %s must hold one JSON object", path)
	}
	if config.Root == "" {
		return config, nil
	}
	if !filepath.IsAbs(config.Root) {
		return DevDriveConfig{}, fmt.Errorf("home: %s names %q, which is not an absolute folder", path, config.Root)
	}
	config.Root = filepath.Clean(config.Root)
	return config, nil
}

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
	config, err := ReadDevDriveConfig(root)
	if err != nil {
		return Home{}, err
	}
	h := Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data"), DevDrive: config.Root}
	if s := os.Getenv("CFO_STATE_OVERRIDE"); s != "" {
		h.State = s
	}
	return h, nil
}

// InstalledMarker is the file `cfo install` writes into every home it sets
// up, and what makes a folder a home: a source checkout holds AGENTS.md and
// may hold a state folder a test or an older build left, and is never one
// unless it is the home in use, as a checkout an older build made the home
// is, which install marks. The repository ignores the file, so such a
// checkout carries it untracked and none of its worktrees carries it.
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
// or one of its parents, in either layout: <root>\<project>\<name> under any
// of worktreesRoots, the home's WorktreeRoots, or
// <checkout>\.worktrees\gb-<name> where an older build put it. A path inside
// none is no fleet worktree, so a project checkout or an unrelated directory
// never gets a task attributed to it.
func LocateWorktree(worktreesRoots []string, dir string) (WorktreePlace, bool) {
	cleaned := strings.TrimRight(filepath.Clean(dir), `\/`)
	for _, worktreesRoot := range worktreesRoots {
		if worktreesRoot == "" {
			continue
		}
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
