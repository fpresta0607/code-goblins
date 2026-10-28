package reap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// activityGrace is how long a worktree is kept after its last change. A
// session works in one between commits with nothing to show for it but what it
// changed, and on 2026-10-27 a grace counted from a worktree's creation let
// the janitor return 77 worktrees the Overlord's live sessions had made.
const activityGrace = time.Hour

// markWorktreesInUse sets InUse on every worktree its project lists and no
// task owns: why a live session may still be using it, or empty when nothing
// shows one is. A process works in it, or anywhere in a folder of its
// repository that holds it, as a session does whose subagents each work in a
// worktree made under the session's checkout; or its HEAD, its index, its HEAD's
// reflog or a file git sees in it changed within activityGrace. What cannot be
// read keeps it. A task's own worktree is returned through cfo cleanup, which
// first ends what the task left running there.
func (c Collector) markWorktreesInUse(ctx context.Context, inv *Inventory) {
	tasks := make(map[string]Task, len(inv.Tasks))
	for _, task := range inv.Tasks {
		tasks[task.ID] = task
	}
	unreadable := make(map[string]bool, len(inv.UnreadableTasks))
	for _, id := range inv.UnreadableTasks {
		unreadable[id] = true
	}
	var working []workingProcess
	isRead := false
	for i, dir := range inv.Worktrees {
		if dir.Registration != RegistrationListed {
			continue
		}
		if _, known := ownerOf(dir, tasks, inv.Worktrees, unreadable); known {
			continue
		}
		if c.Processes == nil || c.WorkingDirectory == nil {
			inv.Worktrees[i].InUse = "the processes running on the machine were not read, so whether a live session works in it is unknown"
			continue
		}
		if !isRead {
			working = c.workingProcesses(inv.Processes)
			isRead = true
		}
		inv.Worktrees[i].InUse = c.inUse(ctx, dir, inv.Worktrees, working)
	}
}

// workingProcess is a process with the folder it works in and every folder
// above that, each by its directory identity: git spells a path through an
// 8.3 folder name, and resolves a junction the process may have entered by
// its own name.
type workingProcess struct {
	Process
	folder string
	chain  []os.FileInfo
}

// workingProcesses reads the folder each process works in. One whose folder
// cannot be read, because it runs at a privilege this one cannot open or has
// ended, is left out: it is no evidence about any folder.
func (c Collector) workingProcesses(processes []Process) []workingProcess {
	var working []workingProcess
	for _, process := range processes {
		folder, err := c.WorkingDirectory(process.PID)
		if err != nil {
			continue
		}
		folder = filepath.Clean(folder)
		chain := folderChain(folder)
		if len(chain) == 0 {
			continue
		}
		working = append(working, workingProcess{Process: process, folder: folder, chain: chain})
	}
	return working
}

func (c Collector) inUse(ctx context.Context, dir WorktreeDir, worktrees []WorktreeDir, working []workingProcess) string {
	chain := folderChain(dir.Path)
	if len(chain) == 0 {
		return "it could not be read, so whether a live session works in it is unknown"
	}
	var repository []os.FileInfo
	for _, folder := range append([]WorktreeDir{{Path: dir.Project, Project: dir.Project}}, worktrees...) {
		if normalizePath(filepath.Clean(folder.Project)) != normalizePath(filepath.Clean(dir.Project)) {
			continue
		}
		if info, err := os.Stat(folder.Path); err == nil {
			repository = append(repository, info)
		}
	}
	for _, process := range working {
		if slices.ContainsFunc(repository, func(root os.FileInfo) bool { return holds(process.chain, root) && holds(chain, root) }) {
			return fmt.Sprintf("pid %d %s works in %s, so a live session may still be using it", process.PID, process.Name, process.folder)
		}
	}
	changed, err := c.lastChange(ctx, dir.Path)
	if err != nil {
		return "when it last changed could not be read, so whether a live session works in it is unknown: " + err.Error()
	}
	if time.Since(changed) < activityGrace {
		return "its HEAD, its index or a file in it changed at " + changed.UTC().Format(time.RFC3339) + ", within the hour a worktree is kept after its last change"
	}
	return ""
}

// lastChange is when the worktree at path last changed: its HEAD, its index
// or its HEAD's reflog in its own git folder, which a commit, a checkout or a
// git add writes, or a file in it git sees, tracked or untracked. An ignored
// file is left out: it is a build's output or a dependency, which a process
// working in the worktree writes, and walking a node_modules every sweep is
// what reading it would cost. It stops at the first change within
// activityGrace, which is all inUse asks of it.
func (c Collector) lastChange(ctx context.Context, path string) (time.Time, error) {
	gitDir, err := c.gitOutput(ctx, path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return time.Time{}, err
	}
	gitDir = strings.TrimSpace(gitDir)
	var latest time.Time
	for _, name := range []string{"HEAD", "index", filepath.Join("logs", "HEAD")} {
		info, err := os.Stat(filepath.Join(gitDir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return time.Time{}, err
		}
		if time.Since(info.ModTime()) < activityGrace {
			return info.ModTime(), nil
		}
		latest = later(latest, info.ModTime())
	}
	files, err := c.gitOutput(ctx, path, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return time.Time{}, err
	}
	for _, name := range strings.Split(files, "\x00") {
		if name == "" {
			continue
		}
		info, err := os.Lstat(filepath.Join(path, filepath.FromSlash(name)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return time.Time{}, err
		}
		if time.Since(info.ModTime()) < activityGrace {
			return info.ModTime(), nil
		}
		latest = later(latest, info.ModTime())
	}
	return latest, nil
}

func (c Collector) gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	result, err := c.Commands.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: args})
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("git %s exited with code %d: %s", args[0], result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return string(result.Stdout), nil
}

func later(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}

// folderChain is folder and every folder above it, each by its directory
// identity, empty when folder itself cannot be read.
func folderChain(folder string) []os.FileInfo {
	var chain []os.FileInfo
	for current := filepath.Clean(folder); ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err != nil && len(chain) == 0 {
			return nil
		}
		if err == nil {
			chain = append(chain, info)
		}
		if filepath.Dir(current) == current {
			return chain
		}
	}
}

// holds reports whether folder is in chain: the folder chain starts at, or
// one above it.
func holds(chain []os.FileInfo, folder os.FileInfo) bool {
	return slices.ContainsFunc(chain, func(info os.FileInfo) bool { return os.SameFile(info, folder) })
}
