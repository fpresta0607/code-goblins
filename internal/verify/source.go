package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Source is the source a run tested, read from its worktree: every file git
// does not ignore. Ignored files, such as installed dependencies or a built
// page a package embeds, are inputs too, and are no part of it.
type Source struct {
	// Commit is the commit checked out, and CommitTree its own tree.
	Commit     string `json:"commit"`
	CommitTree string `json:"commit_tree"`
	// Tree is the git tree of the files git does not ignore as they were on
	// disk, whatever the worktree's own index held: a changed, deleted or new
	// file each gives another tree, and a worktree that matches its commit
	// has CommitTree. Its content is git's, after line-ending conversion and
	// clean filters, with this machine's core.filemode=false a file's mode is
	// its commit's, and on this case-insensitive disk a rename that changes
	// only a name's case keeps the commit's spelling.
	Tree string `json:"tree"`
	// IsComplete is false when Tree does not hold all of those files as they
	// were: a submodule had changes of its own or was not checked out, git
	// left a file unstaged, as a sparse checkout leaves the files it keeps
	// off the disk, or a file changed while the tree was read.
	IsComplete bool `json:"complete"`
}

// sourceReadLimit bounds a read, which holds the machine's turn.
const sourceReadLimit = 2 * time.Minute

// repositoryVariables are the variables that point git at another
// repository, index or object store, or pass it configuration, as git
// rev-parse --local-env-vars names them. A read never inherits them, so it
// reads the worktree it is given, as git hooks and gate daemons set them.
var repositoryVariables = []string{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
	"GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE", "GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE",
	"GIT_INDEX_FILE", "GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX",
	"GIT_SHALLOW_FILE", "GIT_COMMON_DIR",
}

// repositoryFreeEnvironment is this process's environment without
// repositoryVariables.
func repositoryFreeEnvironment() []string {
	var environment []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(strings.ToUpper(entry), "=")
		if !slices.Contains(repositoryVariables, name) && !strings.HasPrefix(name, "GIT_CONFIG_KEY_") && !strings.HasPrefix(name, "GIT_CONFIG_VALUE_") {
			environment = append(environment, entry)
		}
	}
	return environment
}

