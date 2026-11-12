//go:build !windows

package janitor

import (
	"context"
	"errors"
	"fmt"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/reap"
)

// errProcessesNotPorted is what the process sweep's readers return on this
// system: reading the machine's processes is not ported to it yet.
var errProcessesNotPorted = fmt.Errorf("janitor: reading the machine's processes is not ported to this system yet: %w", errors.ErrUnsupported)

// ReadProcesses refuses, since reading processes is not ported yet.
func ReadProcesses(ctx context.Context) ([]Process, error) {
	return nil, errProcessesNotPorted
}

// Sightings refuses each reading, as ReadProcesses does.
func Sightings(h home.Home) func(context.Context) ([]reap.Sighting, []string, error) {
	return func(context.Context) ([]reap.Sighting, []string, error) {
		return nil, nil, errProcessesNotPorted
	}
}

// ReadOwners reads no terminal, as Owners does.
func ReadOwners(ctx context.Context, h home.Home, tree *fleettree.Reader) ([]Owner, []string) {
	return Owners(ctx, h, tree)
}

// Owners reads no terminal and says why, so nothing is ended on any
// terminal's account.
func Owners(ctx context.Context, h home.Home, tree *fleettree.Reader) ([]Owner, []string) {
	return nil, []string{"the terminals were not read, so no process was judged: " + errProcessesNotPorted.Error()}
}

// EndProcess refuses and ends nothing, since ending a process proven by its
// start time is not ported yet.
func EndProcess(ctx context.Context, item ProcessItem) error {
	return fmt.Errorf("janitor: ending process %d is not ported to this system yet: %w", item.PID, errors.ErrUnsupported)
}
