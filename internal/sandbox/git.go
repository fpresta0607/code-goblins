package sandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// emptyTree is git's empty tree, which every repository holds without
// storing it.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Variable is set in a sandbox task's terminal, so the seal its Codex or pi
// runs as a command reads the task's record for its host rule, as it does
// for no other task's command that names no path.
const Variable = "CFO_SANDBOX"

// HooksOffDir is the folder under a task's tasktmp folder that its host git
// reads hooks from, which holds none and which no container mounts.
const HooksOffDir = "git-hooks-off"

// HostGitEnvironment is what a sandbox task's terminal sets for every git
// and gh it runs on the host, which the container's writes to the worktree
// would otherwise reach: hooks are read from hooksDir, an empty folder no
// container mounts, git's fsmonitor is off, and attributes are read from
// the empty tree rather than the worktree's .gitattributes, so no filter,
// diff or merge driver a file in the worktree names runs. Git reads these
// above any repository's own configuration. What a replaced .git link would
// bring, a repository configuration of the container's choosing, is refused
// before each command instead (CheckGitLink).
func HostGitEnvironment(hooksDir string) map[string]string {
	return map[string]string{
		"GIT_CONFIG_COUNT":   "2",
		"GIT_CONFIG_KEY_0":   "core.hooksPath",
		"GIT_CONFIG_VALUE_0": hooksDir,
		"GIT_CONFIG_KEY_1":   "core.fsmonitor",
		"GIT_CONFIG_VALUE_1": "false",
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
	data, err := os.ReadFile(path)
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
// .git link no longer says want, the link its spawn recorded, or that runs
// from a folder of the worktree, cwd, under which a .git of another
// repository was made: either would have host git read a repository the
// container wrote, its hooks, filters, fsmonitor and configuration. A cwd
// outside the worktree is refused too, since the host runs a sandbox task's
// commands only there.
func CheckGitLink(worktree, cwd, want string) error {
	link, err := ReadGitLink(worktree)
	if err != nil {
		return err
	}
	if link != want {
		return fmt.Errorf("sandbox: the worktree's .git link now says %q, not %q, the link its spawn recorded, so host git would read a repository the container made", link, want)
	}
	if cwd == "" {
		return nil
	}
	relative, err := filepath.Rel(worktree, cwd)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("sandbox: this command runs in %s, outside its worktree %s, and a sandbox task runs host commands only in its worktree", cwd, worktree)
	}
	for dir := cwd; !strings.EqualFold(filepath.Clean(dir), filepath.Clean(worktree)); dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return fmt.Errorf("sandbox: %s holds a .git of its own, so host git there would read a repository the container made", dir)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("sandbox: look for a .git in %s: %w", dir, err)
		}
		if filepath.Dir(dir) == dir {
			break
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
