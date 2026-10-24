package lifecycle

import (
	"slices"
	"testing"
	"time"
)

func TestExecutionCustodyRequiresExactIdentityAndPreservesExplicitOwnership(t *testing.T) {
	started := time.Date(2026, 10, 24, 4, 5, 32, 0, time.UTC)
	directory := t.TempDir()
	mark := Mark{Terminal: "consumer", ProofSum: "proof"}
	for _, test := range []struct {
		name        string
		controller  Identity
		marks       []Mark
		job         []Identity
		parentStart time.Time
		want        []int
	}{
		{name: "active custody overrides folder", controller: Identity{PID: 30, Started: started}},
		{name: "reused controller pid is an orphan", controller: Identity{PID: 30, Started: started.Add(-time.Nanosecond)}, want: []int{30, 31}},
		{name: "no custody is an orphan", want: []int{30, 31}},
		{name: "reused parent does not protect earlier child", controller: Identity{PID: 30, Started: started.Add(time.Hour)}, parentStart: started.Add(time.Hour), want: []int{31}},
		{name: "own terminal can stop its execution", controller: Identity{PID: 30, Started: started}, marks: []Mark{mark}, want: []int{30, 31}},
		{name: "exact job can stop its execution", controller: Identity{PID: 30, Started: started}, job: []Identity{{PID: 30, Started: started}, {PID: 31, Started: started.Add(time.Second)}}, want: []int{30, 31}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			parentStart := test.parentStart
			if parentStart.IsZero() {
				parentStart = started
			}
			processes := []Process{
				{PID: 30, ParentPID: 900, Started: parentStart, Directory: directory, Mark: mark},
				{PID: 31, ParentPID: 30, Started: started.Add(time.Second), Directory: directory},
			}

			// Act
			processes = WithExecutionCustody(processes, []Identity{test.controller})
			owned := OwnedProcesses(processes, []string{directory}, test.job, test.marks, nil)
			var pids []int
			for _, process := range owned {
				pids = append(pids, process.PID)
			}

			// Assert
			if !slices.Equal(pids, test.want) {
				t.Fatalf("owned pids %v, want %v", pids, test.want)
			}
		})
	}
}
