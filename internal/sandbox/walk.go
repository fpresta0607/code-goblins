package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// walkTokenFile and cleanMarkFile are the files under a sandbox task's
// tasktmp folder, which no container mounts and no goblin's path boundary
// lets it write, that spare its host commands the walk for a .git the
// container made (NestedGitRefusal): the walk token, which every start of a
// container and every host command that may write the worktree replaces,
// and the token of the last walk that found none.
const (
	walkTokenFile = "sandbox-walk"
	cleanMarkFile = "sandbox-clean"
)

// ForgetNestedGitWalk makes the next host command of the sandbox task whose
// tasktmp folder is taskTmp walk its worktree again, by replacing its walk
// token with one no clean mark holds.
func ForgetNestedGitWalk(taskTmp string) error {
	_, err := newWalkToken(taskTmp)
	return err
}

// WalkForNestedGit walks worktree once for a .git the container made
// (NestedGitRefusal) and, when it finds none, marks the worktree clean under
// the walk token it replaced before the walk. A container started or a
// command that may write the worktree run during the walk replaces that
// token, so the mark this walk writes then marks nothing clean.
func WalkForNestedGit(taskTmp, worktree string) error {
	token, err := newWalkToken(taskTmp)
	if err != nil {
		return err
	}
	if err := NestedGitRefusal(worktree); err != nil {
		return err
	}
	if err := fsx.AtomicWriteFile(filepath.Join(taskTmp, cleanMarkFile), []byte(token)); err != nil {
		return fmt.Errorf("sandbox: record that the worktree holds no .git the container made: %w", err)
	}
	return nil
}

// NestedGitCheck refuses a host git of the sandbox task whose tasktmp folder
// is taskTmp while worktree holds a .git the container made, as
// NestedGitRefusal does, walking it only when no walk since the walk token
// last changed found it clean (WalkForNestedGit).
func NestedGitCheck(taskTmp, worktree string) error {
	if filepath.IsAbs(taskTmp) {
		token, err := os.ReadFile(filepath.Join(taskTmp, walkTokenFile))
		if err == nil {
			if mark, err := os.ReadFile(filepath.Join(taskTmp, cleanMarkFile)); err == nil && string(mark) == string(token) {
				return nil
			}
		}
	}
	return WalkForNestedGit(taskTmp, worktree)
}

// newWalkToken replaces the walk token in taskTmp with a fresh one, which it
// returns.
func newWalkToken(taskTmp string) (string, error) {
	if !filepath.IsAbs(taskTmp) {
		return "", fmt.Errorf("sandbox: the task's record names no tasktmp folder, where the walk for a .git the container made is recorded, but %q", taskTmp)
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("sandbox: make a token for the walk for a .git the container made: %w", err)
	}
	token := hex.EncodeToString(random)
	if err := os.MkdirAll(taskTmp, 0o755); err != nil {
		return "", fmt.Errorf("sandbox: make the task's tasktmp folder: %w", err)
	}
	if err := fsx.AtomicWriteFile(filepath.Join(taskTmp, walkTokenFile), []byte(token)); err != nil {
		return "", fmt.Errorf("sandbox: record a new walk for a .git the container made: %w", err)
	}
	return token, nil
}
