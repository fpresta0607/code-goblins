//go:build windows

package install

import "golang.org/x/sys/windows"

// userDesktop is the folder Windows shows as the user's desktop, wherever it
// keeps it, OneDrive included, or nothing when Windows cannot say.
func userDesktop() string {
	folder, err := windows.KnownFolderPath(windows.FOLDERID_Desktop, 0)
	if err != nil {
		return ""
	}
	return folder
}
