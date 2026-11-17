package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// currentController identifies this process as the one that starts an
// execution's workers, by its start and, where the system reads one, its
// instance, which a record names its process by there (lock.Info.Instance):
// a record without it reads as another program's, so its controller as gone.
func currentController() (lock.Info, error) {
	pid := os.Getpid()
	start, isKnown := proc.StartTime(pid)
	if !isKnown {
		return lock.Info{}, errors.New("verify: this process's start time cannot be read")
	}
	instance, err := instanceOf(pid)
	if err != nil {
		return lock.Info{}, fmt.Errorf("verify: read which run of its program this process is: %w", err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		return lock.Info{}, fmt.Errorf("verify: read this machine's name: %w", err)
	}
	return lock.Info{PID: pid, OwnerPID: pid, Start: start, Instance: instance, Hostname: hostname, Acquired: time.Now().UTC()}, nil
}

// endJob ends what still runs in an abandoned execution's job.
func (guard Custody) endJob(ctx context.Context) (execx.TreeUsage, bool, error) {
	usage, isFound, err := execx.EndNamedJob(ctx, guard.Job)
	if err != nil {
		return usage, isFound, fmt.Errorf("prove its job empty: %w", err)
	}
	return usage, isFound, nil
}
