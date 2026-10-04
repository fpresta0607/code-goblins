//go:build !windows

package install

// StartAtLoginKey has no counterpart off Windows, where there is no desktop
// window to start at login.
const StartAtLoginKey = ""

func (s Service) removeStartAtLogin(*reporter) error { return nil }

func (s Service) adoptStartAtLogin(*reporter) error { return nil }
