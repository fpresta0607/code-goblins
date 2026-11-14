//go:build linux || darwin

package fsx

import (
	"errors"
	"os"
)

// SyncFolder puts folder's own entries on disk, so a file just renamed into
// it keeps its new name after the machine stops: a rename is a change of the
// folder, which syncing the file alone does not write. Windows needs none,
// where a rename is written through instead (MOVEFILE_WRITE_THROUGH).
func SyncFolder(folder string) error {
	handle, err := os.Open(folder)
	if err != nil {
		return err
	}
	return errors.Join(handle.Sync(), handle.Close())
}
