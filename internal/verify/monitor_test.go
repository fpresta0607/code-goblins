package verify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type measuredTree struct {
	usage        execx.TreeUsage
	readError    error
	cleanupError error
	wasCleaned   bool
}

func (tree *measuredTree) Usage() (execx.TreeUsage, error) { return tree.usage, tree.readError }
func (tree *measuredTree) TerminateAndWait(context.Context) (execx.TreeUsage, error) {
	tree.wasCleaned = true
	usage := tree.usage
	if tree.cleanupError == nil {
		usage.ActiveProcesses = 0
	}
	return usage, tree.cleanupError
}

func TestMonitorInterruptsOnPressureAndUnavailableMeasurements(t *testing.T) {
	for _, test := range []struct {
		name        string
		memory      Headroom
		memoryError error
		usageError  error
	}{
		{name: "physical reserve", memory: Headroom{PhysicalAvailable: 3 << 30, CommitAvailable: 8 << 30}},
		{name: "commit reserve", memory: Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 3 << 30}},
		{name: "unreadable headroom", memoryError: errors.New("headroom unavailable")},
		{name: "unreadable job", memory: Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, usageError: errors.New("job unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			tree := &measuredTree{usage: execx.TreeUsage{ActiveProcesses: 2, TotalProcesses: 3, CPU: time.Second, PeakCommitBytes: 8 << 20}, readError: test.usageError}
			wasCanceled := false
			monitor := Monitor{Reserve: Headroom{PhysicalAvailable: 4 << 30, CommitAvailable: 4 << 30}, Poll: time.Millisecond, Grace: time.Millisecond,
				Headroom: func() (Headroom, error) { return test.memory, test.memoryError },
				Cancel:   func() error { wasCanceled = true; return nil },
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			// Act
			result, err := monitor.Watch(ctx, tree)

			// Assert
			if err == nil || result.State != Interrupted || !result.CleanupConfirmed || !tree.wasCleaned || !wasCanceled {
				t.Fatalf("result = %+v, err = %v, cleaned = %v, canceled = %v", result, err, tree.wasCleaned, wasCanceled)
			}
			if result.Usage.CPU != time.Second || result.Usage.PeakCommitBytes != 8<<20 {
				t.Fatalf("cleanup lost cumulative usage: %+v", result)
			}
		})
	}
}

func TestMonitorNeverCallsUncertainCleanupCompleted(t *testing.T) {
	// Arrange
	tree := &measuredTree{usage: execx.TreeUsage{ActiveProcesses: 1}, cleanupError: errors.New("job still occupied")}
	monitor := Monitor{Headroom: func() (Headroom, error) { return Headroom{}, errors.New("unreadable") }, Grace: time.Millisecond}

	// Act
	result, err := monitor.Watch(context.Background(), tree)

	// Assert
	if err == nil || result.State != CleanupUncertain || result.CleanupConfirmed || !tree.wasCleaned {
		t.Fatalf("uncertain cleanup = %+v, %v", result, err)
	}
}

func TestMonitorCompletesOnlyOnceTheWholeTreeIsEmpty(t *testing.T) {
	// Arrange
	tree := &measuredTree{usage: execx.TreeUsage{ActiveProcesses: 2, TotalProcesses: 3, CPU: time.Second, SampledPrivateWorkingSetBytes: 32 << 20}}
	samples := 0
	monitor := Monitor{Poll: time.Millisecond, Headroom: func() (Headroom, error) {
		samples++
		if samples == 2 {
			tree.usage.ActiveProcesses = 0
		}
		return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 9 << 30}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Act
	result, err := monitor.Watch(ctx, tree)

	// Assert
	if err != nil || samples < 2 || result.State != Completed || !result.CleanupConfirmed || !tree.wasCleaned {
		t.Fatalf("completion = %+v, %v, samples = %d", result, err, samples)
	}
	if result.SampledPeakPrivateWorkingSetBytes != 32<<20 || result.MinimumHeadroom.PhysicalAvailable != 8<<30 {
		t.Fatalf("lost measurement: %+v", result)
	}
}

func TestMonitorRetainsFailuresFromCancellationCleanupAndRecording(t *testing.T) {
	for _, test := range []struct {
		name         string
		isCanceled   bool
		cleanupError error
		recordAt     int
	}{
		{name: "caller cancellation", isCanceled: true},
		{name: "cleanup error", cleanupError: errors.New("cannot prove cleanup")},
		{name: "sample write error", recordAt: 1},
		{name: "final write error", recordAt: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			tree := &measuredTree{cleanupError: test.cleanupError}
			records := 0
			monitor := Monitor{Headroom: func() (Headroom, error) { return Headroom{PhysicalAvailable: 4 << 30, CommitAvailable: 4 << 30}, nil },
				Observe: func(Measurement) error {
					records++
					if records == test.recordAt {
						return errors.New("write failed")
					}
					return nil
				},
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.isCanceled {
				cancel()
			}

			// Act
			result, err := monitor.Watch(ctx, tree)

			// Assert
			if err == nil || result.State == Completed || !tree.wasCleaned {
				t.Fatalf("failure became a pass: %+v, %v", result, err)
			}
		})
	}
}
