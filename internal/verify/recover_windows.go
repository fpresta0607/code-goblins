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
// execution's workers.
func currentController() (lock.Info, error) {
	pid := os.Getpid()
	start, isKnown := proc.StartTime(pid)
	if !isKnown {
		return lock.Info{}, errors.New("verify: this process's start time cannot be read")
	}
	hostname, err := os.Hostname()
	if err != nil {
		return lock.Info{}, fmt.Errorf("verify: read this machine's name: %w", err)
	}
	return lock.Info{PID: pid, OwnerPID: pid, Start: start, Hostname: hostname, Acquired: time.Now().UTC()}, nil
}

// endJob ends what still runs in an abandoned execution's job.
func (guard Custody) endJob(ctx context.Context) (execx.TreeUsage, bool, error) {
	usage, isFound, err := execx.EndNamedJob(ctx, guard.Job)
	if err != nil {
		return usage, isFound, fmt.Errorf("prove its job empty: %w", err)
	}
	return usage, isFound, nil
}

// strayWorkers lists the processes the abandoned controller started after
// it recorded custody that still run outside the job: a worker is created
// suspended and only then put in its job, so a controller that ended between
// the two leaves it outside. A worker the job's end has just ended, or a
// controller still exiting, can be listed for a moment, so the list is read
// again for a second and a half, within ctx, before anything is named.
func (guard Custody) strayWorkers(ctx context.Context) ([]proc.Entry, error) {
	deadline := time.Now().Add(1500 * time.Millisecond)
	for {
		processes, err := proc.Processes()
		if err != nil {
			return nil, fmt.Errorf("list the machine's processes: %w", err)
		}
		strays, err := straysAmong(processes, guard)
		var running []proc.Entry
		for _, stray := range strays {
			if isRunning(stray.PID) {
				running = append(running, stray)
			}
		}
		if err == nil && len(running) == 0 {
			return nil, nil
		}
		if time.Now().After(deadline) {
			return running, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// straysAmong picks from processes those the controller of guard started
// after it recorded custody. While the controller itself is still listed
// under its own start it has not finished exiting, and nothing is proven. A
// process that has since taken the controller's pid, and what that one
// started, are not the controller's.
func straysAmong(processes []proc.Entry, guard Custody) ([]proc.Entry, error) {
	var successor time.Time
	for _, process := range processes {
		if process.PID != guard.Controller.PID {
			continue
		}
		if difference := process.Start.Sub(guard.Controller.Start); difference > -time.Second && difference < time.Second {
			return nil, fmt.Errorf("its controller, pid %d, has not finished exiting", guard.Controller.PID)
		}
		successor = process.Start
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

// isRunning reports whether process pid has not exited. One this process
// cannot read is not proven gone.
func isRunning(pid int) bool {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false
	}
	if err != nil {
		return true
	}
	defer windows.CloseHandle(process)
	const stillActive = 259
	var code uint32
	if err := windows.GetExitCodeProcess(process, &code); err != nil {
		return true
	}
	return code == stillActive
}
