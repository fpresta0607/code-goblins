//go:build !windows

package starthook

// ansi has no ANSI code page to decode in off Windows, where Python reads a
// .pth file as UTF-8.
func ansi([]byte) (string, bool) {
	return "", false
}
