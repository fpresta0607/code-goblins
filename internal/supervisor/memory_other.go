//go:build !windows

package supervisor

import "errors"

// MachineMemory is read only on Windows, the fleet's platform.
func MachineMemory() (Memory, error) {
	return Memory{}, errors.New("free memory is read only on Windows")
}

// CommitHolders is read only on Windows, the fleet's platform.
func CommitHolders() ([]CommitHolder, error) {
	return nil, errors.New("commit is read only on Windows")
}
