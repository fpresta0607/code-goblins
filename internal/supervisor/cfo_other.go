//go:build !windows

package supervisor

import (
	"errors"
	"os"
)

func openPrimary(string) (*os.File, error) {
	return nil, errors.New("verified primary CFO transport requires Windows")
}
