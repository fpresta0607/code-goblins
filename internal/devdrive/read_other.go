//go:build !windows

package devdrive

import (
	"context"
	"errors"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Read reports that Dev Drives are a Windows feature.
func Read(context.Context, execx.Runner) (Machine, error) {
	return Machine{}, errors.New("devdrive: Dev Drives are a Windows feature")
}

// BootTime is the zero time: no Dev Drive is attached at boot here.
func BootTime() time.Time { return time.Time{} }
