//go:build !windows && !linux && !darwin

package onboarding

import "io"

// AskConsole reads a console only on Windows, Linux and macOS.
func AskConsole(io.Writer, Step) (int, error) {
	return 0, ErrNoConsole
}

// ConsoleWidth is known only on Windows, Linux and macOS.
func ConsoleWidth() int {
	return 0
}
