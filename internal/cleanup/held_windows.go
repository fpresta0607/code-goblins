package cleanup

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isHeld says a removal failed because another process holds what it would
// remove: a file or folder it has open without delete sharing, as a working
// directory is, or an image it has loaded from a file no other hard link
// keeps.
func isHeld(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
