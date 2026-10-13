package execx_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/execx/treetest"
)

// usageOnce reads the tree's usage until counts says it has the processes
// it should, for ten seconds at most. Windows can still count a process
// that has exited, and that a wait has seen exit, as active in its job for a
// moment, which a busy machine stretches.
func usageOnce(t *testing.T, tree *execx.Tree, counts func(execx.TreeUsage) bool) execx.TreeUsage {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		usage, err := tree.Usage()
		if err != nil {
			t.Fatal(err)
		}
		if counts(usage) || time.Now().After(deadline) {
			return usage
		}
	}
}

func TestTreeUsageIncludesWorkersAfterTheirParentExits(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	command := execx.Command(os.Args[0], treetest.Args(folder, false)...)
	tree, err := execx.StartTree(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tree.End() })
	found, err := treetest.Find(folder)
	t.Cleanup(found.End)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	usage := usageOnce(t, tree, func(usage execx.TreeUsage) bool { return usage.ActiveProcesses == 3 })

	// Assert
	if usage.ActiveProcesses != 3 || usage.TotalProcesses != 4 {
		t.Errorf("usage = %+v, want three live workers and the exited relay", usage)
	}
	// CPU time is charged at the clock's ticks, so workers that mostly wait
	// can read none yet: TestTreeUsageCountsTheCPUTimeOfAWorkerThatExited
	// proves it is read.
	if usage.CommitBytes == 0 || usage.PeakCommitBytes < usage.CommitBytes || usage.SampledPrivateWorkingSetBytes == 0 {
		t.Errorf("usage = %+v, want measured commitment and private working set", usage)
	}
	if err := treetest.Release(folder); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if !found.Child.Ends() {
		t.Fatal("child did not exit")
	}
	after := usageOnce(t, tree, func(usage execx.TreeUsage) bool { return usage.ActiveProcesses == 1 })
	if after.ActiveProcesses != 1 || after.TotalProcesses != 4 || after.CPU < usage.CPU || after.PeakCommitBytes < usage.PeakCommitBytes {
		t.Errorf("usage after parent exit = %+v, before = %+v", after, usage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	final, err := tree.TerminateAndWait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if final.ActiveProcesses != 0 || final.TotalProcesses != 4 || final.CPU < after.CPU || !found.Helper.Ends() {
		t.Errorf("final usage = %+v, want all workers ended and cumulative CPU retained", final)
	}
	if _, err := tree.Usage(); err == nil {
		t.Fatal("measurement after handle closure reported success")
	}
}

func TestTreeTerminationRetainsCustodyWhenCancellationPreventsProof(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	command := execx.Command(os.Args[0], treetest.Args(folder, false)...)
	tree, err := execx.StartTree(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tree.End(); _ = command.Wait() })
	found, err := treetest.Find(folder)
	t.Cleanup(found.End)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	_, err = tree.TerminateAndWait(ctx)

	// Assert
	if err == nil {
		t.Fatal("canceled cleanup reported success")
	}
	if _, err := tree.Usage(); err != nil {
		t.Fatalf("uncertain cleanup lost its job handle: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	final, err := tree.TerminateAndWait(ctx)
	if err != nil || final.ActiveProcesses != 0 {
		t.Fatalf("retry cleanup = %+v, %v", final, err)
	}
}

func TestTreeMembersNamesEachProcessStillInTheJob(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	command := execx.Command(os.Args[0], treetest.Args(folder, false)...)
	tree, err := execx.StartTree(command)
	if err != nil {
		t.Fatal(err)
	}
	found, err := treetest.Find(folder)
	defer found.End()
	defer func() { tree.End(); command.Wait() }()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	members, err := tree.Members()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	named := make(map[int]string)
	for _, member := range members {
		named[member.PID] = member.Name
	}
	for _, process := range found.Members() {
		if !strings.EqualFold(named[process.PID], filepath.Base(os.Args[0])) {
			t.Fatalf("members = %+v, want pid %d named %s", members, process.PID, filepath.Base(os.Args[0]))
		}
	}
	if _, err := tree.TerminateAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if members, err := tree.Members(); err == nil {
		t.Fatalf("a closed job still names members: %+v", members)
	}
}

func TestTreeUsageCountsTheCPUTimeOfAWorkerThatExited(t *testing.T) {
	const burn = 100 * time.Millisecond
	if os.Getenv("EXECX_TEST_BURN") != "" {
		// Spin until Windows has charged this process the CPU time, which it
		// does at the clock's ticks.
		for spent := time.Duration(0); spent < burn; {
			var creation, exit, kernel, user windows.Filetime
			if err := windows.GetProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user); err != nil {
				os.Exit(2)
			}
			spent = filetimeDuration(kernel) + filetimeDuration(user)
		}
		os.Exit(0)
	}
	// Arrange
	command := execx.Command(os.Args[0], "-test.run=^TestTreeUsageCountsTheCPUTimeOfAWorkerThatExited$")
	command.Env = append(os.Environ(), "EXECX_TEST_BURN=1")
	tree, err := execx.StartTree(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tree.End() })
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Act
	usage, err := tree.TerminateAndWait(ctx)

	// Assert
	if err != nil || usage.TotalProcesses != 1 || usage.CPU < burn {
		t.Fatalf("usage = %+v, %v, want the exited worker's %s of CPU time", usage, err, burn)
	}
}

// filetimeDuration is a span of 100-nanosecond intervals as a duration.
func filetimeDuration(span windows.Filetime) time.Duration {
	return time.Duration(uint64(span.HighDateTime)<<32|uint64(span.LowDateTime)) * 100
}
