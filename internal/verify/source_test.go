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
	tree := gitIn(t, dir, "rev-parse", "HEAD^{tree}")
	want := Source{Commit: gitIn(t, dir, "rev-parse", "HEAD"), CommitTree: tree, Tree: tree, IsExact: true}
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
// commit a submodule is at, and neither are the files of one not checked
// out, so a worktree with either is read as inexact.
func TestReadSourceIsInexactWhenASubmoduleHoldsChangesOfItsOwn(t *testing.T) {
	for name, arrange := range map[string]func(t *testing.T) string{
		"a changed file": func(t *testing.T) string {
			dir := sourceSuperproject(t)
			writeSource(t, dir, "sub/inner.txt", "changed\n")
			return dir
		},
		"a new file": func(t *testing.T) string {
			dir := sourceSuperproject(t)
			writeSource(t, dir, "sub/new.txt", "new\n")
			return dir
		},
		"a change the settings say to ignore": func(t *testing.T) string {
			dir := sourceSuperproject(t)
			gitIn(t, dir, "config", "submodule.sub.ignore", "all")
			writeSource(t, dir, "sub/inner.txt", "changed\n")
			return dir
		},
		"a submodule not checked out": func(t *testing.T) string {
			clone := filepath.Join(t.TempDir(), "clone")
			gitIn(t, sourceSuperproject(t), "clone", "-q", ".", clone)
			return clone
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := arrange(t)

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
// the read is inexact, whatever the repository's settings would hide.
func TestReadSourceIsInexactWhenAFileChangesWhileItIsRead(t *testing.T) {
	for name, row := range map[string]struct {
		file    string
		setting []string
	}{
		"a changed file":                        {file: "a.txt"},
		"a new file":                            {file: "new.txt"},
		"a new file the settings hide":          {file: "new.txt", setting: []string{"status.showUntrackedFiles", "no"}},
		"a changed file whose stat is not read": {file: "a.txt", setting: []string{"core.ignoreStat", "true"}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := sourceWorktree(t)
			if row.setting != nil {
				gitIn(t, dir, append([]string{"config"}, row.setting...)...)
			}
			commands := afterWriteTree{then: func() { writeSource(t, dir, row.file, "written while read\n") }}

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

// sourceSuperproject is a source worktree with a submodule at sub, whose
// repository has a commit of its own with inner.txt.
func sourceSuperproject(t *testing.T) string {
	t.Helper()
	inner := sourceWorktree(t)
	writeSource(t, inner, "inner.txt", "inner\n")
	gitIn(t, inner, "add", "inner.txt")
	gitIn(t, inner, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "inner")
	dir := sourceWorktree(t)
	gitIn(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", "-q", inner, "sub")
	gitIn(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "submodule")
	return dir
}

// A submodule checked out at a commit is in the tree by that commit, so a
// worktree with one is read exactly, whichever commit it is at.
func TestReadSourceOfASubmoduleAtACommitIsExact(t *testing.T) {
	for name, row := range map[string]struct {
		isMoved bool
	}{
		"a submodule at its recorded commit":  {},
		"a submodule moved to another commit": {isMoved: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := sourceSuperproject(t)
			if row.isMoved {
				writeSource(t, dir, "sub/inner.txt", "moved\n")
				gitIn(t, filepath.Join(dir, "sub"), "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-am", "moved")
			}

			// Act
			source, err := ReadSource(context.Background(), execx.OSRunner{}, dir)

			// Assert
			if err != nil || !source.IsExact || (source.Tree == source.CommitTree) == row.isMoved {
				t.Fatalf("ReadSource = %+v, %v; want an exact source that is its commit's own tree only when the submodule did not move", source, err)
			}
			gitIn(t, dir, "add", "--all")
			gitIn(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "--allow-empty", "-m", "what was tested")
			if committed := gitIn(t, dir, "rev-parse", "HEAD^{tree}"); committed != source.Tree {
				t.Errorf("a commit of what was on disk has tree %s; the run recorded %s", committed, source.Tree)
			}
		})
	}
}

// A sparse checkout leaves out of the disk files its commit holds, so its
// tree does not say what was on disk.
func TestReadSourceIsInexactInASparseCheckout(t *testing.T) {
	// Arrange
	dir := sourceWorktree(t)
	gitIn(t, dir, "sparse-checkout", "set", "--no-cone", "/a.txt")

	// Act
	source, err := ReadSource(context.Background(), execx.OSRunner{}, dir)

	// Assert
	if err != nil || source.IsExact {
		t.Errorf("ReadSource = %+v, %v; want an inexact source", source, err)
	}
}

// A read reads the worktree it is given, whatever repository, index or
// object store the git variables it inherits would name, as a git hook or a
// gate daemon sets them.
func TestReadSourceReadsTheWorktreeItIsGivenWhateverGitItInherits(t *testing.T) {
	for name, arrange := range map[string]func(t *testing.T, dir string) string{
		"a linked worktree": func(t *testing.T, dir string) string {
			linked := filepath.Join(t.TempDir(), "linked")
			gitIn(t, dir, "worktree", "add", "-q", "-b", "linked", linked)
			writeSource(t, linked, "a.txt", "linked\n")
			gitIn(t, linked, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-am", "linked")
			return linked
		},
		"a folder inside it": func(t *testing.T, dir string) string {
			return filepath.Join(dir, "folder")
		},
		"a worktree whose path has a space": func(t *testing.T, dir string) string {
			spaced := filepath.Join(t.TempDir(), "with space")
			gitIn(t, dir, "clone", "-q", ".", spaced)
			return spaced
		},
		"another repository its variables name": func(t *testing.T, dir string) string {
			other := sourceWorktree(t)
			writeSource(t, other, "a.txt", "other\n")
			gitIn(t, other, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-am", "other")
			t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
			t.Setenv("GIT_WORK_TREE", other)
			return dir
		},
		"an object store its variables name": func(t *testing.T, dir string) string {
			t.Setenv("GIT_OBJECT_DIRECTORY", t.TempDir())
			return dir
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := sourceWorktree(t)
			read := arrange(t, dir)
			worktree := read
			if name == "a folder inside it" {
				worktree = dir
			}
			commit, tree := gitClean(t, worktree, "HEAD"), gitClean(t, worktree, "HEAD^{tree}")

			// Act
			source, err := ReadSource(context.Background(), execx.OSRunner{}, read)

			// Assert
			want := Source{Commit: commit, CommitTree: tree, Tree: tree, IsExact: true}
			if err != nil || source != want {
				t.Errorf("ReadSource = %+v, %v; want %+v", source, err, want)
			}
		})
	}
}

// gitClean is git rev-parse name in dir, run with none of the git variables
// a test set.
func gitClean(t *testing.T, dir, name string) string {
	t.Helper()
	command := exec.Command("git", "rev-parse", name)
	command.Dir = dir
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git rev-parse %s in %s: %v", name, dir, err)
	}
	return strings.TrimSpace(string(output))
}

// A read writes nothing into the repository: the content of a changed or new
// file it stages goes to an object store of its own, which it removes.
func TestReadSourceWritesNothingIntoTheRepository(t *testing.T) {
	// Arrange
	dir := sourceWorktree(t)
	writeSource(t, dir, "new.txt", "content no object holds\n")
	blob := gitIn(t, dir, "hash-object", "new.txt")

	// Act
	source, err := ReadSource(context.Background(), execx.OSRunner{}, dir)

	// Assert
	command := exec.Command("git", "cat-file", "-e", blob)
	command.Dir = dir
	if err != nil || source.Tree == source.CommitTree || command.Run() == nil {
		t.Errorf("ReadSource = %+v, %v; want a tree with new.txt, and its content still in no object of the repository", source, err)
	}
}
