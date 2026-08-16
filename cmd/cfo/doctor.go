package main

import (
	"context"
	"fmt"
	"io"

	"github.com/fpresta0607/code-goblins/internal/doctor"
	"github.com/fpresta0607/code-goblins/internal/execx"
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

	if !healthy {
		return 1
	}
	return 0
}
