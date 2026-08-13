package fsx

import (
	"fmt"
	"path/filepath"
	"strings"
)

// AbsClean returns an absolute, cleaned path. Command output may carry a
// trailing line ending, which is removed before path processing.
func AbsClean(path string) (string, error) {
	path = normalizeInput(path)
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// Canonical returns the absolute, cleaned physical path after resolving
// symlinks. Failure to resolve the path is returned so callers cannot use an
// unresolved spelling as proof of identity.
func Canonical(path string) (string, error) {
	abs, err := AbsClean(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("fsx: resolve %q: %w", path, err)
	}
	return AbsClean(resolved)
}

// SamePath reports whether two existing paths resolve to the same
// case-insensitive Windows identity.
func SamePath(left, right string) bool {
	left, err := Canonical(left)
	if err != nil {
		return false
	}
	right, err = Canonical(right)
	if err != nil {
		return false
	}
	return strings.EqualFold(normalizeIdentity(left), normalizeIdentity(right))
}

func normalizeInput(path string) string {
	path = strings.TrimRight(path, "\r\n")
	path = filepath.FromSlash(path)
	return stripLongPathPrefix(path)
}

func normalizeIdentity(path string) string {
	return filepath.Clean(stripLongPathPrefix(filepath.FromSlash(path)))
}

func stripLongPathPrefix(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasPrefix(lower, `\\?\unc\`):
		return `\\` + path[len(`\\?\UNC\`):]
	case strings.HasPrefix(lower, `\\?\`):
		return path[len(`\\?\`):]
	}
	return path
}
