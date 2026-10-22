//go:build !windows

package clipboard

import "errors"

func set(_, _ []byte) error {
	return errors.New("a picture can be put on the clipboard only on Windows")
}
