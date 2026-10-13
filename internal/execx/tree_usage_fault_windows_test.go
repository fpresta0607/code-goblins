package execx

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

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
