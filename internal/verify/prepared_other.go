//go:build !windows

package verify

import "os"

func isPreparedReparsePoint(info os.FileInfo) bool {
	return info.Mode()&os.ModeIrregular != 0
}
