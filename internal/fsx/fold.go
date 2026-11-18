package fsx

import "strings"

// EqualPath reports whether two paths, as written, spell the same path by the
// rule of the file system that holds left: without regard to case where it
// ignores case, as Windows and a Mac's default volume do, and exactly where
// it keeps two names that differ only in case apart, as Linux does. It cleans
// nothing and follows no link: SamePath compares what exists.
func EqualPath(left, right string) bool {
	if foldsCase(left) {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// HasPathPrefix reports whether path begins with prefix by EqualPath's rule.
func HasPathPrefix(path, prefix string) bool {
	return len(path) >= len(prefix) && EqualPath(path[:len(prefix)], prefix)
}

// PathKey is path in the form two spellings EqualPath equates share, for a
// map that keys paths: lowercase where the file system ignores case, and as
// written where it does not.
func PathKey(path string) string {
	if foldsCase(path) {
		return strings.ToLower(path)
	}
	return path
}
