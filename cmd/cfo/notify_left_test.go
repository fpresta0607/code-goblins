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

// A done report runs in the goblin's tool call, whose shell, the console
// host Windows starts beside its hidden console and the programs of its
// pipeline all end with it, so none of them is what the goblin left
// running. On 2026-10-11 four of the first five done reports named such a
// console host or the report's own tail -2, gone a moment later. What the
// goblin did leave, a detached server from an earlier tool call, a
// background shell with its watch, or a server the tool call started with a
// console of its own, is still named. A machine service is not.
func TestADoneReportNamesNothingOfTheToolCallItIsSentFrom(t *testing.T) {
	leftovers := []fleettree.Placed{
		// A server an earlier tool call left, detached from its shell, with
		// the browser it drives.
		detachedAt(70, 69, "node.exe", -30*time.Minute),
		placedAt(71, 70, "chrome.exe", -29*time.Minute),
		// A background shell with its watch.
		byHarnessAt(80, "bash.exe", -20*time.Minute),
		placedAt(81, 80, "node.exe", -20*time.Minute),
	}
	service := detachedAt(90, 89, "no-mistakes.exe", -40*time.Minute)
	service.Service = true
	leftoverConsoles := map[int]int{70: 600, 80: 610, 81: 610, 90: 620}
	tests := []struct {
		name     string
		shell    []fleettree.Placed
		report   int
		console  int
		consoles map[int]int
		alsoLeft []int
	}{
		{
			// goblins notify ... | tail -2, as Elsie sent it: Git Bash's
			// launcher runs the shell, which made the console, runs a copy of
			// itself for the report, and started tail by replacing that
			// copy's Windows process. The report's git call took the id of
			// the server's long gone parent.
			name: "a Git Bash pipeline",
			shell: []fleettree.Placed{
				byHarnessAt(10, "bash.exe", -2*time.Second),
				placedAt(11, 10, "bash.exe", -1990*time.Millisecond),
				placedAt(12, 11, "conhost.exe", -1980*time.Millisecond),
				placedAt(13, 11, "bash.exe", -30*time.Millisecond),
				placedAt(14, 13, "goblins.exe", 0),
				detachedAt(15, 98, "tail.exe", 10*time.Millisecond),
				placedAt(69, 14, "git.exe", 20*time.Millisecond),
			},
			report:   14,
			console:  12,
			consoles: map[int]int{11: 12, 13: 12, 14: 12, 15: 12, 69: 12},
		},
		{
			// A PowerShell tool call, as Toby and Woody sent theirs, which
			// also started a server with a console of its own.
			name: "a PowerShell tool call",
			shell: []fleettree.Placed{
				byHarnessAt(20, "cmd.exe", -time.Second),
				placedAt(21, 20, "conhost.exe", -990*time.Millisecond),
				placedAt(22, 20, "powershell.exe", -900*time.Millisecond),
				placedAt(23, 22, "goblins.exe", 0),
				placedAt(24, 22, "node.exe", -500*time.Millisecond),
				placedAt(25, 24, "conhost.exe", -490*time.Millisecond),
			},
			report:   23,
			console:  21,
			consoles: map[int]int{20: 21, 22: 21, 23: 21, 24: 25},
			alsoLeft: []int{24, 25},
		},
		{
			// timeout 60 goblins notify ..., where Git Bash replaced the
			// shell's copy with timeout, so the report reaches no shell.
			name: "a report under timeout in Git Bash",
			shell: []fleettree.Placed{
				byHarnessAt(30, "bash.exe", -2*time.Second),
				placedAt(31, 30, "bash.exe", -1990*time.Millisecond),
				placedAt(32, 31, "conhost.exe", -1980*time.Millisecond),
				detachedAt(33, 97, "timeout.exe", -30*time.Millisecond),
				placedAt(34, 33, "timeout.exe", -20*time.Millisecond),
				placedAt(35, 34, "goblins.exe", 0),
			},
			report:   35,
			console:  32,
			consoles: map[int]int{31: 32, 33: 32, 34: 32, 35: 32},
		},
		{
			// A harness that runs its tool calls in its terminal's own
			// console, which everything the goblin started there shares, so
			// the console says nothing and only the report's line counts.
			name: "a console the terminal shares",
			shell: []fleettree.Placed{
				byHarnessAt(40, "powershell.exe", -time.Second),
				placedAt(41, 40, "goblins.exe", 0),
				placedAt(42, 41, "git.exe", 10*time.Millisecond),
			},
			report:   41,
			console:  0,
			consoles: map[int]int{40: 630, 41: 630, 42: 630, 70: 630, 80: 630, 81: 630},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			placed := append(append(slices.Clone(test.shell), leftovers...), service)
			consoleOf := func(pid int) (int, error) {
				if console, isKnown := test.consoles[pid]; isKnown {
					return console, nil
				}
				if console, isKnown := leftoverConsoles[pid]; isKnown {
					return console, nil
				}
				return 0, nil
			}

			// Act
			left := leftBeside(placed, test.report, test.console, consoleOf)

			// Assert
			want := append(pidsOf(leftovers), test.alsoLeft...)
			slices.Sort(want)
			if got := pidsOf(left); !slices.Equal(got, want) {
				t.Errorf("left running = %v, want %v", got, want)
			}
		})
	}
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
