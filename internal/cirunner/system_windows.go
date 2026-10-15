package cirunner

import "golang.org/x/sys/windows"

// systemFolder is Windows' own folder, as Windows gives it, never as a
// variable a user can set says.
func systemFolder() (string, error) {
	return windows.GetSystemWindowsDirectory()
}

// programFilesFolder is Program Files, as Windows gives it, which only
// Administrators may change.
func programFilesFolder() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
}
