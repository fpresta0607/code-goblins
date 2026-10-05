package gatetest

import (
	"path"
	"strings"
)

// matches reports whether a policy's path pattern names file, a slash path
// from the repository root. Each segment of the pattern matches one segment
// of the path as path.Match matches it, ** matches any number of segments,
// none included, and case does not count, as Windows names files. A pattern
// path.Match refuses matches nothing.
func matches(pattern, file string) bool {
	return matchSegments(strings.Split(strings.ToLower(pattern), "/"), strings.Split(strings.ToLower(file), "/"))
}

func matchSegments(pattern, names []string) bool {
	if len(pattern) == 0 {
		return len(names) == 0
	}
	if pattern[0] == "**" {
		for skipped := 0; skipped <= len(names); skipped++ {
			if matchSegments(pattern[1:], names[skipped:]) {
				return true
			}
		}
		return false
	}
	if len(names) == 0 {
		return false
	}
	matched, err := path.Match(pattern[0], names[0])
	return err == nil && matched && matchSegments(pattern[1:], names[1:])
}
