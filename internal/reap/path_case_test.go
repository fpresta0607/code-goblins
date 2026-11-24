package reap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// caseSpellings makes the folder parent\name, name in lowercase, and returns
// its path, the same path with name in uppercase, and whether the file system
// opens the folder by that second spelling: the oracle a case test here holds
// a comparison to, read from the file system itself.
func caseSpellings(t *testing.T, parent, name string) (string, string, bool) {
	t.Helper()
	lower := filepath.Join(parent, name)
	if err := os.MkdirAll(lower, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(parent, strings.ToUpper(name))
	_, err := os.Stat(upper)
	return lower, upper, err == nil
}

// A directory is within a folder spelled in another case exactly where the
// file system reads the two spellings as one folder, and a boundary is the
// system's own separator.
func TestPathWithinComparesByTheFileSystemsCaseAndSeparator(t *testing.T) {
	// Arrange
	lower, upper, isOnePath := caseSpellings(t, t.TempDir(), "worktree")
	cases := []struct {
		name       string
		path       string
		wantWithin bool
	}{
		{name: "the folder itself", path: lower, wantWithin: true},
		{name: "a folder inside it", path: filepath.Join(lower, "src"), wantWithin: true},
		{name: "the folder with a trailing separator", path: lower + string(filepath.Separator), wantWithin: true},
		{name: "a folder whose name it begins", path: lower + "s", wantWithin: false},
		{name: "a folder inside it in another case", path: filepath.Join(upper, "src"), wantWithin: isOnePath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isWithin := pathWithin(tc.path, lower)

			// Assert
			if isWithin != tc.wantWithin {
				t.Fatalf("pathWithin(%s, %s) = %t, want %t", tc.path, lower, isWithin, tc.wantWithin)
			}
		})
	}
}

// A command line names a directory up to a boundary, the system's own
// separator among them, so a task id that begins another is not it.
func TestNamesPathEndsAtTheSystemsSeparator(t *testing.T) {
	// Arrange
	worktree := filepath.Join(t.TempDir(), "gb-old")
	cases := []struct {
		name      string
		command   string
		wantNames bool
	}{
		{name: "a file inside it", command: "node " + filepath.Join(worktree, "server.js"), wantNames: true},
		{name: "the folder at the end", command: "node --cwd " + worktree, wantNames: true},
		{name: "the folder quoted", command: `node "` + worktree + `"`, wantNames: true},
		{name: "a task whose id it begins", command: "node " + filepath.Join(worktree+"-2", "server.js"), wantNames: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isNamed := namesPath(tc.command, worktree)

			// Assert
			if isNamed != tc.wantNames {
				t.Fatalf("namesPath(%q, %s) = %t, want %t", tc.command, worktree, isNamed, tc.wantNames)
			}
		})
	}
}

// A dev server is known by its module folder between two of the system's
// separators, and never by a folder whose name begins with the module's.
func TestIsServerFindsAModuleBetweenTheSystemsSeparators(t *testing.T) {
	// Arrange
	root := t.TempDir()
	cases := []struct {
		name         string
		command      string
		wantIsServer bool
	}{
		{name: "the module's folder", command: "node " + filepath.Join(root, "node_modules", "next", "dist", "bin", "next") + " dev", wantIsServer: true},
		{name: "a folder whose name begins with the module's", command: "node " + filepath.Join(root, "nextcloud", "sync.js"), wantIsServer: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isServerProcess := isServer(Process{CommandLine: tc.command})

			// Assert
			if isServerProcess != tc.wantIsServer {
				t.Fatalf("isServer(%q) = %t, want %t", tc.command, isServerProcess, tc.wantIsServer)
			}
		})
	}
}

// A program a goblin's Go test runs names its task by the folder it runs
// from: a scratch root's folder for the task, or an older build's
// goblins\gotmp\<fleet>\<task>, each read with the system's separator and
// the file system's case.
func TestGoTestTaskReadsTheTaskByTheFileSystemsCaseAndSeparator(t *testing.T) {
	// Arrange
	root := t.TempDir()
	scratch, upperScratch, isOnePath := caseSpellings(t, root, "scratch")
	gotmp := filepath.Join(root, "goblins", "gotmp", "fleet")
	upperGotmp := filepath.Join(root, "GOBLINS", "GOTMP", "fleet")
	cases := []struct {
		name     string
		program  string
		wantTask string
		wantOK   bool
	}{
		{name: "the scratch root as written", program: filepath.Join(scratch, "task-1", "go-build", "x.test"), wantTask: "task-1", wantOK: true},
		{name: "the scratch root in another case", program: filepath.Join(upperScratch, "task-2", "go-build", "x.test"), wantTask: "task-2", wantOK: isOnePath},
		{name: "an older build's folder as written", program: filepath.Join(gotmp, "task-3", "go-build", "x.test"), wantTask: "task-3", wantOK: true},
		{name: "an older build's folder in another case", program: filepath.Join(upperGotmp, "task-4", "go-build", "x.test"), wantTask: "task-4", wantOK: isOnePath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			task, ok := goTestTask(Process{CommandLine: tc.program + " -test.v"}, []string{scratch})

			// Assert
			if ok != tc.wantOK || (ok && task != tc.wantTask) {
				t.Fatalf("goTestTask(%s) = %q, %t; want %q, %t", tc.program, task, ok, tc.wantTask, tc.wantOK)
			}
		})
	}
}

