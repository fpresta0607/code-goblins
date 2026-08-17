//go:build !windows

package auth

import "errors"

// openCredentialManager has no non-Windows equivalent, so the file store is
// the only store off Windows.
func openCredentialManager() (Store, error) {
	return nil, errors.New("auth: Windows Credential Manager is unavailable on this platform")
}

// restrictToOwner is a no-op: os.WriteFile already applied 0600.
func restrictToOwner(string) error {
	return nil
}
