//go:build !windows

package devdrive

import (
	"context"
	"errors"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Read reports that Dev Drives are a Windows feature.
func Read(context.Context, execx.Runner) (Machine, error) {
	return Machine{}, errors.New("devdrive: Dev Drives are a Windows feature")
}
