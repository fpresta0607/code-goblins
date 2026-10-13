//go:build !windows

package execx

import (
	"context"
	"errors"
	"os/exec"
)

func StartNamedTree(*exec.Cmd, string) (*Tree, error) {
	return nil, errors.New("execx: named verification jobs require Windows")
}

func EndNamedJob(context.Context, string) (TreeUsage, bool, error) {
	return TreeUsage{}, false, errors.New("execx: named verification jobs require Windows")
}
