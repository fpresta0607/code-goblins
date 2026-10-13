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
	members      []execx.EndedProcess
	membersError error
	cleanupError error
	wasCleaned   bool
}

func (tree *measuredTree) Usage() (execx.TreeUsage, error) { return tree.usage, tree.readError }
func (tree *measuredTree) Members() ([]execx.EndedProcess, error) {
	return tree.members, tree.membersError
}
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

func TestMonitorDoesNotCompleteWhenTheFinalSampleCancels(t *testing.T) {
	// Arrange
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tree := &measuredTree{}
	monitor := Monitor{Headroom: func() (Headroom, error) {
		return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil
	}, Observe: func(measurement Measurement) error {
		if measurement.State == Running {
			cancel()
		}
		return nil
	}}

	// Act
	result, err := monitor.Watch(ctx, tree)

	// Assert
	if !errors.Is(err, context.Canceled) || result.State != Interrupted || !result.CleanupConfirmed {
		t.Fatalf("final sample cancellation = %+v, %v", result, err)
	}
}

func TestMonitorEndsAndNamesWhatTheCommandLeftOnceItsLingerPasses(t *testing.T) {
	// Arrange
	tree := &measuredTree{usage: execx.TreeUsage{ActiveProcesses: 1, TotalProcesses: 2}, members: []execx.EndedProcess{{Name: "node.exe", PID: 4242}}}
	exited := make(chan struct{})
	close(exited)
	monitor := Monitor{Reserve: Headroom{PhysicalAvailable: 2 << 30, CommitAvailable: 2 << 30}, Poll: time.Millisecond, Exited: exited, Linger: 20 * time.Millisecond,
		Headroom: func() (Headroom, error) { return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Act
	result, err := monitor.Watch(ctx, tree)

	// Assert
	if err != nil || result.State != Completed || !result.CleanupConfirmed || !tree.wasCleaned {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if len(result.Leftovers) != 1 || result.Leftovers[0] != (Leftover{Name: "node.exe", PID: 4242}) {
		t.Fatalf("leftovers = %+v, want node.exe pid 4242", result.Leftovers)
	}
}

func TestMonitorWaitsForTheWholeJobUntilTheCommandExits(t *testing.T) {
	// Arrange
	tree := &measuredTree{usage: execx.TreeUsage{ActiveProcesses: 1}}
	monitor := Monitor{Reserve: Headroom{PhysicalAvailable: 2 << 30, CommitAvailable: 2 << 30}, Poll: time.Millisecond, Grace: time.Millisecond, Exited: make(chan struct{}), Linger: time.Millisecond,
		Headroom: func() (Headroom, error) { return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Act
	result, err := monitor.Watch(ctx, tree)

	// Assert
	if !errors.Is(err, context.DeadlineExceeded) || result.State != Interrupted || len(result.Leftovers) != 0 {
		t.Fatalf("a command still running was cut short as finished: %+v, %v", result, err)
	}
}

func TestMonitorInterruptsWhenItCannotNameWhatTheCommandLeft(t *testing.T) {
	// Arrange
	tree := &measuredTree{usage: execx.TreeUsage{ActiveProcesses: 1}, membersError: errors.New("job members unreadable")}
	exited := make(chan struct{})
	close(exited)
	monitor := Monitor{Reserve: Headroom{PhysicalAvailable: 2 << 30, CommitAvailable: 2 << 30}, Poll: time.Millisecond, Grace: time.Millisecond, Exited: exited, Linger: time.Millisecond,
		Headroom: func() (Headroom, error) { return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil },
	}

	// Act
	result, err := monitor.Watch(context.Background(), tree)

	// Assert
	if err == nil || result.State != Interrupted || !tree.wasCleaned {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
}

func TestMonitorLetsARunUseTheMemoryBetweenTheFloorAndItsReserve(t *testing.T) {
	// Arrange
	tree := &measuredTree{usage: execx.TreeUsage{ActiveProcesses: 1}}
	samples := 0
	monitor := Monitor{Reserve: Headroom{PhysicalAvailable: 2 << 30, CommitAvailable: 2 << 30}, Poll: time.Millisecond,
		Headroom: func() (Headroom, error) {
			samples++
			if samples == 3 {
				tree.usage.ActiveProcesses = 0
			}
			return Headroom{PhysicalAvailable: 3 << 30, CommitAvailable: 3 << 30}, nil
		},
	}

	// Act
	result, err := monitor.Watch(context.Background(), tree)

	// Assert
	if err != nil || result.State != Completed {
		t.Fatalf("a run above its reserve and under the admission floor was cut: %+v, %v", result, err)
	}
}

// exitingTree holds one process until its command exits, and none after.
type exitingTree struct{ exited chan struct{} }

func (tree exitingTree) Usage() (execx.TreeUsage, error) {
	select {
	case <-tree.exited:
		return execx.TreeUsage{TotalProcesses: 1}, nil
	default:
		return execx.TreeUsage{ActiveProcesses: 1, TotalProcesses: 1}, nil
	}
}
func (exitingTree) Members() ([]execx.EndedProcess, error) { return nil, nil }
func (exitingTree) TerminateAndWait(context.Context) (execx.TreeUsage, error) {
	return execx.TreeUsage{TotalProcesses: 1}, nil
}

func TestMonitorSamplesAgainAsSoonAsTheCommandExits(t *testing.T) {
	// Arrange
	tree := exitingTree{exited: make(chan struct{})}
	monitor := Monitor{Poll: time.Minute, Exited: tree.exited,
		Headroom: func() (Headroom, error) { return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil },
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(tree.exited)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := time.Now()

	// Act
	result, err := monitor.Watch(ctx, tree)

	// Assert
	if err != nil || result.State != Completed || time.Since(started) > 5*time.Second {
		t.Fatalf("a command that exited waited for the next poll: %+v, %v after %s", result, err, time.Since(started))
	}
}
