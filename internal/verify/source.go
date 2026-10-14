package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Source is the exact source a run tested, read from its worktree.
type Source struct {
	// Commit is the commit checked out.
	Commit string `json:"commit"`
	// Tree is the git tree of what was on disk: every file git does not
	// ignore, with its content and mode, whatever the worktree's own index
	// held. A changed, deleted or new file each gives another tree, a commit
	// of exactly what was tested has this tree, and a worktree that matches
	// its commit has the commit's own.
	Tree string `json:"tree"`
	// IsExact is false when the tree does not hold all of what was on disk:
	// a submodule had changes of its own, of which a tree records only the
	// commit the submodule is at, or a file changed while the tree was read.
	IsExact bool `json:"exact"`
}

// ReadSource reads the source of the worktree dir. It stages what is on disk
// into an index of its own, which leaves the worktree's index as it was, and
// writes the content of each changed or new file into the repository, as git
// add does.
func ReadSource(ctx context.Context, commands execx.Runner, dir string) (Source, error) {
	folder, err := os.MkdirTemp("", "cfo-source-")
	if err != nil {
		return Source{}, fmt.Errorf("verify: read the source: %w", err)
	}
	defer os.RemoveAll(folder)
	environment := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(folder, "index"))
	git := func(args ...string) (string, error) {
		result, err := commands.Run(ctx, execx.Request{Dir: dir, Env: environment, Name: "git", Args: args})
		if err == nil && result.ExitCode != 0 {
			err = fmt.Errorf("exited %d: %s", result.ExitCode, bytes.TrimSpace(result.Stderr))
		}
		if err != nil {
			return "", fmt.Errorf("verify: read the source: git %s: %w", strings.Join(args, " "), err)
		}
		return string(result.Stdout), nil
	}
	commit, err := git("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return Source{}, err
	}
	commit = strings.TrimSpace(commit)
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
	tree = strings.TrimSpace(tree)
	// With everything on disk staged, what status still finds on disk is
	// what the tree does not hold, whatever the repository's settings ignore.
	status, err := git("status", "--porcelain=v2", "-z", "--no-renames", "--ignore-submodules=none")
	if err != nil {
		return Source{}, err
	}
	isExact := true
	for _, entry := range strings.Split(status, "\x00") {
		switch {
		case entry == "":
		case strings.HasPrefix(entry, "1 ") && len(entry) > 3:
			isExact = isExact && entry[3] == '.'
		default:
			isExact = false
		}
	}
	return Source{Commit: commit, Tree: tree, IsExact: isExact}, nil
}

// SourceReads are the two reads of a run's source (ReadSource): once its turn
// was taken and before it started, and once it had ended. Failure says why a
// read failed.
type SourceReads struct {
	Start   *Source `json:"start,omitempty"`
	End     *Source `json:"end,omitempty"`
	Failure string  `json:"failure,omitempty"`
}

// Read records a read of the source: as the run started, or once it had
// ended. A read that failed is recorded as such.
func (reads *SourceReads) Read(isEnd bool, source Source, failure error) {
	switch {
	case failure != nil:
		reads.Failure = failure.Error()
	case isEnd:
		reads.End = &source
	default:
		reads.Start = &source
	}
}

// Tested is the one source a run tested: the same exact source at both
// reads. Otherwise the run tested no one source, and the error says why.
func (reads SourceReads) Tested() (Source, error) {
	switch {
	case reads.Failure != "":
		return Source{}, fmt.Errorf("verify: the source could not be read: %s", reads.Failure)
	case reads.Start == nil || reads.End == nil:
		return Source{}, errors.New("verify: the source was not read both as the run started and once it ended")
	case *reads.Start != *reads.End:
		return Source{}, fmt.Errorf("verify: the source changed while the run ran, from tree %s to tree %s", reads.Start.Tree, reads.End.Tree)
	case !reads.Start.IsExact:
		return Source{}, errors.New("verify: the source holds changes its tree does not, as a submodule's own")
	}
	return *reads.Start, nil
}
