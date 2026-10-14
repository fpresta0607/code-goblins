package cirunner

import "golang.org/x/sys/windows"

// systemFolder is Windows' own folder, as Windows gives it, never as a
// variable a user can set says.
func systemFolder() (string, error) {
	return windows.GetSystemWindowsDirectory()
}
