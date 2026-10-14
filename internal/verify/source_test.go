package verify

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// sourceWorktree is a repository with one commit: two tracked files, one in
// a folder, a .gitignore that ignores logs, and a log tracked all the same.
func sourceWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeSource(t, dir, "a.txt", "a\n")
	writeSource(t, dir, "folder/b.txt", "b\n")
	writeSource(t, dir, ".gitignore", "*.log\n")
	writeSource(t, dir, "kept.log", "tracked though ignored\n")
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "add", "--force", "kept.log")
	gitIn(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "start")
	return dir
}

func writeSource(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.Output()
	if err != nil {
		var stderr []byte
		if exitError, isExit := err.(*exec.ExitError); isExit {
			stderr = exitError.Stderr
		}
		t.Fatalf("git %v: %v: %s", args, err, stderr)
	}
	return strings.TrimSpace(string(output))
}

// A worktree that matches its commit is that commit's tree, exactly.
func TestReadSourceOfACleanWorktreeIsItsCommitsTree(t *testing.T) {
	// Arrange
	dir := sourceWorktree(t)

	// Act
	source, err := ReadSource(context.Background(), execx.OSRunner{}, dir)

	// Assert
	want := Source{Commit: gitIn(t, dir, "rev-parse", "HEAD"), Tree: gitIn(t, dir, "rev-parse", "HEAD^{tree}"), IsExact: true}
	if err != nil || source != want {
		t.Errorf("ReadSource = %+v, %v; want %+v", source, err, want)
	}
}

// The tree follows what is on disk, whatever the worktree's own index holds,
// so a commit of exactly what a run tested has the tree the run recorded,
// and any other change gives another tree.
func TestReadSourceIsTheTreeOfACommitOfExactlyWhatWasOnDisk(t *testing.T) {
	for name, row := range map[string]struct {
		change      func(t *testing.T, dir string)
		isUnchanged bool
	}{
		"a changed file": {change: func(t *testing.T, dir string) { writeSource(t, dir, "a.txt", "changed\n") }},
		"a new file":     {change: func(t *testing.T, dir string) { writeSource(t, dir, "new/c.txt", "c\n") }},
		"a deleted file": {change: func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "folder", "b.txt")); err != nil {
				t.Fatal(err)
			}
		}},
		"a staged change undone on disk": {isUnchanged: true, change: func(t *testing.T, dir string) {
			writeSource(t, dir, "a.txt", "staged\n")
			gitIn(t, dir, "add", "a.txt")
			writeSource(t, dir, "a.txt", "a\n")
		}},
		"an ignored file": {isUnchanged: true, change: func(t *testing.T, dir string) { writeSource(t, dir, "run.log", "log\n") }},
		"a renamed file": {change: func(t *testing.T, dir string) {
			if err := os.Rename(filepath.Join(dir, "folder", "b.txt"), filepath.Join(dir, "folder", "renamed.txt")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := sourceWorktree(t)
			commitTree := gitIn(t, dir, "rev-parse", "HEAD^{tree}")
			row.change(t, dir)

			// Act
			source, err := ReadSource(context.Background(), execx.OSRunner{}, dir)

			// Assert
			if err != nil || !source.IsExact || (source.Tree == commitTree) != row.isUnchanged {
				t.Fatalf("ReadSource = %+v, %v; want an exact tree that is the commit's own only when nothing on disk differs from it (%s)", source, err, commitTree)
			}
			gitIn(t, dir, "add", "--all")
			gitIn(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "--allow-empty", "-m", "what was tested")
			if committed := gitIn(t, dir, "rev-parse", "HEAD^{tree}"); committed != source.Tree {
				t.Errorf("a commit of what was on disk has tree %s; the run recorded %s", committed, source.Tree)
			}
		})
	}
}

