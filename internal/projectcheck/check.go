package projectcheck

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Options name the project to assess and how to look at it.
type Options struct {
	// DataDir is the home's data folder, which holds the project's files
	// under projects\<checkout folder>.
	DataDir string
	// Checkout is the project's checkout. A folder that is missing or is no
	// git checkout is a finding, and what needs no repository is still read.
	Checkout string
	// PolicyFile is the home's pipeline policy, which says whether a gate
	// run takes an agent a gate file pins. Empty leaves a pin unjudged.
	PolicyFile string
	// Runner runs git, the only program an assessment starts.
	Runner execx.Runner
	// LookPath finds a program by name the way a shell would.
	LookPath func(string) (string, error)
	// Now is the time of the run, which the age of a reading is counted
	// from. Nil is the clock.
	Now func() time.Time
}

// checker is one assessment in progress.
type checker struct {
	Options
	project string
	// repo is the checkout as git reads it, nil when this machine has none.
	repo   *repository
	lines  []Finding
	proven proven
	// plan is what a worktree of the project is given and what its install
	// step makes, read once.
	plan *worktreePlan
}

// homeOnly is what Unread says of an area read as far as the home's own
// files go, with no repository to compare them with.
const homeOnly = "the home's files alone"

// Check assesses one project and returns every line it can prove.
func Check(ctx context.Context, o Options) (Report, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if absolute, err := filepath.Abs(o.Checkout); err == nil {
		o.Checkout = absolute
	}
	c := &checker{Options: o, project: projectName(o.Checkout, o.DataDir)}
	if typed := filepath.Base(o.Checkout); typed != c.project && sameName(typed, c.project) {
		c.Checkout = filepath.Join(filepath.Dir(o.Checkout), c.project)
	}
	repo, err := openRepository(ctx, o.Runner, c.Checkout, o.Now())
	var unread map[string]string
	if err != nil {
		c.checkoutMissing(err)
		unread = map[string]string{AreaGate: notAssessed, AreaInstructions: notAssessed, AreaConfigs: homeOnly, AreaConnectors: homeOnly}
		c.record()
	} else {
		c.repo = repo
		c.reading(ctx)
		c.record()
		c.productionReach(ctx, c.gate(ctx))
	}
	if err := c.configs(ctx); err != nil {
		return Report{}, err
	}
	if err := c.connectors(ctx); err != nil {
		return Report{}, err
	}
	if c.repo != nil {
		c.instructions(ctx)
	}
	draft, tier := c.draft()
	return Report{Project: c.project, Checkout: c.Checkout, Lines: c.lines, Draft: draft, DraftTier: tier, Unread: unread}, nil
}

// sameName reports whether two names are one project's. A Windows folder
// answers to its name in any letter case, so there the case is not compared.
func sameName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// projectName returns the name the home keys the project's files by: the
// checkout's folder as it is spelled on disk, which is not always how it was
// typed, or for a project with no checkout, its folder under the home's
// projects. A name that is on no disk stays as it was typed.
func projectName(checkout, dataDir string) string {
	typed := filepath.Base(checkout)
	for _, folder := range []string{filepath.Dir(checkout), filepath.Join(dataDir, "projects")} {
		entries, err := os.ReadDir(folder)
		if err != nil {
			continue
		}
		spelled := ""
		for _, entry := range entries {
			switch name := entry.Name(); {
			case !entry.IsDir():
			case name == typed:
				return typed
			case sameName(name, typed):
				spelled = name
			}
		}
		if spelled != "" {
			return spelled
		}
	}
	return typed
}

// checkoutMissing reports a project this machine has no checkout of.
func (c *checker) checkoutMissing(why error) {
	evidence := c.Checkout + " is no folder on this machine"
	if _, err := os.Stat(c.Checkout); err == nil {
		evidence = strings.TrimPrefix(why.Error(), "projectcheck: ")
	}
	c.add(AreaCheckout, "checkout-missing", High,
		"this machine has no checkout of the project, so nothing of its repository was read: its gate and its instructions are not assessed, and its configs and its connectors only as far as the home's own files go",
		evidence,
		"clone the project under the projects root and run the check again, or retire its folder under the home's projects when the project is gone")
}

// reading says what was read of the repository and how old that reading is.
// A spawn fetches before it cuts a worktree, so a default branch the remote
// has moved past is one whose files a goblin does not get. The remote is
// asked where it is, which writes nothing to the repository, and what git
// says when it cannot answer is never printed, since it can hold the
// remote's address.
func (c *checker) reading(ctx context.Context) {
	r := c.repo
	if r.remote == "" {
		c.add(AreaCheckout, "checkout-read", OK,
			"read the folder's own commit, since the checkout names no default branch of a remote",
			r.at()+" of "+c.Checkout, "")
		return
	}
	asked := "git ls-remote " + r.remote + " refs/heads/" + r.branch
	head, answered := r.remoteHead(ctx)
	switch {
	case !answered:
		c.add(AreaCheckout, "remote-unanswered", Low,
			"the remote did not answer, so whether its default branch has moved since the last fetch is not known",
			asked+" gave no commit. Read at "+r.asRead(),
			"check the network and the sign-in git uses for the remote, and run the check again")
	case head != r.full:
		c.add(AreaCheckout, "remote-moved", Medium,
			"the remote's default branch has moved since this checkout last fetched it, so a spawn, which fetches first, gives a goblin files this check did not read",
			asked+" answers "+head[:min(len(head), len(r.commit))]+", and this check read "+r.asRead(),
			"run git fetch in the checkout, which changes none of its files, and run the check again")
	default:
		c.add(AreaCheckout, "checkout-read", OK,
			"read the default branch "+r.ref+", and the remote is at the same commit",
			asked+" answers "+r.commit+". Read at "+r.asRead(), "")
	}
}

// add appends one line to the report.
func (c *checker) add(area, check string, severity Severity, says, evidence, fix string) {
	c.lines = append(c.lines, Finding{Area: area, Check: check, Severity: severity, Says: says, Evidence: evidence, Fix: fix})
}
