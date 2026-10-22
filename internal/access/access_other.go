//go:build !windows

package access

// Off Windows a folder takes no grant to Authenticated Users from the drive
// it is on, so there is nothing to narrow.

// Narrow changes nothing.
func Narrow(string) error { return nil }

// IsNarrow reports every folder narrowed.
func IsNarrow(string) (bool, error) { return true, nil }
