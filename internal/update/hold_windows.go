package update

import (
	"os"

	"golang.org/x/sys/windows"
)

// Hold opens path so that nothing can write, replace or delete it until the
// returned file is closed, and hashes its content through that handle. A
// program started from path while it is held runs exactly what was hashed.
func Hold(path string) (*os.File, string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, "", err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, "", err
	}
	file := os.NewFile(uintptr(handle), path)
	hash, err := hashOf(file)
	if err != nil {
		file.Close()
		return nil, "", err
	}
	return file, hash, nil
}
