package uninstall

import "github.com/fpresta0607/code-goblins/internal/auth"

// CredentialManager is the Vault of Windows Credential Manager, where Code
// Goblins stores credentials.
type CredentialManager struct{}

// Targets lists the entries Code Goblins stored.
func (CredentialManager) Targets() ([]string, error) { return auth.VaultTargets() }

// Delete removes one, and reads it back.
func (CredentialManager) Delete(target string) error {
	if err := auth.DeleteVaultTarget(target); err != nil {
		return err
	}
	if still, err := auth.VaultHolds(target); err != nil || still {
		if err == nil {
			err = errorString("Windows Credential Manager still holds it")
		}
		return err
	}
	return nil
}

// NoVault is the Vault of a stand-in machine, as a test's is, which holds
// nothing: the real Credential Manager is the machine's own.
type NoVault struct{}

// Targets finds nothing.
func (NoVault) Targets() ([]string, error) { return nil, nil }

// Delete deletes nothing.
func (NoVault) Delete(string) error { return nil }

// errorString is an error that is only its words.
type errorString string

func (e errorString) Error() string { return string(e) }
