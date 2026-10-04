package verify

import "golang.org/x/sys/windows"

func admissionCacheDir() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
}
