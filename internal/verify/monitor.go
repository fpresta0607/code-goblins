package verify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type ExecutionState string

const (
	Queued           ExecutionState = "queued"
	Admitted         ExecutionState = "admitted"
	Running          ExecutionState = "running"
	Completed        ExecutionState = "completed"
	Interrupted      ExecutionState = "interrupted"
	CleanupUncertain ExecutionState = "cleanup_uncertain"
)

type Headroom struct {
	PhysicalAvailable uint64 `json:"physical_available_bytes"`
	CommitAvailable   uint64 `json:"commit_available_bytes"`
}

type Measurement struct {
	State                             ExecutionState  `json:"state"`
	Usage                             execx.TreeUsage `json:"usage"`
	MinimumHeadroom                   Headroom        `json:"minimum_headroom"`
	SampledPeakPrivateWorkingSetBytes uint64          `json:"sampled_peak_private_working_set_bytes"`
	Samples                           uint64          `json:"samples"`
	WallSeconds                       float64         `json:"wall_seconds"`
	CleanupConfirmed                  bool            `json:"cleanup_confirmed"`
	// Leftovers are what the command left running once its own process had
	// exited and its linger had passed, which the run then ended.
	Leftovers []Leftover `json:"leftovers,omitempty"`
	// Spared are the machine services the command started, with what runs
	// under them, which the run's end left running: Docker Desktop, the
	// no-mistakes daemon or the page server serves the whole machine
	// (proc.ServiceOf).
	Spared []Leftover `json:"spared,omitempty"`
}

// ErrLimit is the failure of a command that ran past its limit.
var ErrLimit = errors.New("verify: the command ran past its limit")

// Leftover is a process a command left running, by image name and pid.
type Leftover struct {
	Name string `json:"name"`
	PID  int    `json:"pid"`
}

// MeasuredJob retains custody until cleanup is confirmed. A caller must not
// give its admission turn back when EndAndWait fails. EndAndWait spares a
// machine service and what runs under it, and returns them once nothing
// else is left in the job.
type MeasuredJob interface {
	Usage() (execx.TreeUsage, error)
	Members() ([]execx.EndedProcess, error)
	EndAndWait(context.Context) (execx.TreeUsage, []execx.EndedProcess, error)
}

// Monitor observes an admitted job. Reserve is the memory the run leaves
// the desktop, a cancellation threshold, not a memory reservation or a
// kernel limit. It sits under the admission floor, since a run admitted at
// the floor uses the memory between the two, and a dimension left at zero
// takes DefaultDesktopReserve's.
type Monitor struct {
	Reserve  Headroom
	Headroom func() (Headroom, error)
	// Sustain, ten seconds unless set, is how long free memory must stay
	// under the reserve before the run is cancelled. Free memory is the
	// whole machine's, so one sample under it can be another program's
	// spike.
	Sustain time.Duration
	Poll    time.Duration
	Grace   time.Duration
	// Exited is closed once the command's own process has exited. Linger,
	// ten seconds unless set, is how long what it left running may run on
	// after it. What still runs then is ended and named as a leftover, and
	// the run keeps the command's own result. Without Exited the run waits
	// for the whole job.
	Exited <-chan struct{}
	Linger time.Duration
	// Cancel requests graceful cancellation and must return promptly.
	Cancel func() error
	// Observe persists a sample. Failure interrupts the run.
	Observe func(Measurement) error
}

