//go:build !windows

package cirunner

// checkFolder has nothing to check where the service, a Windows service,
// never runs.
func checkFolder(string) error {
	return nil
}
