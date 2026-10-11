//go:build !windows

package install

// AppsEntryKey has no counterpart off Windows, which has no list of
// installed apps to add Code Goblins to.
const AppsEntryKey = ""

// UninstallArgument is what the desktop window is started with to show the
// uninstall.
const UninstallArgument = "--uninstall"

func (s Service) keepAppsEntry(*reporter) error { return nil }

// AppsEntryThere finds no entry off Windows.
func (s Service) AppsEntryThere() bool { return false }

// RemoveAppsEntry has nothing to remove off Windows.
func (s Service) RemoveAppsEntry() error { return nil }
