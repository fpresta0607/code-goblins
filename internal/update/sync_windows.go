package update

import (
	"os"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// renameSynced renames temporary over path with the rename written through.
//
// Windows replaces no file while anyone has it open, even a reader that
// shares it for deletion, and the journal has readers: the update that bounds
// the work reads it every quarter second until it says prepared, the janitor
// reads it on its sweeps, and a virus scanner reads a file it just saw. So
// the rename waits a reader out, and one refused to the end names both files,
// where the system's own "Access is denied." names none.
func renameSynced(temporary, path string) error {
	from, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	err = fsx.WaitOut(func() error {
		return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
	})
	if err != nil {
		os.Remove(temporary)
		return &os.LinkError{Op: "rename", Old: temporary, New: path, Err: err}
	}
	return nil
}
