//go:build windows || linux || darwin

package host

import "testing"

// setCloseTerminal stands send in for Close's close request for one test.
func setCloseTerminal(t *testing.T, send func(*Client) error) {
	t.Helper()
	previous := closeTerminal
	closeTerminal = send
	t.Cleanup(func() { closeTerminal = previous })
}
