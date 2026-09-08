//go:build !windows

package pipeline

import "errors"

func lockDaemon(string) (func() error, error) {
	return nil, errors.New("pipeline: idle config-apply currently supports Windows only")
}
