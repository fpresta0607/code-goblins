package execx_test

import (
	"context"
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/execx/treetest"
)

func uniqueJobName() string {
	return `Local\cfo-execx-test-` + rand.Text()
}

func TestStartNamedTreeRefusesANameThatAlreadyExists(t *testing.T) {
	// Arrange
	name := uniqueJobName()
	pointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	existing, err := windows.CreateJobObject(nil, pointer)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(existing)
	cmd := execx.Command(os.Args[0], "-test.run=^$")

	// Act
	tree, err := execx.StartNamedTree(cmd, name)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a taken job name was accepted: %v", err)
	}
	if tree != nil || cmd.Process != nil {
		t.Fatalf("a process started under a taken job name: %+v", cmd.Process)
	}
}

func TestEndNamedJobFindsNoJobUnderAnUnusedName(t *testing.T) {
	// Act
	usage, isFound, err := execx.EndNamedJob(context.Background(), uniqueJobName())

	// Assert
	if err != nil || isFound || usage != (execx.TreeUsage{}) {
		t.Fatalf("an unused name = %+v, %v, %v, want no job and no error", usage, isFound, err)
	}
}

func TestEndNamedJobEndsATreeWhoseParentExitedFromAnotherHandle(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	name := uniqueJobName()
	cmd := execx.Command(os.Args[0], treetest.Args(folder, false)...)
	tree, err := execx.StartNamedTree(cmd, name)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.End()
	found, err := treetest.Find(folder)
	defer found.End()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Act
	usage, isFound, err := execx.EndNamedJob(ctx, name)
	cmd.Wait()

	// Assert
	if err != nil || !isFound || usage.ActiveProcesses != 0 || usage.TotalProcesses < 3 {
		t.Fatalf("ending the named job = %+v, %v, %v", usage, isFound, err)
	}
	for _, member := range found.Members() {
		if !member.Ends() {
			t.Fatalf("process %d of the named job still runs", member.PID)
		}
	}
}
