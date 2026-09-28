package proc

import (
	"os"
	"syscall"
	"testing"
)

func TestJobProcessStartRejectsAPIDThatNoLongerBelongsToTheJob(t *testing.T) {
	job, _, err := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(syscall.Handle(job))
	// A PID from an earlier job snapshot can now name an unrelated process.
	if started, belongs := jobProcessStart(syscall.Handle(job), uint32(os.Getpid())); belongs || !started.IsZero() {
		t.Fatalf("unrelated replacement authorized by old membership: %v %v", started, belongs)
	}
}
