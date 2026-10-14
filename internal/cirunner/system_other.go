//go:build !windows

package cirunner

import "errors"

// systemFolder has no Windows folder to give where Hyper-V never runs.
func systemFolder() (string, error) {
	return "", errors.New("cirunner: Hyper-V runs only on Windows")
}
