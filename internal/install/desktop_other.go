//go:build !windows

package install

// userDesktop has no counterpart off Windows, where the install script puts no
// desktop shortcut.
func userDesktop() string {
	return ""
}