// ReadSource reads the source of the worktree dir. It stages what is on disk
// into an index and an object store of its own, which it removes after, so
// it leaves the worktree's index as it was and writes nothing into the
// repository. A store it cannot remove fails the read, which says where.
func ReadSource(ctx context.Context, commands execx.Runner, dir string) (source Source, err error) {
	ctx, cancel := context.WithTimeout(ctx, sourceReadLimit)
	defer cancel()
	folder, err := os.MkdirTemp("", "cfo-source-")
	if err != nil {
		return Source{}, fmt.Errorf("verify: read the source: %w", err)
	}
	defer func() {
		if removeError := fsx.RemoveAll(folder); removeError != nil {
			source, err = Source{}, errors.Join(err, fmt.Errorf("verify: read the source: its store %s was not removed: %w", folder, removeError))
		}
	}()
	environment := repositoryFreeEnvironment()
	git := func(args ...string) (string, error) {
		// A file system monitor would answer status for the read, and with
		// core.ignoreStat a change after the add would go unseen.
		args = append([]string{"-c", "core.fsmonitor=false", "-c", "core.ignoreStat=false"}, args...)
		// A git cut off at the limit ends with what it started, as a
		// clean filter.
		result, err := commands.Run(ctx, execx.Request{Dir: dir, Env: environment, Name: "git", Args: args, KillTree: true})
		if err == nil && result.ExitCode != 0 {
			err = fmt.Errorf("exited %d: %s", result.ExitCode, bytes.TrimSpace(result.Stderr))
		}
		if err != nil {
			return "", fmt.Errorf("verify: read the source: git %s: %w", strings.Join(args[4:], " "), err)
		}
		return string(result.Stdout), nil
	}
	found, err := git("rev-parse", "--path-format=absolute", "--show-toplevel", "--git-path", "objects", "HEAD^{commit}", "HEAD^{tree}")
	if err != nil {
		return Source{}, err
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(found, "\r\n", "\n")), "\n")
	if len(lines) != 4 {
		return Source{}, fmt.Errorf("verify: read the source: git rev-parse said %q", found)
	}
	top, objects, commit, commitTree := lines[0], lines[1], lines[2], lines[3]
	objectStore := filepath.Join(folder, "objects")
	if err := os.Mkdir(objectStore, 0o700); err != nil {
		return Source{}, fmt.Errorf("verify: read the source: %w", err)
	}
	environment = append(environment, "GIT_INDEX_FILE="+filepath.Join(folder, "index"), "GIT_OBJECT_DIRECTORY="+objectStore, "GIT_ALTERNATE_OBJECT_DIRECTORIES="+objects)
	if _, err := git("read-tree", commit); err != nil {
		return Source{}, err
	}
	if _, err := git("add", "--all"); err != nil {
		return Source{}, err
	}
	tree, err := git("write-tree")
	if err != nil {
		return Source{}, err
	}
	// With everything on disk staged, what status still finds on disk is
	// what the tree does not hold, whatever the repository's settings hide.
	status, err := git("status", "--porcelain=v2", "-z", "--no-renames", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return Source{}, err
	}
	isComplete := true
	for _, entry := range strings.Split(status, "\x00") {
		switch {
		case entry == "":
		case strings.HasPrefix(entry, "1 ") && len(entry) > 3:
			isComplete = isComplete && entry[3] == '.'
		default:
			isComplete = false
		}
	}
	// A submodule that is not checked out matches its commit for status,
	// though none of its files were on disk. A worktree names its
	// submodules in .gitmodules.
	if _, err := os.Stat(filepath.Join(top, ".gitmodules")); err == nil {
		submodules, err := git("submodule", "status")
		if err != nil {
			return Source{}, err
		}
		for _, line := range strings.Split(strings.TrimRight(submodules, "\n"), "\n") {
			isComplete = isComplete && (line == "" || line[0] == ' ')
		}
	}
	return Source{Commit: commit, CommitTree: commitTree, Tree: strings.TrimSpace(tree), IsComplete: isComplete}, nil
}

// SourceReads are the two reads of a run's source (ReadSource): once its turn
// was taken and before it started, and once it had ended. Failure says why a
// read failed.
type SourceReads struct {
	Start   *Source `json:"start,omitempty"`
	End     *Source `json:"end,omitempty"`
	Failure string  `json:"failure,omitempty"`
}

// ReadStart records the read as the run started, or why it failed.
func (reads *SourceReads) ReadStart(source Source, failure error) {
	if failure != nil {
		reads.Failure = failure.Error()
		return
	}
	reads.Start = &source
}

// ReadEnd records the read once the run had ended, or why it failed.
func (reads *SourceReads) ReadEnd(source Source, failure error) {
	if failure != nil {
		reads.Failure = failure.Error()
		return
	}
	reads.End = &source
}

// Tested is the source a run tested: the same complete source at both reads.
// Otherwise there is none, and the error says why. Two reads cannot see a
// file changed and changed back between them, nor an ignored input, so this
// is what the run's reads show, not proof that nothing else changed.
func (reads SourceReads) Tested() (Source, error) {
	switch {
	case reads.Failure != "":
		return Source{}, fmt.Errorf("verify: the source could not be read: %s", reads.Failure)
	case reads.Start == nil || reads.End == nil:
		return Source{}, errors.New("verify: the source was not read both as the run started and once it ended")
	case *reads.Start != *reads.End:
		return Source{}, fmt.Errorf("verify: the source differed between the reads as the run started and once it ended, from tree %s to tree %s", reads.Start.Tree, reads.End.Tree)
	case !reads.Start.IsComplete:
		return Source{}, errors.New("verify: the tree does not hold all of what was on disk, as with a submodule's own changes, one not checked out, a sparse checkout or a file changed while it was read")
	}
	return *reads.Start, nil
}
