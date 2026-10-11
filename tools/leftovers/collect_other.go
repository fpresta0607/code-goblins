//go:build !windows

package main

import "errors"

// options are what one snapshot reads beyond what every snapshot does.
type options struct {
	also, content []string
}

// collect is Windows' alone: Code Goblins installs on Windows.
func collect(options) (Snapshot, error) {
	return Snapshot{}, errors.New("a snapshot reads a Windows profile")
}
