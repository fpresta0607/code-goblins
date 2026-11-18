package fsx

// foldsCase reports whether the file system that holds path ignores case:
// Windows always does.
func foldsCase(string) bool {
	return true
}
