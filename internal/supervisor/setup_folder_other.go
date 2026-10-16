//go:build !windows

package supervisor

import (
	"context"
	"errors"
)

func pickProjectsFolder(context.Context, string) (string, error) {
	return "", errors.New("folder selection requires Windows")
}
