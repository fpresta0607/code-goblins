package pipeline

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// ReviewLineLimit is the most lines a branch may change for a gate run to
// start on it. In the week to 2026-10-27 reviews of changes over 2,000 lines
// read 85% of the gate's review tokens, about 41.6 million a pass, and a run
// that ends without landing reviews the whole branch again (#2002).
const ReviewLineLimit = 2000

// lockFiles are the dependency lock files a branch's size leaves out: a tool
// writes them, and no reviewer reads them line by line.
var lockFiles = []string{"go.sum", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "uv.lock", "Cargo.lock"}

// ChangedFile is a file a branch changes and how many lines it changes.
type ChangedFile struct {
	Path  string
	Lines int
}

// BranchSize is how many lines a branch changes, and the files it counted,
// the largest first.
type BranchSize struct {
	Lines int
	Files []ChangedFile
}

// BranchSizeOf counts the lines the HEAD of worktree changes against its merge
// base with base: insertions plus deletions, leaving out binary files, files
// the repository marks linguist-generated in .gitattributes, and lock files.
func BranchSizeOf(ctx context.Context, commands execx.Runner, worktree, base string) (BranchSize, error) {
	mergeBase, err := commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"merge-base", base, "HEAD"}})
	fields := strings.Fields(string(mergeBase.Stdout))
	if err != nil || mergeBase.ExitCode != 0 || len(fields) != 1 {
		return BranchSize{}, fmt.Errorf("pipeline: the merge base of HEAD and %s cannot be read, so the branch's size is unknown and no run started: %v %s", base, err, strings.TrimSpace(string(mergeBase.Stderr)))
	}
	// Both forms of the attribute are left out: a bare linguist-generated
	// sets it, and attr:linguist-generated matches only that, not =true.
	diff, err := commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"diff", "--numstat", "-z", "-M", fields[0], "HEAD", "--", ".", ":(exclude,attr:linguist-generated)", ":(exclude,attr:linguist-generated=true)"}})
	if err != nil || diff.ExitCode != 0 {
		return BranchSize{}, fmt.Errorf("pipeline: the branch's changes since %s cannot be read, so its size is unknown and no run started: %v %s", fields[0], err, strings.TrimSpace(string(diff.Stderr)))
	}
	return numstatSize(string(diff.Stdout))
}

// numstatSize reads git diff --numstat -z: each file is its insertions, a
// tab, its deletions, a tab and its path, ended by a NUL, and a renamed file
// has an empty path followed by its old and new paths, each ended by a NUL.
// A binary file counts - for both.
func numstatSize(numstat string) (BranchSize, error) {
	var size BranchSize
	records := strings.Split(numstat, "\x00")
	for at := 0; at < len(records); at++ {
		if records[at] == "" {
			continue
		}
		counts := strings.SplitN(records[at], "\t", 3)
		if len(counts) != 3 {
			return BranchSize{}, fmt.Errorf("pipeline: git diff --numstat wrote %q, which is no file's change", records[at])
		}
		name := counts[2]
		if name == "" {
			if at+2 >= len(records) {
				return BranchSize{}, fmt.Errorf("pipeline: git diff --numstat wrote a rename with no new path after %q", records[at])
			}
			name = records[at+2]
			at += 2
		}
		if counts[0] == "-" && counts[1] == "-" {
			continue
		}
		if slices.Contains(lockFiles, path.Base(name)) {
			continue
		}
		added, addedErr := strconv.Atoi(counts[0])
		deleted, deletedErr := strconv.Atoi(counts[1])
		if addedErr != nil || deletedErr != nil {
			return BranchSize{}, fmt.Errorf("pipeline: git diff --numstat wrote %q, whose counts are not numbers", records[at])
		}
		size.Lines += added + deleted
		size.Files = append(size.Files, ChangedFile{Path: name, Lines: added + deleted})
	}
	slices.SortStableFunc(size.Files, func(a, b ChangedFile) int { return cmp.Compare(b.Lines, a.Lines) })
	return size, nil
}
