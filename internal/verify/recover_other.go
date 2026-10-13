//go:build !windows

package verify

import (
	"context"
	"errors"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

var errNoRecovery = errors.New("verify: execution custody requires Windows")

func currentController() (lock.Info, uint32, error) {
	return lock.Info{}, 0, errNoRecovery
}

func (Custody) endJob(context.Context) (execx.TreeUsage, bool, error) {
	return execx.TreeUsage{}, false, errNoRecovery
}

func (Custody) strayWorkers() ([]proc.Entry, error) {
	return nil, errNoRecovery
}
