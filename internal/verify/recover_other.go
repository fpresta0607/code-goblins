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

func currentController() (lock.Info, error) {
	return lock.Info{}, errNoRecovery
}

func (Custody) endJob(context.Context) (execx.TreeUsage, bool, error) {
	return execx.TreeUsage{}, false, errNoRecovery
}

func (Custody) strayWorkers(context.Context) ([]proc.Entry, error) {
	return nil, errNoRecovery
}
