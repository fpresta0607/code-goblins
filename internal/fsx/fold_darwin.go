package fsx

import (
	"errors"
	"io/fs"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// pcCaseSensitive is pathconf's _PC_CASE_SENSITIVE in <sys/unistd.h>, which
// golang.org/x/sys/unix does not name.
const pcCaseSensitive = 11

// foldsCase reports whether the volume that holds path ignores case, as a
// Mac's default volume does and a volume formatted case-sensitive does not.
// A path that does not exist yet is on the volume of its nearest folder that
// does. A volume that cannot be read is taken to ignore case, as the default
// does.
func foldsCase(path string) bool {
	path, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	for {
		sensitive, err := unix.Pathconf(path, pcCaseSensitive)
		if err == nil {
			return sensitive == 0
		}
		parent := filepath.Dir(path)
		if !errors.Is(err, fs.ErrNotExist) || parent == path {
			return true
		}
		path = parent
	}
}
