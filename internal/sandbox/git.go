package sandbox

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// emptyTree is git's empty tree, which every repository holds without
// storing it.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// HooksOffDir is the folder under a task's tasktmp folder that its host git
// reads hooks from, which holds none and which no container mounts.
const HooksOffDir = "git-hooks-off"

// HostGitEnvironment is what a sandbox task's terminal sets for every git
// and gh it runs on the host, which the container's writes to the worktree
// would otherwise reach: hooks are read from hooksDir, an empty folder no
// container mounts, git's fsmonitor is off, attributes are read from the
// empty tree rather than the worktree's .gitattributes, so no filter, diff
// or merge driver a file in the worktree names runs, git recurses into no
// submodule by default, and git never takes a folder it finds for a bare
// repository. Git reads these above any repository's own configuration.
// What a replaced .git link or a submodule would bring, a repository
// configuration of the container's choosing, is refused before each command
// instead (CheckGitLink and NestedGitRefusal), as is a git pointed at
// another repository (HostCommandRefusal).
func HostGitEnvironment(hooksDir string) map[string]string {
	return map[string]string{
		"GIT_CONFIG_COUNT":   "5",
		"GIT_CONFIG_KEY_0":   "core.hooksPath",
		"GIT_CONFIG_VALUE_0": hooksDir,
		"GIT_CONFIG_KEY_1":   "core.fsmonitor",
		"GIT_CONFIG_VALUE_1": "false",
		"GIT_CONFIG_KEY_2":   "submodule.recurse",
		"GIT_CONFIG_VALUE_2": "false",
		"GIT_CONFIG_KEY_3":   "diff.ignoreSubmodules",
		"GIT_CONFIG_VALUE_3": "all",
		"GIT_CONFIG_KEY_4":   "safe.bareRepository",
		"GIT_CONFIG_VALUE_4": "explicit",
		"GIT_ATTR_SOURCE":    emptyTree,
	}
}

// ReadGitLink is the text of worktree's .git link, the file that names the
// home's git folder for it, which a sandbox task's record keeps from its
// spawn. A .git that is no plain file is refused.
func ReadGitLink(worktree string) (string, error) {
	path := filepath.Join(worktree, ".git")
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("sandbox: read the worktree's .git link: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("sandbox: the worktree's .git at %s is a %s, not the link file git writes for a worktree", path, kind(info))
	}
	data, err := fsx.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("sandbox: read the worktree's .git link: %w", err)
	}
	link := strings.TrimSpace(string(data))
	if !strings.HasPrefix(link, "gitdir: ") || strings.ContainsAny(link, "\r\n") {
		return "", fmt.Errorf("sandbox: the worktree's .git at %s does not name one git folder", path)
	}
	return link, nil
}

// CheckGitLink refuses a host command of a sandbox task whose worktree's
// .git link no longer says want, the link its spawn recorded, or whose
// worktree has a submodule (submoduleRefusal): each would have host git read
// a repository the container wrote, its hooks, filters, fsmonitor and
// configuration. A cwd outside the worktree is refused too, since the host
// runs a sandbox task's commands only there.
func CheckGitLink(worktree, cwd, want string) error {
	link, err := ReadGitLink(worktree)
	if err != nil {
		return err
	}
	if link != want {
		return fmt.Errorf("sandbox: the worktree's .git link now says %q, not %q, the link its spawn recorded, so host git would read a repository the container made", link, want)
	}
	gitDir := filepath.FromSlash(strings.TrimPrefix(link, "gitdir: "))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktree, gitDir)
	}
	if err := submoduleRefusal(worktree, gitDir); err != nil {
		return err
	}
	if cwd == "" {
		return nil
	}
	relative, err := filepath.Rel(worktree, cwd)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("sandbox: this command runs in %s, outside its worktree %s, and a sandbox task runs host commands only in its worktree", cwd, worktree)
	}
	return nil
}

