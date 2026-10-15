//go:build !windows

package cleanup

// isHeld says no removal failed for a hold where a process's open file or
// working directory never stops a removal.
func isHeld(error) bool {
	return false
}
