//go:build !windows

package home

import (
	"regexp"
	"strings"
)

// windowsDrive is a drive that begins a path anywhere in a fixture, which may
// be a whole command line.
var windowsDrive = regexp.MustCompile(`\b([A-Za-z]):[\\/]`)

// systemPath is a fixture path written for Windows, as this system spells it:
// a drive becomes a folder at the root named by its letter, and every
// backslash a slash, so C:\dev and C:/dev are both /C/dev.
func systemPath(windowsPath string) string {
	return strings.ReplaceAll(windowsDrive.ReplaceAllString(windowsPath, "/${1}/"), `\`, "/")
}
