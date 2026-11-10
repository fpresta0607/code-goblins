//go:build !windows

package gatehome

import "os"

// linkKind says the link at path is a symbolic link: junctions are Windows'
// own.
func linkKind(path string) (isJunction, isDirectory bool, err error) {
	_, err = os.Lstat(path)
	return false, false, err
}

// createLink makes a symbolic link at path that leads to target.
func createLink(path, target string, _, _ bool) error {
	return os.Symlink(target, path)
}
