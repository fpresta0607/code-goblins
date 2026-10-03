//go:build windows

package install

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// StartAtLoginKey is where Windows keeps the programs it starts at the user's
// login, under HKEY_CURRENT_USER.
const StartAtLoginKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// startAtLoginValue names the entry the desktop window's Start at login adds
// under StartAtLoginKey; cmd/goblins-window writes it under the same name.
const startAtLoginValue = "CodeGoblins"

// startAtLogin opens the Start at login entries and reads the desktop
// window's. ok is false where there are no entries or the window has none;
// with ok the caller closes the key.
func (s Service) startAtLogin() (key registry.Key, command string, ok bool, err error) {
	if s.StartAtLoginKey == "" {
		return 0, "", false, nil
	}
	key, err = registry.OpenKey(registry.CURRENT_USER, s.StartAtLoginKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, fmt.Errorf("install: open the Start at login entries: %w", err)
	}
	command, _, err = key.GetStringValue(startAtLoginValue)
	if err != nil {
		key.Close()
		if errors.Is(err, registry.ErrNotExist) {
			return 0, "", false, nil
		}
		return 0, "", false, fmt.Errorf("install: read the Start at login entry: %w", err)
	}
	return key, command, true, nil
}

// startsIn reports whether command starts a program in dir. The window writes
// the program it starts in quotes, first.
func startsIn(command, dir string) bool {
	program, _, quoted := strings.Cut(strings.TrimPrefix(command, `"`), `"`)
	return strings.HasPrefix(command, `"`) && quoted && strings.EqualFold(filepath.Dir(program), filepath.Clean(dir))
}

// removeStartAtLogin removes the desktop window's Start at login entry where
// it starts a program in this home, so a home that was uninstalled does not
// start again at the next login. Another home's entry is left as it is.
func (s Service) removeStartAtLogin(report *reporter) error {
	key, command, ok, err := s.startAtLogin()
	if err != nil || !ok {
		return err
	}
	defer key.Close()
	if !startsIn(command, s.Root) {
		return nil
	}
	if err := key.DeleteValue(startAtLoginValue); err != nil {
		return fmt.Errorf("install: remove the Start at login entry: %w", err)
	}
	report.change("start at login", "removed the desktop window's entry, which ran "+command)
	return nil
}

// adoptStartAtLogin makes Start at login start this home where it started the
// earlier copy of the desktop window. For a window this install supplied, the
// entry becomes the one that window writes when goblins starts it, the window
// alone for the tray, so the window still shows its Start at login as on. A
// window the home only kept may be from before a window started alone opened
// the app, so the entry runs goblins with --window, which opens any window and
// is the entry such a window writes.
func (s Service) adoptStartAtLogin(report *reporter) error {
	key, command, ok, err := s.startAtLogin()
	if err != nil || !ok {
		return err
	}
	defer key.Close()
	if !startsIn(command, s.EarlierWindow) {
		return nil
	}
	adopted := `"` + filepath.Join(s.Root, "goblins.exe") + `" --window --background`
	if s.suppliesWindow() {
		adopted = `"` + filepath.Join(s.Root, windowName) + `" --background`
	}
	if err := key.SetStringValue(startAtLoginValue, adopted); err != nil {
		return fmt.Errorf("install: make Start at login start this home: %w", err)
	}
	report.change("start at login", "now runs "+adopted+", in place of the earlier desktop window")
	return nil
}
