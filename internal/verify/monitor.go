package verify

import (
	"context"
	"errors"
	"fmt"
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
}

// MeasuredJob retains custody until cleanup is confirmed. A caller must not
// give its admission turn back when TerminateAndWait fails.
type MeasuredJob interface {
	Usage() (execx.TreeUsage, error)
	TerminateAndWait(context.Context) (execx.TreeUsage, error)
}

// Monitor observes an admitted job. Reserve is a cancellation threshold,
// not a memory reservation or a kernel limit. The shared floor still applies.
type Monitor struct {
	Reserve  Headroom
	Headroom func() (Headroom, error)
	Poll     time.Duration
	Grace    time.Duration
	// Cancel requests graceful cancellation and must return promptly.
	Cancel func() error
	// Observe persists a sample. Failure interrupts the run.
	Observe func(Measurement) error
}

func (monitor Monitor) Watch(ctx context.Context, tree MeasuredJob) (Measurement, error) {
	start := time.Now()
	result := Measurement{State: Running}
	floor := uint64(4 << 30)
	monitor.Reserve.PhysicalAvailable = max(floor, monitor.Reserve.PhysicalAvailable)
	monitor.Reserve.CommitAvailable = max(floor, monitor.Reserve.CommitAvailable)
	if monitor.Poll <= 0 {
		monitor.Poll = time.Second
	}
	if monitor.Grace <= 0 {
		monitor.Grace = 2 * time.Second
	}
	monitor.Grace = min(monitor.Grace, 30*time.Second)
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
		if headroom.PhysicalAvailable < monitor.Reserve.PhysicalAvailable || headroom.CommitAvailable < monitor.Reserve.CommitAvailable {
			failure = fmt.Errorf("verify: reserve pressure: physical available %d, reserve %d, commit available %d, reserve %d", headroom.PhysicalAvailable, monitor.Reserve.PhysicalAvailable, headroom.CommitAvailable, monitor.Reserve.CommitAvailable)
			break
		}
		if failure = ctx.Err(); failure != nil {
			break
		}
		if usage.ActiveProcesses == 0 {
			break
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
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
	usage, cleanupError := tree.TerminateAndWait(cleanupContext)
	if cleanupError == nil && usage.ActiveProcesses != 0 {
		cleanupError = errors.New("verify: cleanup returned active workers")
	}
	if cleanupError != nil {
		result.State = CleanupUncertain
		failure = errors.Join(failure, fmt.Errorf("verify: cleanup uncertain: %w", cleanupError))
	} else {
		result.CleanupConfirmed = true
		result.Usage = usage
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
