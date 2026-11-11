package sandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

// RunsDir is the folder under a task's tasktmp folder that holds a record of
// each container goblins sandbox run started for it, by the container's name,
// from before the container can start until it was removed. No container
// mounts it.
const RunsDir = "sandbox-runs"

// RunningContainerRefusal refuses a host git of the sandbox task whose
// tasktmp folder is taskTmp while a container of its goblins sandbox run may
// run: one could make a repository in the worktree after the walk for one
// (NestedGitRefusal) and before host git reads it. A record whose run ended
// without removing its container refuses too, until the next goblins sandbox
// run removes that container, as does a record that cannot be read.
func RunningContainerRefusal(taskTmp string) error {
	if !filepath.IsAbs(taskTmp) {
		return fmt.Errorf("sandbox: the task's record names no tasktmp folder, where the records of its containers are kept, but %q", taskTmp)
	}
	dir := filepath.Join(taskTmp, RunsDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sandbox: read the records of the task's containers in %s: %w", dir, err)
	}
	for _, entry := range entries {
		// A record is written beside its place and renamed into it.
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		holder, err := lock.ReadNamed(dir, entry.Name())
		if err != nil {
			return fmt.Errorf("sandbox: the container %s of this task may still run, and its record in %s cannot be read: %w", entry.Name(), dir, err)
		}
		if holder.Alive() {
			return fmt.Errorf("sandbox: the container %s of this task runs, from goblins sandbox run (pid %d), and could make a repository host git would read: run host git once that run ended", entry.Name(), holder.PID)
		}
		return fmt.Errorf("sandbox: the container %s of this task may still run, since its goblins sandbox run (pid %d) ended without removing it: the next goblins sandbox run removes it, and host git runs once it did", entry.Name(), holder.PID)
	}
	return nil
}
