//go:build linux || darwin

package update

import (
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// renameSynced renames temporary over path and then puts the folder's new
// entry on disk, since the rename is a change of the folder the file's own
// sync does not write. A reader holding path open keeps no rename from it
// here, so nothing is waited out.
func renameSynced(temporary, path string) error {
	if err := os.Rename(temporary, path); err != nil {
		os.Remove(temporary)
		return err
	}
	return fsx.SyncDirectory(filepath.Dir(path))
}
