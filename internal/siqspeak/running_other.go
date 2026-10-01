//go:build !windows

package siqspeak

import "errors"

func running() (bool, error) {
	return false, errors.New("SIQspeak requires Windows")
}
