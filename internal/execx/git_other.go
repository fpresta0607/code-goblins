//go:build !windows

package execx

// realGit is path: only Git for Windows runs git through a launcher.
func realGit(path string) string { return path }
