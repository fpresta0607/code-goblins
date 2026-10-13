package verify

import (
	"os"
	"syscall"
)

func isPreparedReparsePoint(info os.FileInfo) bool {
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return !ok || attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
