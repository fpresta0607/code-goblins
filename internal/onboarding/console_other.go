//go:build !windows

package onboarding

import "io"

// AskConsole reads a console only on Windows, the fleet's platform.
func AskConsole(io.Writer, Step) (int, error) {
	return 0, ErrNoConsole
}

// ConsoleWidth is known only on Windows, the fleet's platform.
func ConsoleWidth() int {
	return 0
}
