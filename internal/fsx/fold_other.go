//go:build !windows && !darwin

package fsx

// foldsCase reports whether the file system that holds path ignores case:
// on Linux two names that differ only in case are two files.
func foldsCase(string) bool {
	return false
}
