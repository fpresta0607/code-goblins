//go:build !windows

package connections

// userEnvironmentNames is none off Windows, which has no user environment
// kept apart from the process's own.
func userEnvironmentNames() ([]string, error) { return nil, nil }
