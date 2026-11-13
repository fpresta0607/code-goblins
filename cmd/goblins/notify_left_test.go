package main

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
)

// leftAt is when the reports in these tests are sent.
var leftAt = time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)

// placedAt is one process of goblin cg-a's, started offset from leftAt.
func placedAt(pid, parent int, exe string, offset time.Duration) fleettree.Placed {
	return fleettree.Placed{Process: fleettree.Process{PID: pid, ParentPID: parent, Exe: exe, Created: leftAt.Add(offset).UnixNano(), Started: leftAt.Add(offset)}, Owner: "cg-a"}
}

func detachedAt(pid, parent int, exe string, offset time.Duration) fleettree.Placed {
	process := placedAt(pid, parent, exe, offset)
	process.Detached = true
	return process
}

func byHarnessAt(pid int, exe string, offset time.Duration) fleettree.Placed {
	process := placedAt(pid, 1, exe, offset)
	process.ByHarness = true
	return process
}

func pidsOf(processes []fleettree.Placed) []int {
	pids := []int{}
	for _, process := range processes {
		pids = append(pids, process.PID)
	}
	slices.Sort(pids)
	return pids
}

// What a report would name it reads again a moment later, and names only
// what still runs then by its pid and its start: not a tool call run beside
// the report that ended meanwhile, nor a program that took a named process's
// id since. What started between the readings was not running at the first.
// A report that would name nothing reads once and waits for nothing.
func TestADoneReportNamesOnlyWhatStillRunsAMomentLater(t *testing.T) {
	shell := []fleettree.Placed{byHarnessAt(10, "powershell.exe", -time.Second), placedAt(11, 10, "goblins.exe", 0)}
	server := detachedAt(70, 69, "node.exe", -30*time.Minute)
	gitStatus := byHarnessAt(50, "git.exe", -100*time.Millisecond)
	watch := byHarnessAt(71, "node.exe", -10*time.Minute)
	replaced := byHarnessAt(71, "msedge.exe", time.Second)
	started := byHarnessAt(60, "node.exe", time.Second)
	tests := []struct {
		name      string
		readings  [][]fleettree.Placed
		wantLeft  []int
		wantReads int
	}{
		{
			name: "what ended or was replaced meanwhile",
			readings: [][]fleettree.Placed{
				append(slices.Clone(shell), server, gitStatus, watch),
				append(slices.Clone(shell), server, replaced, started),
			},
			wantLeft:  []int{70},
			wantReads: 2,
		},
		{
			name:      "nothing left at the first reading",
			readings:  [][]fleettree.Placed{shell, append(slices.Clone(shell), server)},
			wantLeft:  []int{},
			wantReads: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			reads := 0
			read := func() ([]fleettree.Placed, error) {
				reads++
				return test.readings[reads-1], nil
			}

			// Act
			left, err := leftByReport(read, 11, 0, 0)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if got := pidsOf(left); !slices.Equal(got, test.wantLeft) {
				t.Errorf("left running = %v, want %v", got, test.wantLeft)
			}
			if reads != test.wantReads {
				t.Errorf("read %d times, want %d", reads, test.wantReads)
			}
		})
	}
}

// A reading that fails at either reading fails the report's list, which
// the report then says could not be read.
func TestADoneReportSaysWhenWhatIsLeftCannotBeRead(t *testing.T) {
	shell := []fleettree.Placed{byHarnessAt(10, "powershell.exe", -time.Second), placedAt(11, 10, "goblins.exe", 0)}
	server := detachedAt(70, 69, "node.exe", -30*time.Minute)
	failure := errors.New("the process list could not be read")
	for _, failingRead := range []int{1, 2} {
		// Arrange
		reads := 0
		read := func() ([]fleettree.Placed, error) {
			reads++
			if reads == failingRead {
				return nil, failure
			}
			return append(slices.Clone(shell), server), nil
		}

		// Act
		left, err := leftByReport(read, 11, 0, 0)

		// Assert
		if !errors.Is(err, failure) || left != nil {
			t.Errorf("reading %d failing: left = %v, err = %v, want the failure and nothing named", failingRead, left, err)
		}
	}
}
