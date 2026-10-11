//go:build !windows

package uninstall

import (
	"context"
	"errors"
)

// RunAsAdministrator is Windows' alone.
func RunAsAdministrator(context.Context, string) ([]string, error) {
	return nil, errors.New("an administrator's part runs on Windows")
}

// RemoveOnceEnded is Windows' alone.
func RemoveOnceEnded(string) error {
	return errors.New("removing a running program's folder is Windows'")
}
