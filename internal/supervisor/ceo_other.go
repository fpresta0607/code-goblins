//go:build !windows

package supervisor

import (
	"os"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// openPrimary holds the registration against a replace while a captured
// registration is being used (fsx.OpenHeld), as Windows denies the replace.
func openPrimary(path string) (*os.File, error) {
	return fsx.OpenHeld(path)
}
