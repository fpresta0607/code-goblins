package projectcheck

import (
	"context"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Options name the project to assess and how to look at it.
type Options struct {
	// DataDir is the home's data folder, which holds the project's files
	// under projects\<checkout folder>.
	DataDir string
	// Checkout is the project's checkout.
	Checkout string
	// Runner runs git, the only program an assessment starts.
	Runner execx.Runner
	// LookPath finds a program by name the way a shell would.
	LookPath func(string) (string, error)
}

// checker is one assessment in progress.
type checker struct {
	Options
	project string
	repo    *repository
	lines   []Finding
	proven  proven
}

// Check assesses one project and returns every line it can prove.
func Check(ctx context.Context, o Options) (Report, error) {
	repo, err := openRepository(ctx, o.Runner, o.Checkout)
	if err != nil {
		return Report{}, err
	}
	c := &checker{Options: o, project: filepath.Base(filepath.Clean(o.Checkout)), repo: repo}
	c.record()
	c.productionReach(ctx, c.gate(ctx))
	if err := c.configs(ctx); err != nil {
		return Report{}, err
	}
	if err := c.connectors(ctx); err != nil {
		return Report{}, err
	}
	c.instructions(ctx)
	return Report{Project: c.project, Checkout: o.Checkout, Lines: c.lines, Draft: c.draft()}, nil
}

// add appends one line to the report.
func (c *checker) add(area, check string, severity Severity, says, evidence, fix string) {
	c.lines = append(c.lines, Finding{Area: area, Check: check, Severity: severity, Says: says, Evidence: evidence, Fix: fix})
}
