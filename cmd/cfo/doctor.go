package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/fpresta0607/code-goblins/internal/doctor"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/routing"
	"github.com/fpresta0607/code-goblins/internal/telemetry"
)

// runDoctor prints the tool checks, then a per-harness spawn sanity verdict
// (ok/broken from real --version probes), then the measured speed table from
// the no-mistakes telemetry database when one is available. A broken harness
// is unhealthy: every pipeline attempt on it is wasted time.
func runDoctor(stdout io.Writer, runtime commandRuntime) int {
	checks := doctor.Run()
	for _, c := range checks {
		switch {
		case c.Err != "" && c.Presentation:
			fmt.Fprintf(stdout, "PRESENTATION_UNAVAILABLE %s %s (requires >=%s; install: %s) - nonvisual work proceeds in plain text\n", c.Name, c.Err, c.Floor, c.Hint)
		case c.Err != "":
			fmt.Fprintf(stdout, "MISSING  %-10s %s (install: %s)\n", c.Name, c.Err, c.Hint)
		case c.Floor != "":
			fmt.Fprintf(stdout, "ok       %-10s %s (floor %s)\n", c.Name, c.Version, c.Floor)
		default:
			fmt.Fprintf(stdout, "ok       %-10s %s\n", c.Name, c.Version)
		}
	}
	healthy := doctor.Healthy(checks)

	probes := doctor.ProbeHarnesses(context.Background())
	for _, p := range probes {
		if p.OK {
			fmt.Fprintf(stdout, "ok       %-10s harness spawn probe: %s\n", p.Name, p.Detail)
		} else {
			fmt.Fprintf(stdout, "broken   %-10s harness spawn probe: %s\n", p.Name, p.Detail)
			healthy = false
		}
	}

	querier := telemetry.Querier{Commands: execx.OSRunner{}, DBPath: telemetry.DefaultDBPath()}
	rows, note := querier.SpeedTable(context.Background())
	if note != "" {
		fmt.Fprintf(stdout, "telemetry: skipped (%s)\n", note)
	} else {
		fmt.Fprintln(stdout, "telemetry: validation invocation minutes; error/cancelled rows are failure latency, not successful speed")
		fmt.Fprintln(stdout, "  agent     model                    role           step            outcome    count   avg min   max min")
		for _, r := range rows {
			fmt.Fprintf(stdout, "  %-9s %-24s %-14s %-15s %-10s %5d %9.1f %9.1f\n", r.Agent, r.Model, r.Role, r.Step, r.Outcome, r.Count, r.AvgMin, r.MaxMin)
		}
	}
	fmt.Fprintln(stdout, "telemetry: implementation unmeasured (the gate database records validation agents only)")

	reportRouting(stdout)
	reportProjectsRoot(stdout, runtime)

	if !healthy {
		return 1
	}
	return 0
}

// reportProjectsRoot prints where a bare `--project <name>` is looked up, or
// how to record it when nothing is. It never counts against the health
// verdict: every command still takes a path without it.
func reportProjectsRoot(stdout io.Writer, runtime commandRuntime) {
	root := ""
	if runtime.projectsRoot != nil {
		var err error
		if root, err = runtime.projectsRoot(); err != nil {
			fmt.Fprintf(stdout, "projects root: unreadable (%v)\n", err)
			return
		}
	}
	if root == "" {
		fmt.Fprintln(stdout, "projects root: not set, so --project takes a path only; run `cfo install --projects-root <dir>` with the folder that holds your checkouts to use bare names")
		return
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		fmt.Fprintf(stdout, "projects root: %s is not a directory; record the right folder with `cfo install --projects-root <dir>`\n", root)
		return
	}
	fmt.Fprintf(stdout, "projects root: %s (--project <name> resolves to a checkout under it)\n", root)
}

// reportRouting prints the standing switch policy and the execution lane
// table, because a rule that silently restarts a goblin's harness and the
// model each kind of work is dispatched on should both be visible in the
// same place the operator checks everything else.
func reportRouting(stdout io.Writer) {
	h, err := home.Resolve()
	if err != nil {
		return
	}
	policy, err := routing.Load(h.Data)
	if err != nil {
		fmt.Fprintf(stdout, "routing: unreadable (%v)\n", err)
		return
	}
	if len(policy.Rules) == 0 {
		fmt.Fprintf(stdout, "routing: no standing switch rules (add %s to answer a harness fault automatically)\n", policy.Path)
		fmt.Fprintln(stdout, "  a goblin whose harness starts erroring wakes the CFO undecided; fix it with `cfo switch <id> --harness <h>`")
	} else {
		fmt.Fprintf(stdout, "routing: %d standing switch rule(s) from %s\n", len(policy.Rules), policy.Path)
		for _, rule := range policy.Rules {
			from := rule.Harness
			if from == "" {
				from = "any"
			}
			mode := "recommend"
			if rule.Auto {
				mode = "automatic"
			}
			fmt.Fprintf(stdout, "  %-9s %-11s %-9s %s\n", from, rule.Fault, mode, rule.Command("<id>"))
		}
	}
	table, err := policy.LaneTable()
	if err != nil {
		fmt.Fprintf(stdout, "routing: lane table invalid (%v)\n", err)
		return
	}
	if len(table.Lanes) == 0 {
		fmt.Fprintf(stdout, "routing: no execution lanes (add lanes to %s so `cfo spawn` without --harness can pick a model)\n", policy.Path)
		return
	}
	fmt.Fprintf(stdout, "routing: %d execution lane(s) from %s (default %s, escalate to %s)\n", len(table.Lanes), policy.Path, table.DefaultLane, valueOr(table.EscalateTo, "none"))
	names := make([]string, 0, len(table.Lanes))
	for name := range table.Lanes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		lane := table.Lanes[name]
		fmt.Fprintf(stdout, "  %-11s %-7s %-8s %-7s %s\n", name, lane.Harness, valueOr(lane.Model, "default"), valueOr(lane.Effort, "default"), lane.Note)
	}
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
