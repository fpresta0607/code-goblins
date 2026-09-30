package state

import (
	"os"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

func TestLifecycleTeardownKeepsTheExactBirthAcrossReads(t *testing.T) {
	directory := t.TempDir()
	started, exists := proc.StartTime(os.Getpid())
	if !exists {
		t.Fatal("could not identify the test process")
	}
	record := Lifecycle{ID: "task", Operation: "pause", Action: "pause", Phase: "paused", Teardown: []TeardownProcess{
		{PID: os.Getpid(), Started: started, Name: "current"},
		{PID: os.Getpid(), Started: started.Add(-time.Hour), Name: "reused PID"},
	}}
	if err := WriteLifecycle(directory, record); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		fresh, err := ReadLifecycle(directory, record.ID)
		if err != nil || len(fresh.Teardown) != 1 || fresh.Teardown[0].Name != "current" {
			t.Fatalf("lost the original or retained a reused identity: %+v %v", fresh, err)
		}
	}
}
