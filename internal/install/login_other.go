//go:build !windows

package install

import (
	"errors"
	"path/filepath"
)

// StartAtLoginKey has no counterpart off Windows, where there is no desktop
// window to start at login.
const StartAtLoginKey = ""

func (s Service) removeStartAtLogin(*reporter) error { return nil }

func (s Service) adoptStartAtLogin(*reporter) error { return nil }

// keepStartAtLogin keeps a choice made with --start-at-login in the home,
// which has no entry to set off Windows.
func (s Service) keepStartAtLogin(*reporter) error {
	if s.StartAtLogin == "" {
		return nil
	}
	return WriteStartAtLogin(filepath.Join(s.Root, "state"), s.StartAtLogin)
}

// StartsAtLogin says Start at login is a Windows setting.
func (s Service) StartsAtLogin() (bool, string, error) {
	return false, "Start at login is a Windows setting", nil
}

// SetStartsAtLogin keeps the choice in the home, which has no entry to set
// off Windows.
func (s Service) SetStartsAtLogin(on bool) error {
	if !on {
		return WriteStartAtLogin(filepath.Join(s.Root, "state"), StartAtLoginOff)
	}
	return errors.New("Start at login is a Windows setting")
}
