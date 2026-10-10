package main

import (
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/janitor"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
)

// A would-end process is checked against whose it looks to be, apart from
// the rule that would end it: a rule that is wrong would otherwise vouch for
// itself. What looks like the Overlord's, the CFO terminal's, a running
// terminal's or, in a task's list, another terminal's, is flagged.
func TestAWouldEndProcessIsFlaggedByWhoseItLooksToBe(t *testing.T) {
	// Arrange
	began := time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return began.Add(time.Duration(seconds) * time.Second) }
	read := func(pid int, start time.Time) janitor.Process {
		return janitor.Process{Process: lifecycle.Process{PID: pid, Started: start}, Memory: 7 << 20}
	}
	standing := processStanding{
		processes: map[int]janitor.Process{1: read(1, at(1)), 2: read(2, at(2)), 3: read(3, at(3)), 4: read(4, at(4)), 5: read(5, at(5)), 6: read(6, at(6))},
		isDesktop: map[int]bool{1: true},
		marked:    map[int]string{2: "cfo", 4: "gb-other", 5: "gb-live"},
		under:     map[int]string{3: "gb-live", 6: "gb-other"},
	}
	owners := []janitor.Owner{{ID: "gb-gone"}, {ID: "gb-live", HostPID: 20}, {ID: "gb-rests", HostPID: 25, AtRestSince: began}, {ID: "cfo", HostPID: 30}}

	for _, test := range []struct {
		name string
		item janitor.ProcessItem
		want string
	}{
		{"a desktop program", janitor.ProcessItem{PID: 1, Started: at(1), Owner: "gb-gone"}, "HIS: a desktop program, or what one started"},
		{"a process that carries the CFO terminal's mark", janitor.ProcessItem{PID: 2, Started: at(2), Owner: "gb-gone"}, "CFO: a process of the CFO's terminal"},
		{"a process under a running host", janitor.ProcessItem{PID: 3, Started: at(3), Owner: "gb-gone"}, "LIVE: under the running host of terminal gb-live"},
		{"a detached tree of a goblin that works", janitor.ProcessItem{PID: 5, Started: at(5), Owner: "gb-live"}, "LIVE: detached from terminal gb-live, whose goblin works"},
		{"a detached tree of a goblin that delivered and rests", janitor.ProcessItem{PID: 4, Started: at(4), Owner: "gb-rests"}, ""},
		{"a leftover of the CFO's terminal", janitor.ProcessItem{PID: 4, Started: at(4), Owner: "cfo"}, "CFO: a process of the CFO's terminal"},
		{"what a terminal with no host left", janitor.ProcessItem{PID: 4, Started: at(4), Owner: "gb-gone"}, ""},
		{"a pid another process took since the plan", janitor.ProcessItem{PID: 1, Started: at(99), Owner: "gb-gone"}, ""},
	} {
		t.Run("the sweep: "+test.name, func(t *testing.T) {
			// Act
			flag := standing.sweepFlag(test.item, owners)

			// Assert
			if flag != test.want {
				t.Errorf("flag = %q, want %q", flag, test.want)
			}
		})
	}
	for _, test := range []struct {
		name string
		task string
		pid  int
		want string
	}{
		{"a desktop program", "gb-live", 1, "HIS: a desktop program, or what one started"},
		{"a process of the CFO's terminal", "gb-live", 2, "CFO: a process of the CFO's terminal"},
		{"the CFO terminal's own process, for the CFO's terminal", "cfo", 2, ""},
		{"a process that carries another terminal's mark", "gb-live", 4, "OTHER: it carries the mark of terminal gb-other"},
		{"a process under another terminal's host", "gb-live", 6, "OTHER: under the running host of terminal gb-other"},
		{"the task's own, by its mark and under its host", "gb-live", 5, ""},
		{"the task's own, under its host", "gb-live", 3, ""},
	} {
		t.Run("a task: "+test.name, func(t *testing.T) {
			// Act
			flag := standing.taskFlag(test.task, test.pid, at(test.pid))

			// Assert
			if flag != test.want {
				t.Errorf("flag = %q, want %q", flag, test.want)
			}
		})
	}
	if memory := standing.memory(3, at(3)); memory != 7<<20 {
		t.Errorf("memory of a known process = %d, want %d", memory, 7<<20)
	}
	if memory := standing.memory(3, at(99)); memory != 0 {
		t.Errorf("memory of a pid another process took = %d, want none", memory)
	}
}
