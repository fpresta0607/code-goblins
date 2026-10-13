package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// currentController identifies this process as the one that starts an
// execution's workers, and the session its job's name lives in.
func currentController() (lock.Info, uint32, error) {
	pid := os.Getpid()
	start, isKnown := proc.StartTime(pid)
	if !isKnown {
		return lock.Info{}, 0, errors.New("verify: this process's start time cannot be read")
	}
	hostname, err := os.Hostname()
	if err != nil {
		return lock.Info{}, 0, fmt.Errorf("verify: read this machine's name: %w", err)
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(pid), &session); err != nil {
		return lock.Info{}, 0, fmt.Errorf("verify: read this process's session: %w", err)
	}
	return lock.Info{PID: pid, OwnerPID: pid, Start: start, Hostname: hostname, Acquired: time.Now().UTC()}, session, nil
}

// endJob ends what still runs in an abandoned execution's job. A job named
// in another session cannot be found from this one, so its absence would
// prove nothing here.
func (guard Custody) endJob(ctx context.Context) (execx.TreeUsage, bool, error) {
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		return execx.TreeUsage{}, false, fmt.Errorf("read this process's session: %w", err)
	}
	if session != guard.Session {
		return execx.TreeUsage{}, false, fmt.Errorf("its job is named in session %d and this process runs in session %d", guard.Session, session)
	}
	usage, isFound, err := execx.EndNamedJob(ctx, guard.Job)
	if err != nil {
		return usage, isFound, fmt.Errorf("prove its job empty: %w", err)
	}
	return usage, isFound, nil
}

// strayWorkers lists the live processes the abandoned controller started
// after it recorded custody and that never joined the job: a worker is
// created suspended and only then put in its job, so a controller that ended
// between the two leaves it outside. A process that now has the
// controller's pid, and what that one started, are not the controller's.
func (guard Custody) strayWorkers() ([]proc.Entry, error) {
	processes, err := proc.Processes()
	if err != nil {
		return nil, fmt.Errorf("list the machine's processes: %w", err)
	}
	var successor time.Time
	for _, process := range processes {
		if process.PID == guard.Controller.PID {
			successor = process.Start
		}
	}
	var strays []proc.Entry
	for _, process := range processes {
		if process.ParentPID != guard.Controller.PID || process.Start.Before(guard.Created.Add(-time.Second)) {
			continue
		}
		if !successor.IsZero() && !process.Start.Before(successor) {
			continue
		}
		strays = append(strays, process)
	}
	return strays, nil
}
