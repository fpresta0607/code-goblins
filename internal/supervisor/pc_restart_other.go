//go:build !windows

package supervisor

import "errors"

// RestartWindows restarts only Windows, the fleet's platform.
func RestartWindows() error {
	return errors.New("only Windows is restarted")
}

// EndHeavyRuns ends runs only on Windows, the fleet's platform.
func EndHeavyRuns() ([]string, error) {
	return nil, errors.New("the heavy line's runs are ended only on Windows")
}
