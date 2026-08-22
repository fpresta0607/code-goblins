//go:build !windows

package auth

import (
	"os"
	"syscall"
)

// hardLinkCount reports how many directory entries point at one file's data.
// A platform whose stat does not carry a link count reports one, which is the
// answer that keeps adoption working rather than disabling it everywhere the
// count cannot be read.
func hardLinkCount(path string) (uint32, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 1, nil
	}
	return uint32(stat.Nlink), nil
}
