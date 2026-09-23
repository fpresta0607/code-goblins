//go:build !windows

package fsx

// LongPath returns path unchanged: only Windows has 8.3 short names.
func LongPath(path string) string {
	return path
}
