//go:build !windows

package supervisor

import "errors"

// MachineMemory is read only on Windows, the fleet's platform.
func MachineMemory() (available, total uint64, err error) {
	return 0, 0, errors.New("free memory is read only on Windows")
}