// NestedGitRefusal refuses a host git of a sandbox task while a .git other
// than its own link is anywhere under its worktree, one the container could
// have made: host git reads that repository's configuration, hooks and
// filters from a folder under it, through a submodule, or once a checkout
// or a pull brings in a gitlink there, after any check of the command
// itself. It walks the whole worktree, stopping at the first it finds, and
// refuses a folder it cannot read, since what that holds is unknown.
func NestedGitRefusal(worktree string) error {
	own := filepath.Join(worktree, ".git")
	found := ""
	err := filepath.WalkDir(worktree, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.EqualFold(entry.Name(), ".git") && path != own {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("sandbox: look for a .git the container made in the worktree: %w", err)
	}
	if found == "" {
		return nil
	}
	relative := strings.TrimPrefix(found, filepath.Clean(worktree)+string(filepath.Separator))
	return fmt.Errorf("sandbox: the worktree holds %s, a .git of another repository, which host git would read as the container wrote it: remove it in the container with goblins sandbox run -- rm -rf %s, and if the project needs it, tell the CEO, since a sandbox task's project cannot hold a repository of its own", found, filepath.ToSlash(relative))
}

// submoduleRefusal refuses worktree, whose git folder is gitDir, while it
// has a submodule, whose repository the container could have made: a
// .gitmodules, whose ignore lines override the host's diff.ignoreSubmodules,
// or an index entry that is a gitlink, a submodule's commit, or a sparse
// directory, which could hold one. It reads the index itself, version 2, 3
// or 4, and runs no program, and refuses an index split across a shared
// index or one it cannot read, since what they hold is unknown.
func submoduleRefusal(worktree, gitDir string) error {
	const cannotUse = "a sandbox task's project cannot use submodules, since host git would read the repository the container makes for one"
	if _, err := os.Lstat(filepath.Join(worktree, ".gitmodules")); err == nil {
		return fmt.Errorf("sandbox: the worktree holds %s, and %s", filepath.Join(worktree, ".gitmodules"), cannotUse)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("sandbox: look for the worktree's .gitmodules: %w", err)
	}
	shared, err := filepath.Glob(filepath.Join(gitDir, "sharedindex.*"))
	if err != nil {
		return fmt.Errorf("sandbox: look for a shared index in %s: %w", gitDir, err)
	}
	if len(shared) > 0 {
		return fmt.Errorf("sandbox: the index in %s is split across %s, which this check does not read for submodules", gitDir, shared[0])
	}
	data, err := fsx.ReadFile(filepath.Join(gitDir, "index"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sandbox: read the worktree's index: %w", err)
	}
	unreadable := fmt.Errorf("sandbox: the index in %s cannot be read for submodules", gitDir)
	if len(data) < 12 || string(data[:4]) != "DIRC" {
		return unreadable
	}
	version := binary.BigEndian.Uint32(data[4:8])
	if version < 2 || version > 4 {
		return unreadable
	}
	offset, path := 12, ""
	for range binary.BigEndian.Uint32(data[8:12]) {
		start := offset + 62
		if start > len(data) {
			return unreadable
		}
		mode := binary.BigEndian.Uint32(data[offset+24:])
		if version >= 3 && binary.BigEndian.Uint16(data[offset+60:])&0x4000 != 0 {
			start += 2
		}
		if start >= len(data) {
			return unreadable
		}
		if version == 4 {
			c := data[start]
			start++
			strip := int(c & 0x7f)
			for c&0x80 != 0 {
				if start >= len(data) || strip > len(data) {
					return unreadable
				}
				c = data[start]
				start++
				strip = (strip+1)<<7 | int(c&0x7f)
			}
			end := bytes.IndexByte(data[start:], 0)
			if end < 0 || strip > len(path) {
				return unreadable
			}
			path = path[:len(path)-strip] + string(data[start:start+end])
			offset = start + end + 1
		} else {
			end := bytes.IndexByte(data[start:], 0)
			if end < 0 {
				return unreadable
			}
			path = string(data[start : start+end])
			offset += (start - offset + end + 8) &^ 7
		}
		if mode == 0o160000 || mode == 0o040000 {
			return fmt.Errorf("sandbox: the worktree's index names %s as a submodule, and %s", path, cannotUse)
		}
	}
	return nil
}

// kind names what a .git that is no plain file is.
func kind(info fs.FileInfo) string {
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return "link"
	case info.IsDir():
		return "folder"
	}
	return "special file"
}
