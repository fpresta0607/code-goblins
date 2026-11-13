package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// A pre-tool hook's own work, after goblins.exe has started, is reading its
// payload and one of the home's files, with no child process, symlink
// resolution, Herdr call or walk of the process tree on the path. Its budget
// is what it does, counted: hookBodyOpens files opened, and no list of the
// machine's processes, which every walk of the process tree takes.
//
// Until 2026-10 the budget was a median of 5 ms and a 95th percentile of
// 50 ms, said to be broken by any of those and by no loaded machine. Measured
// on 2026-10-09, neither held. Beside a cold build the same hook, with
// nothing added, took 5 to 16 ms at the median and 34 to 181 ms at the 95th
// percentile, so the test failed on main. And on a quiet machine, where the
// hook takes 0.6 ms and a walk of the process tree 3.5 ms, a hook with a walk
// added would have passed.
//
// No time is asserted, because every yardstick tried measured the machine
// before the hook. A list of the machine's processes, timed beside the hook,
// cost six times the hook on a workstation running hundreds of processes and
// a third of it on a CI runner running few. The hook's time is logged.
const hookBodyOpens = 1

// The CEO's pre-tool hooks run before every tool call they select, so what
// each does after starting stays within its budget.
func TestTheCEOsPreToolHooksStayWithinTheirBudget(t *testing.T) {
	// Arrange
	newCEOHome(t)
	bash := `{"session_id":"s","tool_name":"Bash","tool_input":{"command":"git log --oneline"}}`
	cases := []struct{ hook, payload string }{
		{"pretool-bash", bash},
		{"pretool-powershell", `{"session_id":"s","tool_name":"PowerShell","tool_input":{"command":"git log --oneline"}}`},
		{"pretool-arm", bash},
		{"pretool-subagent", `{"session_id":"s","tool_name":"TaskCreate"}`},
	}
	const runs = 40
	for _, c := range cases {
		durations := make([]time.Duration, runs)
		opened, listed := fsx.Opens(), proc.Lists()
		for i := range durations {
			var stdout, stderr bytes.Buffer
			start := time.Now()

			// Act
			exit := runHook(c.hook, strings.NewReader(c.payload), &stdout, &stderr)

			durations[i] = time.Since(start)
			if exit != 0 {
				t.Fatalf("%s exit %d, stderr %q; want the call allowed", c.hook, exit, stderr.String())
			}
		}
		opened, listed = fsx.Opens()-opened, proc.Lists()-listed

		// Assert
		if opened != runs*hookBodyOpens || listed != 0 {
			t.Errorf("%s opened %d file(s) and listed the machine's processes %d time(s) in %d runs, want %d file(s) a run and no list", c.hook, opened, listed, runs, hookBodyOpens)
		}
		slices.Sort(durations)
		t.Logf("%s: median %v, 95th percentile %v", c.hook, durations[runs/2], durations[runs*95/100])
	}
}
