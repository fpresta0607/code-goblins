package execx

import (
	"context"
	"testing"

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
