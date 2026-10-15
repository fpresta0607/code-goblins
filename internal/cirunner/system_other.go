//go:build !windows

package cirunner

import "errors"

// systemFolder has no Windows folder to give where Hyper-V never runs.
func systemFolder() (string, error) {
	return "", errors.New("cirunner: Hyper-V runs only on Windows")
}

// programFilesFolder has no Program Files to give where the service never
// runs.
func programFilesFolder() (string, error) {
	return "", errors.New("cirunner: the service runs only on Windows")
}
