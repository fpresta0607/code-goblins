//go:build !windows

package cutover

import "errors"

// RunningPrograms are the programs running on this machine, which only
// Windows, where a home runs, reports here.
type RunningPrograms struct{}

func (RunningPrograms) Running() ([]Process, error) {
	return nil, errors.New("reading what runs needs Windows")
}
