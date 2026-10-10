package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/janitor"
)

// examplePlan is a plan with one process in each list, and one would-end
// process of the sweep and one of a task flagged as somebody else's when
// flagged is set.
func examplePlan(flagged bool) processPlan {
	began := time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC)
	plan := processPlan{
		At: began,
		Sweep: janitor.ProcessPlan{
			Read:   412,
			Owners: []janitor.Owner{{ID: "gb-gone"}, {ID: "gb-live", HostPID: 20}},
			Ending: []janitor.ProcessItem{{PID: 10, Started: began, Name: "node.exe", Owner: "gb-gone", Memory: 300 << 20, Command: "node server.js", Why: "its terminal gb-gone is gone"}},
			Left:   []janitor.ProcessItem{{PID: 70, Started: began, Name: "node.exe", Memory: 900 << 20, Command: "node bridge.js", Why: "a browser bridge nothing ties to an owner"}},
			Kept:   []janitor.ProcessItem{{PID: 21, Started: began, Name: "claude.exe", Owner: "gb-live", Memory: 400 << 20, Why: "terminal gb-live runs and its host reaches it"}},
			Notes:  []string{"terminal gb-odd's proofs could not be read, so its processes were left: access denied"},
		},
		Later: []janitor.ProcessItem{{PID: 30, Started: began, Name: "node.exe", Owner: "gb-live", Memory: 50 << 20, Why: "detached from terminal gb-live and idle since 04:00Z"}},
		Flags: map[int]string{},
		Tasks: []taskProcessPlan{
			{ID: "gb-live", HostPID: 20, Ending: []plannedProcess{{PID: 21, Name: "claude.exe", Memory: 400 << 20, By: "mark"}, {PID: 23, Name: "tail.exe", Memory: 2 << 20, By: "place"}}},
			{ID: "gb-odd", Err: "task worktree is not its isolated project worktree"},
		},
	}
	if flagged {
		plan.Flags[30] = "LIVE: detached from terminal gb-live, whose goblin works"
		plan.Tasks[0].Ending[1].Flag = "CFO: a process of the CFO's terminal"
	}
	return plan
}

// The plan is read before a build that ends processes is installed, so what
// must be acted on comes first: every would-end process that looks like the
// Overlord's, the CFO terminal's or a running goblin's. A plan with none
// says so in as many words, where an empty heading would read as not looked.
func TestAProcessPlanPutsItsAlarmsFirstAndSaysSoWhenThereAreNone(t *testing.T) {
	for _, test := range []struct {
		name    string
		flagged bool
		want    []string
		absent  []string
	}{
		{name: "nothing of anybody else's would end", want: []string{
			"## Alarms\n\nNone. No would-end list holds a desktop program, a process of the CFO's terminal, a process of a goblin that works, or a process that is another terminal's.",
		}, absent: []string{"HIS:", "CFO:", "LIVE:", "OTHER:"}},
		{name: "a working goblin's tree and a process of the CFO's would end", flagged: true, want: []string{
			"## Alarms\n\n- The sweep would end node.exe pid 30, 50.0 MB: LIVE: detached from terminal gb-live, whose goblin works.\n- A cleanup or a relaunch of gb-live would end tail.exe pid 23, 2.0 MB: CFO: a process of the CFO's terminal.",
		}, absent: []string{"None. No would-end list"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			out := renderProcessPlan(examplePlan(test.flagged))

			// Assert
			for _, want := range append(test.want,
				"Read at 2026-10-10 04:00:00Z from 412 processes and 2 terminals of this home. Nothing was ended and nothing was written.",
				"### Would end now\n\n1 processes holding 300.0 MB.",
				"| 10 | node.exe | 300.0 MB | gb-gone | its terminal gb-gone is gone |  | node server.js |",
				"### Would end at a sweep an hour on, if each goblin that rests stays at rest and nothing it watches does anything meanwhile\n\n1 processes holding 50.0 MB.",
				"### Would name for the CFO and leave running\n\n1 processes holding 900.0 MB.",
				"| 21 | claude.exe | 400.0 MB | gb-live | terminal gb-live runs and its host reaches it |",
				"- terminal gb-odd's proofs could not be read, so its processes were left: access denied",
				"### gb-live\n\nIts terminal runs, host pid 20. It would end 2 processes holding 402.0 MB.",
				"| 23 | tail.exe | 2.0 MB | its place in the task's folders, with no parent of somebody else's running |",
				"### gb-odd\n\nIts terminal has no host. It would end 0 processes holding 0.0 MB.\n\nIt could not be planned: task worktree is not its isolated project worktree",
			) {
				if !strings.Contains(out, want) {
					t.Errorf("the plan does not hold %q:\n%s", want, out)
				}
			}
			for _, absent := range test.absent {
				if strings.Contains(out, absent) {
					t.Errorf("the plan holds %q, which it should not:\n%s", absent, out)
				}
			}
			if alarms, sweep := strings.Index(out, "## Alarms"), strings.Index(out, "## The janitor's sweep"); alarms < 0 || sweep < alarms {
				t.Errorf("the alarms do not come before the sweep's lists:\n%s", out)
			}
		})
	}
}

func TestRunProcessPlanPrintsThePlanOfItsHomeAndRefusesArguments(t *testing.T) {
	// Arrange
	homeRoot := t.TempDir()
	t.Setenv("CFO_HOME", homeRoot)
	deps := defaultCommandRuntime()
	deps.processPlan = func(_ context.Context, h home.Home) (processPlan, error) {
		if h.Root != homeRoot {
			t.Errorf("home = %+v, want CFO_HOME %q", h, homeRoot)
		}
		return examplePlan(false), nil
	}

	// Act
	var out, errOut bytes.Buffer
	exit := runWithRuntime([]string{"process-plan"}, &out, &errOut, deps)
	var extraOut, extraErr bytes.Buffer
	extraExit := runWithRuntime([]string{"process-plan", "gb-live"}, &extraOut, &extraErr, deps)
	deps.processPlan = func(context.Context, home.Home) (processPlan, error) {
		return processPlan{}, errors.New("read the machine's processes: access denied")
	}
	var failedOut, failedErr bytes.Buffer
	failedExit := runWithRuntime([]string{"process-plan"}, &failedOut, &failedErr, deps)

	// Assert
	if exit != 0 || errOut.Len() != 0 || !strings.HasPrefix(out.String(), "# What the process sweeps would end\n") {
		t.Errorf("exit = %d, stderr = %q, stdout = %q, want the plan alone", exit, errOut.String(), out.String())
	}
	if extraExit != 2 || extraOut.Len() != 0 || !strings.Contains(extraErr.String(), "usage: cfo process-plan") {
		t.Errorf("with an argument: exit = %d, stdout = %q, stderr = %q, want its usage and nothing printed", extraExit, extraOut.String(), extraErr.String())
	}
	if failedExit != 1 || failedOut.Len() != 0 || !strings.Contains(failedErr.String(), "cfo process-plan: read the machine's processes: access denied") {
		t.Errorf("with a machine that cannot be read: exit = %d, stdout = %q, stderr = %q, want the error and no plan", failedExit, failedOut.String(), failedErr.String())
	}
}
