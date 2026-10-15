package cirunner

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Limits bound a job: how long the VM's runner waits for a job, how long the
// VM runs in all, and how often the job is looked at.
type Limits struct {
	Pickup time.Duration
	Job    time.Duration
	Poll   time.Duration
}

// DefaultLimits give the runner 5 minutes to take a job and the VM 30 in
// all: the shards' own 20 minute timeout and 10 more.
var DefaultLimits = Limits{Pickup: 5 * time.Minute, Job: 30 * time.Minute, Poll: 5 * time.Second}

// cleanupLimit bounds putting the VM back to clean and deleting the job's
// runner, however the job ended. Each is tried cleanupTries times, a
// cleanupPause apart, within it: a Hyper-V call slow to start on a machine
// short of memory, or a runner GitHub still counts busy, must not leave a
// VM running a job's code or a runner registered.
const (
	cleanupLimit = 2 * time.Minute
	cleanupTries = 3
	cleanupPause = 5 * time.Second
)

// Outcome is how a job ended.
type Outcome string

const (
	// Ran is a job the runner took and ran until the VM powered itself off.
	Ran Outcome = "ran"
	// NoJob is a VM whose runner took no job: none came within the pickup
	// limit, or the VM powered off first.
	NoJob Outcome = "no job"
	// TimedOut is a VM that ran past the job limit.
	TimedOut Outcome = "timed out"
	// Abandoned is a job whose context ended, as its turn's end ends it.
	Abandoned Outcome = "abandoned"
	// Failed is a job that could not be run or looked at.
	Failed Outcome = "failed"
)

// Runners are the GitHub calls a job makes.
type Runners interface {
	IsPrivate(ctx context.Context) (bool, error)
	QueuedJob(ctx context.Context) (Pending, error)
	RegisterRunner(ctx context.Context, name string) (Runner, error)
	RunnerState(ctx context.Context, id int64) (isBusy, isOnline bool, err error)
	DeleteRunner(ctx context.Context, id int64) error
	DeleteOwnRunners(ctx context.Context) error
}

// Deps are what a job runs with: GitHub, the VM, the DVD it builds for the
// VM from a runner's configuration, returning the DVD's path, and a clock
// whose Sleep ends early when its context ends.
type Deps struct {
	GitHub Runners
	VM     Hypervisor
	DVD    func(config string) (string, error)
	Now    func() time.Time
	Sleep  func(ctx context.Context, d time.Duration)
}

// RunOneJob runs one GitHub job in the VM: from clean, with a runner
// registered for one job, until the VM powers itself off once its runner
// exits. Whatever happened, it then puts the VM back to clean and deletes
// the runner, within cleanupLimit and past ctx's end. It ends the VM when no
// job comes within limits.Pickup, when the VM runs past limits.Job, and when
// ctx ends, which is how the end of the job's turn reaches it: the next look
// at the VM fails, as a command started past its context's end does.
func RunOneJob(ctx context.Context, d Deps, limits Limits, progress func(string)) (outcome Outcome, failure error) {
	if err := putBack(ctx, d.VM); err != nil {
		return ended(ctx, err)
	}
	runner, err := d.GitHub.RegisterRunner(ctx, RunnerPrefix+d.Now().UTC().Format("20060102-150405"))
	if err != nil {
		return ended(ctx, err)
	}
	var iso string
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupLimit)
		defer cancel()
		isClean := again(cleanup, d, func() error { return putBack(cleanup, d.VM) })
		failure = errors.Join(failure, isClean, again(cleanup, d, func() error { return d.GitHub.DeleteRunner(cleanup, runner.ID) }))
		// The DVD holds the runner's configuration, spent once the runner
		// is gone, and goes once the VM no longer holds it.
		if iso != "" && isClean == nil {
			if err := fsx.Remove(iso); err != nil && !errors.Is(err, os.ErrNotExist) {
				failure = errors.Join(failure, err)
			}
		}
	}()
	iso, err = d.DVD(runner.Config)
	if err != nil {
		return Failed, err
	}
	if err := d.VM.AttachDVD(ctx, iso); err != nil {
		return ended(ctx, err)
	}
	if err := d.VM.Start(ctx); err != nil {
		return ended(ctx, err)
	}
	progress("the VM started with runner " + runner.Name)
	started := d.Now()
	isOnline, hasTakenJob := false, false
	for {
		isRunning, err := d.VM.IsRunning(ctx)
		if err != nil {
			return ended(ctx, err)
		}
		if !isRunning && hasTakenJob {
			return Ran, nil
		}
		if !isRunning {
			return NoJob, nil
		}
		if !hasTakenJob {
			// A state GitHub did not give is asked for again at the next
			// look, until the pickup limit.
			isBusy, isNowOnline, err := d.GitHub.RunnerState(ctx, runner.ID)
			if err == nil && isNowOnline && !isOnline {
				isOnline = true
				progress("the runner is online")
			}
			if err == nil && isBusy {
				hasTakenJob = true
				progress("the runner took a job")
			}
		}
		elapsed := d.Now().Sub(started)
		if !hasTakenJob && elapsed >= limits.Pickup {
			return NoJob, nil
		}
		if elapsed >= limits.Job {
			return TimedOut, nil
		}
		d.Sleep(ctx, limits.Poll)
	}
}

// again runs step up to cleanupTries times, a cleanupPause apart, until it
// succeeds or ctx ends, and returns its last error.
func again(ctx context.Context, d Deps, step func() error) error {
	err := step()
	for try := 1; err != nil && try < cleanupTries && ctx.Err() == nil; try++ {
		d.Sleep(ctx, cleanupPause)
		err = step()
	}
	return err
}

// putBack turns the VM off and puts it back to its clean checkpoint.
func putBack(ctx context.Context, vm Hypervisor) error {
	if err := vm.TurnOff(ctx); err != nil {
		return err
	}
	return vm.Restore(ctx)
}

// ended is how a job ends on err: abandoned when its context ended first,
// and failed otherwise.
func ended(ctx context.Context, err error) (Outcome, error) {
	if ctx.Err() != nil {
		return Abandoned, nil
	}
	return Failed, err
}
