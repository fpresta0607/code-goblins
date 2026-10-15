package execx

import (
	"context"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// waitingTree starts a tree whose command runs for 30 seconds, ended with
// the test.
func waitingTree(t *testing.T) *Tree {
	t.Helper()
	tree, err := StartTree(Command("cmd.exe", "/d", "/c", "ping -n 30 127.0.0.1 >nul"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tree.End() })
	return tree
}

// readsOfNothing has the job memory query return nothing for its first count
// calls, as a CI run of #799 saw it return with a member still running, and
// counts every call.
func readsOfNothing(t *testing.T, count int) *int {
	t.Helper()
	real := queryJobMemory
	calls := 0
	queryJobMemory = func(job windows.Handle) (jobMemoryUsage, error) {
		calls++
		if calls <= count {
			return jobMemoryUsage{}, nil
		}
		return real(job)
	}
	t.Cleanup(func() { queryJobMemory = real })
	return &calls
}

// A read of no commitment while a member runs, which cannot be so, is read
// again, and the usage returned is the read that can be, never the zeros.
func TestTreeUsageReadsTheJobAgainPastAReadOfNothing(t *testing.T) {
	// Arrange
	tree := waitingTree(t)
	before, err := tree.Usage()
	if err != nil || before.ActiveProcesses == 0 || before.CommitBytes == 0 {
		t.Fatalf("the first read = %+v, %v; want a running member with its commitment", before, err)
	}
	calls := readsOfNothing(t, 3)

	// Act
	usage, err := tree.Usage()

	// Assert
	if err != nil || usage.CommitBytes == 0 || usage.PeakCommitBytes < before.PeakCommitBytes || *calls < 4 {
		t.Errorf("usage = %+v, %v after %d memory reads; want a read with commitment and a peak of at least %d, read again past the zeros", usage, err, *calls, before.PeakCommitBytes)
	}
}

// A job whose memory reads as nothing through every read while a member runs
// is unreadable: its usage is an error naming why, never a zero sample.
func TestTreeUsageRefusesAJobWhoseMemoryReadsNothingWhileItRuns(t *testing.T) {
	// Arrange
	tree := waitingTree(t)
	if _, err := tree.Usage(); err != nil {
		t.Fatal(err)
	}
	realPeak := queryJobPeak
	queryJobPeak = func(windows.Handle) (uint64, error) { return 0, nil }
	t.Cleanup(func() { queryJobPeak = realPeak })
	readsOfNothing(t, 1<<30)
	started := time.Now()

	// Act
	usage, err := tree.Usage()

	// Assert
	if err == nil || usage != (TreeUsage{}) || !strings.Contains(err.Error(), "no commitment while") {
		t.Errorf("usage = %+v, %v; want an error that names no commitment while members run, and no sample", usage, err)
	}
	if waited := time.Since(started); waited > 5*time.Second {
		t.Errorf("the read took %s; want it to give up within its bound", waited)
	}
}

// A peak is never reported under one read before: a peak that reads lower,
// from either query, is the documented peak's or the one read before.
func TestTreeUsageNeverReportsAPeakUnderOneItRead(t *testing.T) {
	// Arrange
	tree := waitingTree(t)
	before, err := tree.Usage()
	if err != nil || before.PeakCommitBytes == 0 {
		t.Fatalf("the first read = %+v, %v; want a peak", before, err)
	}
	real := queryJobMemory
	queryJobMemory = func(job windows.Handle) (jobMemoryUsage, error) {
		memory, err := real(job)
		memory.PeakCommit = 1
		return memory, err
	}
	t.Cleanup(func() { queryJobMemory = real })

	// Act
	usage, err := tree.Usage()

	// Assert
	if err != nil || usage.PeakCommitBytes < before.PeakCommitBytes {
		t.Errorf("usage = %+v, %v; want a peak of at least the %d read before", usage, err, before.PeakCommitBytes)
	}
}

// A peak that both queries read under one read before is read again, so the
// usage returned never falls under it.
func TestTreeUsageReadsAgainPastAPeakBothQueriesReadLow(t *testing.T) {
	// Arrange
	tree := waitingTree(t)
	before, err := tree.Usage()
	if err != nil || before.PeakCommitBytes == 0 {
		t.Fatalf("the first read = %+v, %v; want a peak", before, err)
	}
	realMemory, realPeak := queryJobMemory, queryJobPeak
	low := 3
	queryJobMemory = func(job windows.Handle) (jobMemoryUsage, error) {
		memory, err := realMemory(job)
		if low > 0 {
			memory.PeakCommit = 1
		}
		return memory, err
	}
	queryJobPeak = func(job windows.Handle) (uint64, error) {
		if low > 0 {
			low--
			return 1, nil
		}
		return realPeak(job)
	}
	t.Cleanup(func() { queryJobMemory, queryJobPeak = realMemory, realPeak })

	// Act
	usage, err := tree.Usage()

	// Assert
	if err != nil || usage.PeakCommitBytes < before.PeakCommitBytes {
		t.Errorf("usage = %+v, %v; want a peak of at least the %d read before", usage, err, before.PeakCommitBytes)
	}
}

// A job a process ran in whose peak reads as nothing through every read is
// unreadable, even on its first read.
func TestTreeUsageRefusesAPeakOfNothingAfterAProcessRan(t *testing.T) {
	// Arrange
	tree := waitingTree(t)
	realMemory, realPeak := queryJobMemory, queryJobPeak
	queryJobMemory = func(job windows.Handle) (jobMemoryUsage, error) {
		memory, err := realMemory(job)
		memory.PeakCommit = 0
		return memory, err
	}
	queryJobPeak = func(windows.Handle) (uint64, error) { return 0, nil }
	t.Cleanup(func() { queryJobMemory, queryJobPeak = realMemory, realPeak })

	// Act
	usage, err := tree.Usage()

	// Assert
	if err == nil || usage != (TreeUsage{}) || !strings.Contains(err.Error(), "no peak after") {
		t.Errorf("usage = %+v, %v; want an error that names no peak after processes ran", usage, err)
	}
}

// Once every member has exited, no commitment is so, and the peak stays.
func TestTreeUsageKeepsItsPeakOnceItsMembersExit(t *testing.T) {
	// Arrange
	command := Command("cmd.exe", "/d", "/c", "exit 0")
	tree, err := StartTree(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tree.End() })
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}

	// Act
	var usage TreeUsage
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if usage, err = tree.Usage(); err != nil || usage.ActiveProcesses == 0 {
			break
		}
	}

	// Assert
	if err != nil || usage.ActiveProcesses != 0 || usage.PeakCommitBytes == 0 {
		t.Errorf("usage = %+v, %v; want no member running, and the peak it reached", usage, err)
	}
}

func TestTreeAccountingRejectsUnreadableJob(t *testing.T) {
	// Arrange
	tree := &Tree{job: windows.InvalidHandle}

	// Act
	usage, err := tree.Usage()

	// Assert
	if err == nil || usage != (TreeUsage{}) {
		t.Fatalf("unreadable job = %+v, %v, want no usable measurement", usage, err)
	}
	if _, err := tree.TerminateAndWait(context.Background()); err == nil || tree.isDone {
		t.Fatalf("unreadable cleanup = %v, closed = %v, want custody retained", err, tree.isDone)
	}
}

func TestTreeCleanupDeadlineIncludesWaitingForMeasurement(t *testing.T) {
	// Arrange
	tree := &Tree{job: windows.InvalidHandle}
	tree.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	finished := make(chan error, 1)

	// Act
	go func() { _, err := tree.TerminateAndWait(ctx); finished <- err }()

	// Assert
	select {
	case err := <-finished:
		tree.mu.Unlock()
		if err != context.DeadlineExceeded {
			t.Fatalf("cleanup = %v, want deadline before taking custody", err)
		}
	case <-time.After(time.Second):
		tree.mu.Unlock()
		<-finished
		t.Fatal("cleanup waited for measurement past its deadline")
	}
}
