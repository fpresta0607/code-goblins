package fsx

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// removeAll is os.RemoveAll, which deletes each file with Windows' image
// section check forced: it refuses every name of a file a process has loaded
// as a program or a DLL, through whichever name the process loaded it. A
// name it is refused is deleted again as DeleteFile deletes it, without that
// check, and the removal goes on. That takes a name another hard link keeps
// the file alive behind, as uv's links into its cache do, and Windows still
// refuses the last name of a loaded file, which is a process running the
// folder's own copy. Each pass deletes the name the one before was refused,
// so the passes end once nothing is refused or a refused name stays.
func removeAll(path string) error {
	for {
		err := os.RemoveAll(path)
		var refused *os.PathError
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || !errors.As(err, &refused) || os.Remove(refused.Path) != nil {
			return err
		}
	}
}
