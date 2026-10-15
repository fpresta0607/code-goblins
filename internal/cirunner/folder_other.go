//go:build !windows

package cirunner

import "errors"

// checkFolder has nothing to check where the service, a Windows service,
// never runs.
func checkFolder(string) error {
	return nil
}

// checkInstalledFolder refuses every folder where the install, which makes
// a Windows service, never runs.
func checkInstalledFolder(string) error {
	return errors.New("cirunner: the install runs only on Windows")
}