// Reading the source leaves the worktree's own index, staged and unstaged
// changes alike, as it was.
func TestReadSourceLeavesTheWorktreesOwnIndexAsItWas(t *testing.T) {
	// Arrange
	dir := sourceWorktree(t)
	writeSource(t, dir, "a.txt", "staged\n")
	gitIn(t, dir, "add", "a.txt")
	writeSource(t, dir, "folder/b.txt", "not staged\n")
	writeSource(t, dir, "untracked.txt", "untracked\n")
	index := filepath.Join(dir, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	_, err = ReadSource(context.Background(), execx.OSRunner{}, dir)

	// Assert
	after, readError := os.ReadFile(index)
	if err != nil || readError != nil || !bytes.Equal(before, after) {
		t.Errorf("ReadSource: %v; the index was read back with %v and is unchanged = %v; want it as it was", err, readError, bytes.Equal(before, after))
	}
	if staged := gitIn(t, dir, "diff", "--cached", "--name-only"); staged != "a.txt" {
		t.Errorf("staged = %q; want only a.txt", staged)
	}
}

// A submodule's own changes are no part of a tree, which records only the
// commit a submodule is at, so a worktree with one is read as inexact.
func TestReadSourceIsInexactWhenASubmoduleHoldsChangesOfItsOwn(t *testing.T) {
	for name, row := range map[string]struct {
		change    string
		isIgnored bool
	}{
		"a changed file":                      {change: "inner.txt"},
		"a new file":                          {change: "new.txt"},
		"a change the settings say to ignore": {change: "inner.txt", isIgnored: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			inner := sourceWorktree(t)
			writeSource(t, inner, "inner.txt", "inner\n")
			gitIn(t, inner, "add", "inner.txt")
			gitIn(t, inner, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "inner")
			dir := sourceWorktree(t)
			gitIn(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", "-q", inner, "sub")
			gitIn(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "submodule")
			if row.isIgnored {
				gitIn(t, dir, "config", "submodule.sub.ignore", "all")
			}
			writeSource(t, dir, "sub/"+row.change, "changed\n")

			// Act
			source, err := ReadSource(context.Background(), execx.OSRunner{}, dir)

			// Assert
			if err != nil || source.IsExact {
				t.Errorf("ReadSource = %+v, %v; want an inexact source", source, err)
			}
		})
	}
}

// afterWriteTree runs each command and, once git write-tree has run, does
// then: what changes a worktree while its source is read.
type afterWriteTree struct{ then func() }

func (runner afterWriteTree) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	result, err := execx.OSRunner{}.Run(ctx, request)
	if slices.Contains(request.Args, "write-tree") {
		runner.then()
	}
	return result, err
}

// A file that changes on disk after the tree was written is not in it, so
// the read is inexact.
func TestReadSourceIsInexactWhenAFileChangesWhileItIsRead(t *testing.T) {
	for name, file := range map[string]string{
		"a changed file": "a.txt",
		"a new file":     "new.txt",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := sourceWorktree(t)
			commands := afterWriteTree{then: func() { writeSource(t, dir, file, "written while read\n") }}

			// Act
			source, err := ReadSource(context.Background(), commands, dir)

			// Assert
			if err != nil || source.IsExact {
				t.Errorf("ReadSource = %+v, %v; want an inexact source", source, err)
			}
		})
	}
}

// A folder with no commit checked out has no source to read.
func TestReadSourceRefusesAFolderWithNoCommit(t *testing.T) {
	for name, folder := range map[string]func(t *testing.T) string{
		"a repository with no commit": func(t *testing.T) string {
			dir := t.TempDir()
			gitIn(t, dir, "init", "-q")
			return dir
		},
		"a folder outside any repository": func(t *testing.T) string {
			dir := t.TempDir()
			t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
			return dir
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := folder(t)

			// Act
			source, err := ReadSource(context.Background(), execx.OSRunner{}, dir)

			// Assert
			if err == nil {
				t.Errorf("ReadSource = %+v; want an error", source)
			}
		})
	}
}

// A run tested one source only when it read the same exact source as it
// started and once it ended.
func TestSourceReadsTestedIsTheOneExactSourceOfBothReads(t *testing.T) {
	exact := Source{Commit: "c1", Tree: "t1", IsExact: true}
	moved := Source{Commit: "c1", Tree: "t2", IsExact: true}
	inexact := Source{Commit: "c1", Tree: "t1"}
	for name, row := range map[string]struct {
		reads    SourceReads
		isTested bool
	}{
		"the same exact source":      {reads: SourceReads{Start: &exact, End: &exact}, isTested: true},
		"a source that moved":        {reads: SourceReads{Start: &exact, End: &moved}},
		"an inexact source":          {reads: SourceReads{Start: &inexact, End: &inexact}},
		"no read as the run started": {reads: SourceReads{End: &exact}},
		"no read once the run ended": {reads: SourceReads{Start: &exact}},
		"a read that failed":         {reads: SourceReads{Start: &exact, End: &exact, Failure: "git failed"}},
		"no read at all":             {},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			source, err := row.reads.Tested()

			// Assert
			if row.isTested && (err != nil || source != exact) {
				t.Errorf("Tested = %+v, %v; want %+v", source, err, exact)
			}
			if !row.isTested && (err == nil || source != (Source{})) {
				t.Errorf("Tested = %+v, %v; want no source and why", source, err)
			}
		})
	}
}
