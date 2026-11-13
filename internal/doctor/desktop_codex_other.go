//go:build !windows

package doctor

// DesktopCodex reports the Codex desktop app as not installed: the app this
// looks for is a Windows package.
func DesktopCodex() (string, bool, error) {
	return "", false, nil
}