func (monitor Monitor) Watch(ctx context.Context, tree MeasuredJob) (Measurement, error) {
	start := time.Now()
	result := Measurement{State: Running}
	if monitor.Reserve.PhysicalAvailable == 0 {
		monitor.Reserve.PhysicalAvailable = DefaultDesktopReserve.PhysicalAvailable
	}
	if monitor.Reserve.CommitAvailable == 0 {
		monitor.Reserve.CommitAvailable = DefaultDesktopReserve.CommitAvailable
	}
	if monitor.Poll <= 0 {
		monitor.Poll = time.Second
	}
	if monitor.Grace <= 0 {
		monitor.Grace = 2 * time.Second
	}
	monitor.Grace = min(monitor.Grace, 30*time.Second)
	if monitor.Linger <= 0 {
		monitor.Linger = 10 * time.Second
	}
	if monitor.Sustain <= 0 {
		monitor.Sustain = 10 * time.Second
	}
	var exitedAt, pressedSince time.Time
	ticker := time.NewTicker(monitor.Poll)
	defer ticker.Stop()
	var failure error
	for {
		if failure = ctx.Err(); failure != nil {
			break
		}
		if monitor.Headroom == nil {
			failure = errors.New("verify: machine headroom cannot be read")
			break
		}
		headroom, err := monitor.Headroom()
		if err != nil {
			failure = fmt.Errorf("verify: read machine headroom: %w", err)
			break
		}
		usage, err := tree.Usage()
		if err != nil {
			failure = fmt.Errorf("verify: read job usage: %w", err)
			break
		}
		result.Usage = usage
		if result.Samples == 0 {
			result.MinimumHeadroom = headroom
		} else {
			result.MinimumHeadroom.PhysicalAvailable = min(result.MinimumHeadroom.PhysicalAvailable, headroom.PhysicalAvailable)
			result.MinimumHeadroom.CommitAvailable = min(result.MinimumHeadroom.CommitAvailable, headroom.CommitAvailable)
		}
		result.Samples++
		result.SampledPeakPrivateWorkingSetBytes = max(result.SampledPeakPrivateWorkingSetBytes, usage.SampledPrivateWorkingSetBytes)
		result.WallSeconds = time.Since(start).Seconds()
		if monitor.Observe != nil {
			if failure = monitor.Observe(result); failure != nil {
				break
			}
		}
		if headroom.PhysicalAvailable >= monitor.Reserve.PhysicalAvailable && headroom.CommitAvailable >= monitor.Reserve.CommitAvailable {
			pressedSince = time.Time{}
		} else if pressedSince.IsZero() {
			pressedSince = time.Now()
		}
		if !pressedSince.IsZero() && time.Since(pressedSince) >= monitor.Sustain {
			failure = fmt.Errorf("verify: reserve pressure for %s: physical available %d, reserve %d, commit available %d, reserve %d", monitor.Sustain, headroom.PhysicalAvailable, monitor.Reserve.PhysicalAvailable, headroom.CommitAvailable, monitor.Reserve.CommitAvailable)
			break
		}
		if failure = ctx.Err(); failure != nil {
			break
		}
		if usage.ActiveProcesses == 0 {
			break
		}
		if exitedAt.IsZero() && monitor.Exited != nil {
			select {
			case <-monitor.Exited:
				exitedAt = time.Now()
			default:
			}
		}
		if !exitedAt.IsZero() && time.Since(exitedAt) >= monitor.Linger {
			members, err := tree.Members()
			if err != nil {
				failure = fmt.Errorf("verify: read what the command left running: %w", err)
				break
			}
			// Windows can count an exited process in its job for a moment
			// after, and Members names only what still runs.
			for _, member := range members {
				result.Leftovers = append(result.Leftovers, Leftover{Name: member.Name, PID: member.PID})
			}
			break
		}
		// The command's exit is sampled at once, so a quick command does not
		// wait out a whole poll before its run can end.
		var exit <-chan struct{}
		if exitedAt.IsZero() {
			exit = monitor.Exited
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		case <-exit:
		}
	}
	result.State = Completed
	if failure != nil {
		result.State = Interrupted
		if monitor.Cancel != nil {
			failure = errors.Join(failure, monitor.Cancel())
			// The caller's context is already canceled. The grace interval is
			// separate, bounded, and followed by verified whole-job termination.
			timer := time.NewTimer(monitor.Grace)
			<-timer.C
		}
	}
	cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	usage, spared, cleanupError := tree.EndAndWait(cleanupContext)
	if cleanupError == nil && len(spared) == 0 && usage.ActiveProcesses != 0 {
		cleanupError = errors.New("verify: cleanup returned active workers")
	}
	if cleanupError != nil {
		result.State = CleanupUncertain
		failure = errors.Join(failure, fmt.Errorf("verify: cleanup uncertain: %w", cleanupError))
	} else {
		result.CleanupConfirmed = true
		result.Usage = usage
		// A service the end spared is no leftover it ended.
		for _, process := range spared {
			result.Spared = append(result.Spared, Leftover{Name: process.Name, PID: process.PID})
			result.Leftovers = slices.DeleteFunc(result.Leftovers, func(leftover Leftover) bool { return leftover.PID == process.PID })
		}
	}
	result.WallSeconds = time.Since(start).Seconds()
	if monitor.Observe != nil {
		if err := monitor.Observe(result); err != nil {
			failure = errors.Join(failure, fmt.Errorf("verify: record final measurement: %w", err))
			if result.State == Completed {
				result.State = Interrupted
			}
		}
	}
	if err := ctx.Err(); err != nil {
		failure = errors.Join(failure, err)
		if result.State == Completed {
			result.State = Interrupted
		}
	}
	return result, failure
}
