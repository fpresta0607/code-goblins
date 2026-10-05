//go:build !windows

package verify

import (
	"os/user"
	"path/filepath"
	"runtime"
)

func admissionCacheDir() (string, error) {
	owner, err := user.Current()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(owner.HomeDir, "Library", "Caches"), nil
	}
	return filepath.Join(owner.HomeDir, ".cache"), nil
}
