//go:build windows

package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// AppsEntryKey is where Windows keeps Code Goblins' entry in its list of
// installed apps, under HKEY_CURRENT_USER, as it keeps any program installed
// for one user.
const AppsEntryKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\CodeGoblins`

// UninstallArgument is what the desktop window is started with from
// Installed apps to show the uninstall.
const UninstallArgument = "--uninstall"

// keepAppsEntry gives Code Goblins its entry in Windows' Installed apps,
// whose Uninstall runs the same uninstall as goblins uninstall: in the
// desktop window where the home holds one, else in a terminal. Both installs
// make it, the setup program's and the one-line install's, since a person
// looks for it there whichever they used.
func (s Service) keepAppsEntry(report *reporter) error {
	if s.AppsEntryKey == "" {
		return nil
	}
	icon := filepath.Join(s.bin(), "goblins.exe")
	uninstall := `"` + icon + `" uninstall`
	if window := filepath.Join(s.bin(), windowName); fileThere(window) {
		icon = window
		uninstall = `"` + window + `" ` + UninstallArgument
	}
	values := map[string]string{
		"DisplayName":          "Code Goblins",
		"Publisher":            "Code Goblins",
		"DisplayIcon":          icon,
		"InstallLocation":      s.Root,
		"UninstallString":      uninstall,
		"QuietUninstallString": `"` + filepath.Join(s.bin(), "goblins.exe") + `" uninstall --yes`,
	}
	if s.Version != "" {
		values["DisplayVersion"] = s.Version
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, s.AppsEntryKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("install: add Code Goblins to Installed apps: %w", err)
	}
	defer key.Close()
	changed := false
	for name, value := range values {
		if current, _, err := key.GetStringValue(name); err == nil && current == value {
			continue
		}
		if err := key.SetStringValue(name, value); err != nil {
			return fmt.Errorf("install: add Code Goblins to Installed apps: %w", err)
		}
		changed = true
	}
	for _, name := range []string{"NoModify", "NoRepair"} {
		if current, _, err := key.GetIntegerValue(name); err == nil && current == 1 {
			continue
		}
		if err := key.SetDWordValue(name, 1); err != nil {
			return fmt.Errorf("install: add Code Goblins to Installed apps: %w", err)
		}
		changed = true
	}
	if changed {
		report.change("apps", "Code Goblins is in Installed apps, whose Uninstall runs "+uninstall)
	} else {
		report.same("apps", "Code Goblins is in Installed apps")
	}
	return nil
}

// AppsEntryThere reports whether Code Goblins has its entry in Installed
// apps for this home.
func (s Service) AppsEntryThere() bool {
	if s.AppsEntryKey == "" {
		return false
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, s.AppsEntryKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	location, _, err := key.GetStringValue("InstallLocation")
	return err != nil || sameDirectory(location, s.Root)
}

// RemoveAppsEntry takes Code Goblins out of Installed apps.
func (s Service) RemoveAppsEntry() error {
	if s.AppsEntryKey == "" {
		return nil
	}
	if err := registry.DeleteKey(registry.CURRENT_USER, s.AppsEntryKey); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("install: take Code Goblins out of Installed apps: %w", err)
	}
	// A profile whose first installed app Code Goblins was has the list's
	// own key only because of it, empty once its entry goes.
	parent := filepath.Dir(s.AppsEntryKey)
	if key, err := registry.OpenKey(registry.CURRENT_USER, parent, registry.QUERY_VALUE); err == nil {
		info, err := key.Stat()
		key.Close()
		if err == nil && info.SubKeyCount == 0 && info.ValueCount == 0 {
			_ = registry.DeleteKey(registry.CURRENT_USER, parent)
		}
	}
	return nil
}

func fileThere(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
