//go:build !windows

package pipeline

import "errors"

func replaceFile(string, string) error {
	return errors.New("pipeline: idle config-apply currently supports Windows only")
}
