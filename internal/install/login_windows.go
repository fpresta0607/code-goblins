//go:build windows

package install

import (
	"errors"
	"fmt"
	"io"
	"os"
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
	if !startsIn(command, s.bin()) && !startsIn(command, s.Root) {
		return nil
	}
	if err := key.DeleteValue(startAtLoginValue); err != nil {
		return fmt.Errorf("install: remove the Start at login entry: %w", err)
	}
	report.change("start at login", "removed the desktop window's entry, which ran "+command)
	return nil
}

// adoptStartAtLogin makes Start at login start this home where it started the
// earlier copy of the desktop window, or this home's root, where an older
// install put the binaries. For a window this install supplied, the entry
// becomes the one that window writes when goblins starts it, the window alone
// for the tray, so the window still shows its Start at login as on. A window
// the home only kept may be from before a window started alone opened the
// app, so the entry runs goblins with --window, which opens any window and is
// the entry such a window writes.
func (s Service) adoptStartAtLogin(report *reporter) error {
	key, command, ok, err := s.startAtLogin()
	if err != nil || !ok {
		return err
	}
	defer key.Close()
	if !(s.EarlierWindow != "" && startsIn(command, s.EarlierWindow)) && !startsIn(command, s.Root) {
		return nil
	}
	adopted := s.startAtLoginCommand()
	if err := key.SetStringValue(startAtLoginValue, adopted); err != nil {
		return fmt.Errorf("install: make Start at login start this home: %w", err)
	}
	report.change("start at login", "now runs "+adopted+", in place of the earlier desktop window")
	return nil
}

// keepStartAtLogin makes Windows start this home at login, in the tray with no
// terminal, unless the person turned Start at login off: then this home's
// entry goes. A choice made with --start-at-login is kept in the home first,
// on a machine with no Start at login entries too. An entry that starts
// another home is left as it is, as is one that already starts this home's
// supervisor at login, through its window alone or through goblins; one of
// this home's that does not, such as a window started on its own board, which
// starts no supervisor, starts it from now on. A home with no desktop window
// has nothing to start out of sight.
func (s Service) keepStartAtLogin(report *reporter) error {
	state := filepath.Join(s.Root, "state")
	if s.StartAtLogin != "" {
		if err := WriteStartAtLogin(state, s.StartAtLogin); err != nil {
			return fmt.Errorf("install: %w", err)
		}
	}
	if s.StartAtLoginKey == "" {
		return nil
	}
	choice, err := ReadStartAtLogin(state)
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}
	if choice == StartAtLoginOff {
		if err := s.removeStartAtLogin(report); err != nil {
			return err
		}
		report.same("start at login", "off, as chosen; the board's Start at login turns it on")
		return nil
	}
	if _, err := os.Stat(filepath.Join(s.bin(), windowName)); err != nil {
		report.same("start at login", "not set: it starts the desktop app, which this home does not hold")
		return nil
	}
	key, current, ok, err := s.startAtLogin()
	if err != nil {
		return err
	}
	if ok {
		defer key.Close()
		if !startsIn(current, s.bin()) && !startsIn(current, s.Root) {
			report.same("start at login", "kept the entry that starts another Code Goblins home: "+current)
			return nil
		}
		for _, starts := range []string{`"` + filepath.Join(s.bin(), windowName) + `" --background`, `"` + filepath.Join(s.bin(), "goblins.exe") + `" --window --background`} {
			if strings.EqualFold(current, starts) {
				report.same("start at login", "Windows starts Code Goblins at login: "+current)
				return nil
			}
		}
	} else {
		key, _, err = registry.CreateKey(registry.CURRENT_USER, s.StartAtLoginKey, registry.QUERY_VALUE|registry.SET_VALUE)
		if err != nil {
			return fmt.Errorf("install: open the Start at login entries: %w", err)
		}
		defer key.Close()
	}
	command := s.startAtLoginCommand()
	if err := key.SetStringValue(startAtLoginValue, command); err != nil {
		return fmt.Errorf("install: make Windows start Code Goblins at login: %w", err)
	}
	report.change("start at login", "Windows starts Code Goblins at login, in the tray: "+command)
	return nil
}

// StartsAtLogin reports whether Windows starts this home at login, as the
// board shows it, or why it cannot be started at login.
func (s Service) StartsAtLogin() (on bool, unavailable string, err error) {
	if _, err := os.Stat(filepath.Join(s.bin(), windowName)); err != nil {
		return false, "Start at login opens the desktop app, which this home does not hold", nil
	}
	key, command, ok, err := s.startAtLogin()
	if err != nil || !ok {
		return false, "", err
	}
	key.Close()
	return startsIn(command, s.bin()), "", nil
}

// SetStartsAtLogin turns Start at login on or off for this home, as the
// board's switch does, and keeps the choice in the home. On, Windows starts
// the home's desktop window alone, in the tray, which starts the supervisor.
func (s Service) SetStartsAtLogin(on bool) error {
	choice := StartAtLoginOff
	if on {
		choice = StartAtLoginOn
	}
	if err := WriteStartAtLogin(filepath.Join(s.Root, "state"), choice); err != nil {
		return err
	}
	if !on {
		return s.removeStartAtLogin(&reporter{out: io.Discard})
	}
	window := filepath.Join(s.bin(), windowName)
	if _, err := os.Stat(window); err != nil {
		return fmt.Errorf("Start at login opens the desktop app, which this home does not hold: %w", err)
	}
	if s.StartAtLoginKey == "" {
		return errors.New("this machine keeps no Start at login entries")
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, s.StartAtLoginKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open the Start at login entries: %w", err)
	}
	defer key.Close()
	return key.SetStringValue(startAtLoginValue, `"`+window+`" --background`)
}
