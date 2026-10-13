//go:build !windows

package execx

import (
	"context"
	"errors"
)

func (*Tree) Usage() (TreeUsage, error) {
	return TreeUsage{}, errors.New("execx: whole-tree accounting requires Windows")
}

func (*Tree) Members() ([]EndedProcess, error) {
	return nil, errors.New("execx: job members require Windows")
}

func (*Tree) TerminateAndWait(context.Context) (TreeUsage, error) {
	return TreeUsage{}, errors.New("execx: verified whole-tree termination requires Windows")
}

func (*Tree) EndAndWait(context.Context) (TreeUsage, []EndedProcess, error) {
	return TreeUsage{}, nil, errors.New("execx: verified whole-tree termination requires Windows")
}
