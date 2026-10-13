//go:build !windows

package execx

import (
	"context"
	"errors"
)

func (*Tree) Usage() (TreeUsage, error) {
	return TreeUsage{}, errors.New("execx: whole-tree accounting requires Windows")
}

func (*Tree) TerminateAndWait(context.Context) (TreeUsage, error) {
	return TreeUsage{}, errors.New("execx: verified whole-tree termination requires Windows")
}
