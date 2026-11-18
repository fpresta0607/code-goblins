//go:build linux || darwin

package pipeline

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// replaceFile renames staged over destination as ReplaceFileW does on
// Windows: the result keeps destination's mode, which the other accounts on
// the machine read the shared config through, rather than the staged file's
// owner-only one, and the file's data and its new name are on disk before it
// returns, so an interrupted apply leaves either config whole.
func replaceFile(destination, staged string) error {
	live, err := os.Stat(destination)
	if err != nil {
		return fmt.Errorf("pipeline: replacing %s: %w", destination, err)
	}
	file, err := os.OpenFile(staged, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("pipeline: replacing %s: %w", destination, err)
	}
	if err := errors.Join(file.Chmod(live.Mode().Perm()), file.Sync(), file.Close()); err != nil {
		return fmt.Errorf("pipeline: replacing %s: %w", destination, err)
	}
	if err := os.Rename(staged, destination); err != nil {
		return fmt.Errorf("pipeline: replacing %s: %w", destination, err)
	}
	return fsx.SyncDirectory(filepath.Dir(destination))
}
