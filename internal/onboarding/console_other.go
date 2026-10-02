//go:build !windows

package onboarding

import "io"

// ChooseConsole reads a console only on Windows, the fleet's platform.
func ChooseConsole(io.Writer, string, []Choice, int) (int, error) {
	return 0, ErrNoConsole
}
