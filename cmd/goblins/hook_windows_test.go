package main

import (
	"os"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// programIsThisProcess makes record name this test process as its terminal's
// program, by pid and creation time, as a host names the harness it started.
func programIsThisProcess(t *testing.T, record *host.Record) {
	t.Helper()
	started, ok := proc.StartTime(os.Getpid())
	if !ok {
		t.Fatal("this process has no start time")
	}
	record.ChildPID, record.ChildStart = os.Getpid(), started
}
