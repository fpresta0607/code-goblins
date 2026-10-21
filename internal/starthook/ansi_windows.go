//go:build windows

package starthook

import (
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// ansiCodePage is CP_ACP, the system's ANSI code page, which Python's locale
// encoding is on Windows.
const ansiCodePage = 0

// ansi decodes data in the system's ANSI code page, as a Python that reads a
// .pth file in its locale encoding decodes it, and reports whether it could.
func ansi(data []byte) (string, bool) {
	if len(data) == 0 {
		return "", false
	}
	size, err := windows.MultiByteToWideChar(ansiCodePage, 0, &data[0], int32(len(data)), nil, 0)
	if err != nil || size == 0 {
		return "", false
	}
	wide := make([]uint16, size)
	if _, err := windows.MultiByteToWideChar(ansiCodePage, 0, &data[0], int32(len(data)), &wide[0], size); err != nil {
		return "", false
	}
	// Decoded whole: a NUL byte is a character to Python, and the text after
	// it is still read.
	return string(utf16.Decode(wide)), true
}
