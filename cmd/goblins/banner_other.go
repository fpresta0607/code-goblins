//go:build !windows

package main

import "io"

// consoleTakesEscapes reports false, so the banner is plain: asking a
// terminal for escape sequences is not ported to this system yet.
func consoleTakesEscapes(out io.Writer) bool {
	return false
}
