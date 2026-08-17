package main

import (
	"context"
	"fmt"
	"io"

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
func runDoctor(stdout io.Writer) int {
	checks := doctor.Run()
	for _, c := range checks {
		if c.Err != "" {
			fmt.Fprintf(stdout, "MISSING  %-10s %s (install: %s)\n", c.Name, c.Err, c.Hint)
		} else {
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
		fmt.Fprintln(stdout, "telemetry: measured invocation minutes per agent and step")
		fmt.Fprintln(stdout, "  agent     step                        count   avg min   max min")
		for _, r := range rows {
			fmt.Fprintf(stdout, "  %-9s %-25s %5d %9.1f %9.1f\n", r.Agent, r.Step, r.Count, r.AvgMin, r.MaxMin)
		}
	}

	reportRouting(stdout)

	if !healthy {
		return 1
	}
	return 0
}

// reportRouting prints the standing switch policy, because a rule that
// silently restarts a goblin's harness should be visible in the same place
// the operator checks everything else.
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
		return
	}
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
