//go:build !windows

package main

import "io"

// readHiddenLine reads nothing off Windows, where stdin is read as before.
func readHiddenLine(io.Writer, string) (string, bool, error) {
	return "", false, nil
}
