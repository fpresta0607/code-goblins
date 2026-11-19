package supervisor

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/janitor"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// gateProject is the gate's checkout under the projects root, whose
// repository a root issue of the gate's home goes on.
const gateProject = "no-mistakes"

// gateCause is one reason the gate's home keeps completed runs' checkouts past
// its retention, and the root issue that names it on the gate's repository
// when the gate's own code is the cause.
type gateCause struct {
	name     string
	runs     func(janitor.GatePastRetention) janitor.GateRuns
	wakeText string
	root     *tickets.Root
}

// gateCauses are the reasons the janitor tells apart. A junction's is the
// gate's reaper skipping it, whose root key is its open issue's
// (no-mistakes#261), so a filing comments there rather than open another. A
// folder of the gate's own is one its reaper reaches, and the gate's current
// source removes a completed run's checkout there after its retention, so
// what keeps one is the installed gate or a new cause its log shows; the CEO
// finds which before anything is filed.
var gateCauses = []gateCause{
	{
		name:     "junction",
		runs:     func(past janitor.GatePastRetention) janitor.GateRuns { return past.Linked },
		wakeText: "under repository folders that are directory junctions, which its reaper skips; this cause is filed as a root issue on the gate's repository",
		root: &tickets.Root{
			Title: "Worktree reaper skips a repository folder that is a directory junction",
			Key:   "worktree reaper skips a repository folder that is a directory junction",
			Body:  "## What happens\n\nThe gate's home keeps completed runs' checkouts past `worktree.retention` under repository folders in `<NM_HOME>/worktrees` that are directory junctions, which the worktree reaper skips, so nothing bounds them.\n\nThe Code Goblins janitor found it: it counts the checkouts of runs the gate's database records as completed that were last written longer ago than the retention, and tells apart those under a junction.",
		},
	},
	{
		name:     "own",
		runs:     func(past janitor.GatePastRetention) janitor.GateRuns { return past.Unlinked },
		wakeText: "under repository folders of its own, which its reaper reaches and the gate's current source removes after its retention; next: check that the installed gate (no-mistakes --version) is as new as its repository's default branch, read the gate's daemon log for why it keeps each, and file what that shows with goblins issue file",
	},
}

// checkGateGrowth wakes the CEO once when the janitor's last sweep found the
// gate's home keeping completed runs' checkouts past the gate's own retention
// for a cause it had not woken for, names each cause, and files a new one
// whose root is the gate's own code on the gate's repository. A cause the
// sweep no longer finds is forgotten, so it wakes again when it comes back.
// The fleet never removes anything in the gate's home: its own reaper does.
func (s *Service) checkGateGrowth(w *fleetWakes, now time.Time) error {
	record, err := janitor.ReadRecord(s.Store.Home.State)
	if err != nil || record.Gate == nil || record.Gate.PastRetention == nil {
		return nil
	}
	past := *record.Gate.PastRetention
	var found, fresh []gateCause
	for _, cause := range gateCauses {
		if cause.runs(past).Count == 0 {
			continue
		}
		found = append(found, cause)
		if !slices.Contains(w.GateCauses, cause.name) {
			fresh = append(fresh, cause)
		}
	}
	w.GateCauses = slices.DeleteFunc(w.GateCauses, func(name string) bool {
		return !slices.ContainsFunc(found, func(cause gateCause) bool { return cause.name == name })
	})
	if len(fresh) == 0 {
		return nil
	}
	held := disk.GB(uint64(record.Gate.Bytes))
	var parts []string
	for _, cause := range found {
		runs := cause.runs(past)
		parts = append(parts, fmt.Sprintf("%d %s (the oldest last written %s ago)", runs.Count, cause.wakeText, durationText(now.Sub(runs.Oldest).Round(time.Hour))))
	}
	detail := fmt.Sprintf("gate_growth: the gate's home at %s holds %.1f GB and keeps completed runs' checkouts past its %s worktree.retention: %s. The fleet never removes anything in the gate's home.",
		strings.Join(record.Gate.Folders, " and "), held, durationText(past.Retention), strings.Join(parts, "; "))
	if err := raiseFleetWake(s.Store.Home.State, "disk", "gate-growth", detail); err != nil {
		return err
	}
	for _, cause := range fresh {
		if cause.root != nil {
			runs := cause.runs(past)
			root := *cause.root
			root.Evidence = fmt.Sprintf("%d completed runs' checkouts kept past the %s retention this way, the oldest last written %s ago; the gate's home holds %.1f GB in all.", runs.Count, durationText(past.Retention), durationText(now.Sub(runs.Oldest).Round(time.Hour)), held)
			s.fileFoundRoot(gateProject, root)
		}
		w.GateCauses = append(w.GateCauses, cause.name)
	}
	return nil
}

// durationText is a whole duration as a person reads it, 24h rather than
// 24h0m0s.
func durationText(d time.Duration) string {
	return strings.TrimSuffix(strings.TrimSuffix(d.String(), "0s"), "0m")
}