// A Claude Code scratchpad, <temp>\claude\<slug>, belongs to the worktree
// whose path the slug spells, each character that is no ASCII letter or digit
// a hyphen, read with the system's separator.
func TestScratchpadOwnerReadsTheSlugOfTheWorktreesPath(t *testing.T) {
	// Arrange
	worktree := WorktreeDir{Path: filepath.Join(t.TempDir(), "App", "task-1")}
	slug := strings.Map(func(r rune) rune {
		if ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			return r
		}
		return '-'
	}, worktree.Path)
	dir := filepath.Join(t.TempDir(), "temp", "claude", slug, "tasks")

	// Act
	owner := scratchpadOwner(dir, []WorktreeDir{worktree})

	// Assert
	if owner != worktree.Path {
		t.Fatalf("scratchpadOwner(%s) = %s, want the worktree %s", dir, owner, worktree.Path)
	}
}

// A task's own worktree, by the id it is named for or the record that names
// it, is the task's in another case exactly where the file system reads the
// two names as one.
func TestRunsForTaskComparesTheTasksIDByTheFileSystemsCase(t *testing.T) {
	// Arrange
	_, _, isOnePath := caseSpellings(t, t.TempDir(), "probe")
	path := filepath.Join(t.TempDir(), "worktree")
	cases := []struct {
		name     string
		worktree WorktreeDir
		tasks    map[string]Task
	}{
		{name: "named for the task", worktree: WorktreeDir{Path: path, TaskID: "TASK-1"}},
		{name: "named by the task's record", worktree: WorktreeDir{Path: path}, tasks: map[string]Task{"TASK-1": {ID: "TASK-1", Meta: state.TaskMeta{ID: "TASK-1", Worktree: path}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isTasks := runsForTask(filepath.Join(path, "src"), "task-1", Inventory{Worktrees: []WorktreeDir{tc.worktree}}, tc.tasks, nil)

			// Assert
			if isTasks != isOnePath {
				t.Fatalf("runsForTask in TASK-1's worktree for task-1 = %t, want %t", isTasks, isOnePath)
			}
		})
	}
}

// An extra worktree, <id>-<suffix>, belongs to the task whose id begins its
// name in another case exactly where the file system reads the two as one.
func TestOwnerOfReadsAnExtrasIDByTheFileSystemsCase(t *testing.T) {
	// Arrange
	root, _, isOnePath := caseSpellings(t, t.TempDir(), "worktrees")
	own := WorktreeDir{Path: filepath.Join(root, "task-1"), Created: time.Now().Add(-time.Hour)}
	extra := WorktreeDir{Path: filepath.Join(root, "TASK-1-proof"), TaskID: "TASK-1-proof", Project: root, Created: time.Now()}
	tasks := map[string]Task{"task-1": {ID: "task-1", Meta: state.TaskMeta{ID: "task-1", Worktree: own.Path, Project: root}}}

	// Act
	owner, isOwned := ownerOf(extra, tasks, []WorktreeDir{own, extra}, nil)

	// Assert
	if isOwned != isOnePath || (isOwned && owner.ID != "task-1") {
		t.Fatalf("ownerOf(%s) = %q, %t; want task-1 owning it: %t", extra.Path, owner.ID, isOwned, isOnePath)
	}
}

// A goblin worktree under a checkout's .worktrees is this home's when its
// name, gb-<id>, names a task this home holds a record of in another case,
// exactly where the file system reads the two names as one.
func TestClaimsReadsAWorktreesTaskByTheFileSystemsCase(t *testing.T) {
	// Arrange
	stateDir, _, isOnePath := caseSpellings(t, t.TempDir(), "state")
	for _, name := range []string{"TASK-1.meta", "task-2.status"} {
		if err := os.WriteFile(filepath.Join(stateDir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var notes []string
	held := heldIDs(stateDir, nil, &notes)
	checkout := filepath.Join(t.TempDir(), "app")
	cases := []struct {
		name       string
		worktree   string
		wantClaims bool
	}{
		{name: "the record in another case", worktree: "gb-task-1", wantClaims: isOnePath},
		{name: "the prefix in another case", worktree: "GB-task-2", wantClaims: isOnePath},
		{name: "both as written", worktree: "gb-task-2", wantClaims: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isClaimed := claims(checkout, filepath.Join(checkout, ".worktrees", tc.worktree), nil, held)

			// Assert
			if isClaimed != tc.wantClaims {
				t.Fatalf("claims(%s) with records %v = %t, want %t", tc.worktree, held, isClaimed, tc.wantClaims)
			}
		})
	}
}

// A worktree a checkout registers under a folder named .worktrees in another
// case is in the folder an older build kept goblin worktrees in exactly where
// the file system reads the two names as one; elsewhere it is a stray the
// project registered away from the fleet's folders.
func TestWorktreesReadTheOlderBuildsFolderByTheFileSystemsCase(t *testing.T) {
	// Arrange
	root := t.TempDir()
	projects := filepath.Join(root, "dev")
	checkout := filepath.Join(projects, "repo")
	gitInit(t, checkout)
	leftover := filepath.Join(checkout, ".WORKTREES", "leftover")
	command := exec.Command("git", "worktree", "add", "--detach", leftover)
	command.Dir = checkout
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	_, statErr := os.Stat(filepath.Join(checkout, ".worktrees"))
	isOnePath := statErr == nil
	collector := Collector{
		Home:         withRecords(t, filepath.Join(root, "home")),
		Commands:     execx.OSRunner{},
		ProjectsRoot: func() (string, error) { return projects, nil },
	}

	// Act
	var notes []string
	found := collector.worktrees(context.Background(), nil, &notes)

	// Assert
	isStray := false
	for _, worktree := range found {
		if sameFolder(t, worktree.Path, leftover) && worktree.Source == SourceCheckout {
			isStray = true
		}
	}
	if isStray == isOnePath {
		t.Fatalf("worktrees = %+v, notes %q: %s listed as a stray: %t, want %t", found, notes, leftover, isStray, !isOnePath)
	}
}
