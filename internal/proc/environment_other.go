//go:build !windows && !linux

package proc

import "errors"

func Environment(pid int) ([]string, error) {
	return nil, errors.New("process environment is available only on Windows")
}
