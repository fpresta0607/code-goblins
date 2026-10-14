package browserlogin

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// Executable is Google Chrome's chrome.exe: where its installer registered
// it under App Paths, for this user or the machine, or else a folder it
// installs to. It is found from those alone: running chrome.exe to ask would
// hand the call to the Overlord's own Chrome, which opens a window.
func Executable() (string, error) {
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		key, err := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		path, _, err := key.GetStringValue("")
		key.Close()
		if err == nil && isFile(path) {
			return path, nil
		}
	}
	for _, folder := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
		if folder == "" {
			continue
		}
		if path := filepath.Join(folder, "Google", "Chrome", "Application", "chrome.exe"); isFile(path) {
			return path, nil
		}
	}
	return "", errors.New("Google Chrome is not installed on this PC, and browser logins run in Chrome")
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
