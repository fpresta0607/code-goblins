package execx_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/execx/treetest"
)

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
	usage, err := tree.Usage()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if usage.ActiveProcesses != 3 || usage.TotalProcesses != 4 {
		t.Errorf("usage = %+v, want three live workers and the exited relay", usage)
	}
	if usage.CPU <= 0 || usage.CommitBytes == 0 || usage.PeakCommitBytes < usage.CommitBytes || usage.SampledPrivateWorkingSetBytes == 0 {
		t.Errorf("usage = %+v, want measured CPU, commitment and private working set", usage)
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
	after, err := tree.Usage()
	if err != nil {
		t.Fatal(err)
	}
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
