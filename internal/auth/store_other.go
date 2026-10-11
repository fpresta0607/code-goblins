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

// VaultTargets is empty off Windows, which has no Credential Manager.
func VaultTargets() ([]string, error) { return nil, nil }

// DeleteVaultTarget has nothing to delete off Windows.
func DeleteVaultTarget(string) error { return nil }

// VaultHolds holds nothing off Windows.
func VaultHolds(string) (bool, error) { return false, nil }
