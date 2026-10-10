package janitor

import (
	"reflect"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lifecycle"
)

// A dry run against the live machine on 2026-10-10 found the sweep could end
// a working goblin's background command an hour after first seeing it. Every
// background command a harness starts through Git Bash is detached from its
// terminal's host, and the sweep judged such a tree idle by the processor
// time of its live processes added up. That sum falls when a child that used
// the processor exits, so a long test run between two of its test programs
// read as having done less than before, which the rule took for idle. A tree
// is judged by its own processes now: one joining or leaving it is work, and
// so is processor time any of them used. The goblin here rests, so nothing
// but that measure keeps its tree.
func TestATreeIsIdleOnlyWhenNothingInItChangedOrUsedTheProcessor(t *testing.T) {
	started := time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC)
	mark := lifecycle.Mark{Terminal: "gb-task", ProofSum: "own-digest"}
	owners := []Owner{{ID: "gb-task", Marks: []lifecycle.Mark{mark}, HostPID: 1, AtRestSince: started}}
	firstSeen := started.Add(10 * time.Minute)
	now := firstSeen.Add(time.Hour)
	// run is a process of the tree: its first, pid 10, whose parent is gone,
	// or one under it.
	run := func(pid int, name string, start time.Duration, cpu time.Duration) Process {
		parent := 10
		if pid == 10 {
			parent = 900
		}
		return Process{Process: lifecycle.Process{PID: pid, ParentPID: parent, Name: name, Started: started.Add(start), Mark: mark}, CPU: cpu}
	}
	host := Process{Process: lifecycle.Process{PID: 1, Name: "cfo.exe", Started: started}}
	// At the first sweep: go test and its first test program, which had used
	// 100 seconds of processor time.
	seen := []Watched{{PID: 10, Started: started.Add(time.Minute), Since: firstSeen, Members: []WatchedMember{
		{PID: 10, Started: started.Add(time.Minute), CPU: 20 * time.Second},
		{PID: 11, Started: started.Add(2 * time.Minute), CPU: 100 * time.Second},
	}}}
	for _, test := range []struct {
		name    string
		tree    []Process
		watched []Watched
		want    planned
		since   time.Time
	}{
		{name: "its first test program exited and a second has used less", watched: seen, tree: []Process{host,
			run(10, "go.exe", time.Minute, 22*time.Second), run(12, "spawn.test.exe", 40*time.Minute, 30*time.Second),
		}, want: planned{Watching: []int{10}}, since: now},
		{name: "a child exited and nothing took its place", watched: seen, tree: []Process{host,
			run(10, "go.exe", time.Minute, 20*time.Second),
		}, want: planned{Watching: []int{10}}, since: now},
		{name: "a process joined it", watched: seen, tree: []Process{host,
			run(10, "go.exe", time.Minute, 20*time.Second), run(11, "spawn.test.exe", 2*time.Minute, 100*time.Second), run(13, "git.exe", 50*time.Minute, 0),
		}, want: planned{Watching: []int{10}}, since: now},
		{name: "one of its processes used the processor", watched: seen, tree: []Process{host,
			run(10, "go.exe", time.Minute, 20*time.Second), run(11, "spawn.test.exe", 2*time.Minute, 101*time.Second),
		}, want: planned{Watching: []int{10}}, since: now},
		{name: "another process took a child's pid", watched: seen, tree: []Process{host,
			run(10, "go.exe", time.Minute, 20*time.Second), run(11, "spawn.test.exe", 30*time.Minute, 100*time.Second),
		}, want: planned{Watching: []int{10}}, since: now},
		{name: "the same processes, and none used the processor", watched: seen, tree: []Process{host,
			run(10, "go.exe", time.Minute, 20*time.Second), run(11, "spawn.test.exe", 2*time.Minute, 100*time.Second+50*time.Millisecond),
		}, want: planned{Ending: []int{10, 11}}},
		{name: "a record from before its processes were kept", watched: []Watched{{PID: 10, Started: started.Add(time.Minute), Since: firstSeen}}, tree: []Process{host,
			run(10, "go.exe", time.Minute, 20*time.Second), run(11, "spawn.test.exe", 2*time.Minute, 100*time.Second),
		}, want: planned{Watching: []int{10}}, since: now},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			ending, left, watching := planProcesses(test.tree, owners, test.watched, now)

			// Assert
			var kept []int
			for _, watch := range watching {
				kept = append(kept, watch.PID)
			}
			if got := (planned{Ending: pidsOf(ending), Left: pidsOf(left), Watching: kept}); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("plan = %+v, want %+v", got, test.want)
			}
			if len(watching) == 1 && !watching[0].Since.Equal(test.since) {
				t.Errorf("the tree is counted idle since %s, want %s", watching[0].Since, test.since)
			}
			if len(watching) == 1 && len(watching[0].Members) != len(test.tree)-1 {
				t.Errorf("the watch keeps %d of the tree's %d processes, want each", len(watching[0].Members), len(test.tree)-1)
			}
		})
	}
}

// A process a goblin waits on is never idle while its goblin works: a gate
// waiter that blocks for hours without using the processor, a watch on a
// quiet file, a browser it will drive again. Nothing the sweep can read says
// which of a working goblin's processes it still waits on, so none of them
// is judged idle. They end with the goblin, at its pause, its stop, its
// cleanup or its relaunch, or once its terminal is gone. Once the goblin has
// delivered and rests, what it left sits idle from then on, and is ended
// when both the rest and the stillness have lasted the hour.
func TestNothingOfAWorkingGoblinsIsIdleHoweverLongItSatStill(t *testing.T) {
	started := time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC)
	mark := lifecycle.Mark{Terminal: "gb-task", ProofSum: "own-digest"}
	firstSeen := started.Add(10 * time.Minute)
	now := firstSeen.Add(3 * time.Hour)
	bridge := []string{"node", `C:\npm\chrome-devtools-axi\dist\bin\chrome-devtools-axi-bridge.js`}
	processes := []Process{
		{Process: lifecycle.Process{PID: 1, Name: "cfo.exe", Started: started}},
		// A gate waiter, blocked since it started.
		{Process: lifecycle.Process{PID: 10, ParentPID: 900, Name: "no-mistakes.exe", Started: started.Add(time.Minute), Mark: mark}, CPU: time.Second},
		// A browser bridge nothing has driven for two hours.
		{Process: lifecycle.Process{PID: 20, ParentPID: 901, Name: "node.exe", Started: started.Add(2 * time.Minute), Arguments: bridge, Mark: mark}, CPU: 40 * time.Minute, LastUsed: now.Add(-2 * time.Hour)},
	}
	watched := []Watched{{PID: 10, Started: started.Add(time.Minute), Since: firstSeen, Members: []WatchedMember{{PID: 10, Started: started.Add(time.Minute), CPU: time.Second}}}}
	for _, test := range []struct {
		name        string
		atRestSince time.Time
		want        planned
	}{
		{name: "its goblin works", want: planned{Watching: []int{10}}},
		{name: "its goblin delivered and has rested for half an hour", atRestSince: now.Add(-30 * time.Minute), want: planned{Watching: []int{10}}},
		{name: "its goblin delivered and has rested for an hour", atRestSince: now.Add(-time.Hour), want: planned{Ending: []int{10, 20}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			owners := []Owner{{ID: "gb-task", Marks: []lifecycle.Mark{mark}, HostPID: 1, AtRestSince: test.atRestSince}}

			// Act
			got := planOf(processes, owners, watched, now)

			// Assert
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("plan = %+v, want %+v", got, test.want)
			}
		})
	}
}
