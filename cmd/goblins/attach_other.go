//go:build !windows

package main

import (
	"fmt"
	"io"
)

// attachNative refuses: native terminals run only on Windows so far.
func attachNative(stateDir, id string, stdout, stderr io.Writer) int {
	fmt.Fprintf(stderr, "goblins attach: native terminal %s cannot be shown, since native terminals are not ported to this system yet\n", id)
	return 1
}
