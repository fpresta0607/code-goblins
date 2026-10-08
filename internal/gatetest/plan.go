package gatetest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Plan is what the step runs for the branch checked out in a directory.
type Plan struct {
	// Root is the repository's top directory and Base where the branch left
	// the default branch.
	Root string
	Base string
	// Commit is the commit checked out, and Uncommitted how many files
	// differ from it, modified or untracked.
	Commit      string
	Uncommitted int
	// Module is the module's path and Toolchain the Go that builds it.
	Module    string
	Toolchain string
	// Policy says which policy the plan follows and where it was read.
	Policy string
	// Level is the level the plan runs, Required the level the change
	// requires before it merges, and Why the reason it requires that one.
	Level    Level
	Required Level
	Why      string
	// Choices are the packages the change reaches, with why.
	Choices []Choice
	// Everything says a module file changed, which reaches every package.
	Everything bool
	// Outside are the changed files the policy puts outside the Go checks, by
	// its reason, and Unknown the changed files it does not account for,
	// which make the change require the full level. Both are empty under a
	// policy that classifies nothing.
	Outside []Set
	Unknown []string
	// Vet and Tests are the packages Level vets and tests, as import paths
	// or ./... for every package, and Left is each test run Level leaves to
	// a broader level, with why.
	Vet, Tests []string
	Left       []Deferred
}

// listFields are the fields of a package the plan reads. Asking go list for
// these alone spares it loading every package's dependencies, which took it
// 10 to 65 seconds for this repository on a loaded machine.
const listFields = "ImportPath,Dir,Imports,TestImports,XTestImports,EmbedPatterns,TestEmbedPatterns,XTestEmbedPatterns"

// Read works out the plan for the branch checked out in dir: the files that
// differ from where it left the default branch (origin/HEAD, else
// origin/main), committed, uncommitted or untracked, with a rename counted at
// both its paths, and the packages go list reports for the module, each with
// the embed patterns of its code, its tests and its external tests, so a
// deleted embedded file still names its package, and the module path go list
// -m reports, so a deleted package's importers are found by its import path.
// The root and every package directory are spelled with long names, as git
// and go list can spell one directory differently. The plan runs asked, or
// the level the change requires when asked is empty, as build decides it.
func Read(ctx context.Context, runner execx.Runner, dir string, asked Level) (Plan, error) {
	found, err := gather(ctx, runner, dir)
	if err != nil {
		return Plan{}, err
	}
	return build(found, asked), nil
}

// findings are what git and go say about the branch checked out in a
// directory.
type findings struct {
	// base is where the branch left the default branch, commit the commit
	// checked out and root the repository's top directory.
	base, commit, root string
	// changed are the files that differ from base, committed or not, and
	// uncommitted how many differ from commit.
	changed     []string
	uncommitted int
	packages    []Package
	module      string
	toolchain   string
	// policy is the policy file as base has it, when hasPolicy.
	policy    string
	hasPolicy bool
}

func gather(ctx context.Context, runner execx.Runner, dir string) (findings, error) {
	base, err := mergeBase(ctx, runner, dir)
	if err != nil {
		return findings{}, err
	}
	// The top directory can hold spaces, so it is read as a line of its own.
	where, err := output(ctx, runner, dir, "git", "rev-parse", "--show-toplevel", "HEAD")
	if err != nil {
		return findings{}, err
	}
	top, commit, _ := strings.Cut(strings.TrimSpace(where), "\n")
	diff, err := output(ctx, runner, dir, "git", "diff", "--name-only", "-z", "--no-renames", base)
	if err != nil {
		return findings{}, err
	}
	uncommitted, err := output(ctx, runner, dir, "git", "diff", "--name-only", "-z", "--no-renames", "HEAD")
	if err != nil {
		return findings{}, err
	}
	untracked, err := output(ctx, runner, dir, "git", "ls-files", "--others", "--exclude-standard", "--full-name", "-z")
	if err != nil {
		return findings{}, err
	}
	listed, err := output(ctx, runner, dir, "go", "list", "-e", "-json="+listFields, "./...")
	if err != nil {
		return findings{}, err
	}
	packages, err := decodePackages(strings.NewReader(listed))
	if err != nil {
		return findings{}, err
	}
	module, err := output(ctx, runner, dir, "go", "list", "-m", "-f", "{{.Path}}")
	if err != nil {
		return findings{}, err
	}
	toolchain, err := output(ctx, runner, dir, "go", "env", "GOVERSION", "GOOS", "GOARCH")
	if err != nil {
		return findings{}, err
	}
	policy, hasPolicy, err := policyAt(ctx, runner, dir, base)
	if err != nil {
		return findings{}, err
	}
	found := findings{
		base:        base,
		commit:      strings.TrimSpace(commit),
		root:        fsx.LongPath(filepath.FromSlash(strings.TrimSpace(top))),
		changed:     names(diff + "\x00" + untracked),
		uncommitted: len(names(uncommitted + "\x00" + untracked)),
		packages:    packages,
		module:      strings.TrimSpace(module),
		policy:      policy,
		hasPolicy:   hasPolicy,
	}
	if version := strings.Fields(toolchain); len(version) == 3 {
		found.toolchain = version[0] + " " + version[1] + "/" + version[2]
	}
	return found, nil
}

// build makes the plan from what was found. The change requires the affected
// level, or the full one when a module file changed, the policy cannot be
// read, or a changed file is one the policy does not account for: what the
// build cannot be sure of widens the run. The plan runs asked, or the required
// level when asked is empty. The policy is the default branch's, as the commit
// at base has it, so a branch cannot loosen the policy it is planned by.
func build(found findings, asked Level) Plan {
	plan := Plan{
		Root:        found.root,
		Base:        found.base,
		Commit:      found.commit,
		Uncommitted: found.uncommitted,
		Module:      found.module,
		Toolchain:   found.toolchain,
		Policy:      fmt.Sprintf("built-in defaults, as %.8s has no %s", found.base, PolicyPath),
		Required:    Affected,
		Why:         "the default for a change",
	}
	var policy Policy
	var slow map[string]bool
	if found.hasPolicy {
		parsed, err := ParsePolicy([]byte(found.policy))
		if err != nil {
			plan.Policy = fmt.Sprintf("built-in defaults, as %s at %.8s cannot be read", PolicyPath, found.base)
			plan.Required, plan.Why = Full, strings.TrimPrefix(err.Error(), "gatetest: ")
		} else {
			policy = parsed
			plan.Policy = fmt.Sprintf("%s version %d at %.8s", PolicyPath, policy.Version, found.base)
			slow = slowPaths(found.root, found.packages, policy)
		}
	}
	reach := Classify(found.root, found.module, found.changed, found.packages, policy)
	plan.Choices, plan.Everything, plan.Outside, plan.Unknown = reach.Choices, reach.Everything, reach.Outside, reach.Unknown
	if len(plan.Unknown) > 0 {
		subject := plan.Unknown[0] + " is"
		if len(plan.Unknown) > 1 {
			subject = fmt.Sprintf("%s and %d more files are", plan.Unknown[0], len(plan.Unknown)-1)
		}
		plan.Required, plan.Why = Full, subject+" in no package, under no contract and not listed as outside the Go checks"
	}
	if plan.Everything {
		plan.Required, plan.Why = Full, "go.mod or go.sum changed"
	}
	plan.Level = asked
	if asked == "" {
		plan.Level = plan.Required
	}
	plan.Vet, plan.Tests, plan.Left = scope(plan.Level, plan.Choices, plan.Everything, slow)
	return plan
}

// names splits git's NUL-separated file names.
func names(list string) []string {
	var files []string
	for _, file := range strings.Split(list, "\x00") {
		if file != "" {
			files = append(files, file)
		}
	}
	return files
}

// policyAt reads the policy file as the commit at base has it, and reports
// whether that commit has one.
func policyAt(ctx context.Context, runner execx.Runner, dir, base string) (text string, found bool, err error) {
	listed, err := output(ctx, runner, dir, "git", "ls-tree", "--full-tree", "--name-only", base, "--", PolicyPath)
	if err != nil || strings.TrimSpace(listed) == "" {
		return "", false, err
	}
	text, err = output(ctx, runner, dir, "git", "show", base+":"+PolicyPath)
	return text, err == nil, err
}

// slowPaths are the import paths of the packages whose directory, from root,
// the policy lists as slow.
func slowPaths(root string, packages []Package, policy Policy) map[string]bool {
	slow := map[string]bool{}
	for _, p := range packages {
		dir, err := filepath.Rel(root, p.Dir)
		if err != nil {
			continue
		}
		dir = filepath.ToSlash(dir)
		if slices.ContainsFunc(policy.SlowPackages, func(listed string) bool { return strings.EqualFold(listed, dir) }) {
			slow[p.ImportPath] = true
		}
	}
	return slow
}

// mergeBase finds where the branch left the default branch.
func mergeBase(ctx context.Context, runner execx.Runner, dir string) (string, error) {
	for _, ref := range []string{"origin/HEAD", "origin/main"} {
		if base, err := output(ctx, runner, dir, "git", "merge-base", "HEAD", ref); err == nil {
			return strings.TrimSpace(base), nil
		}
	}
	return "", errors.New("gatetest: no merge base with origin/HEAD or origin/main, so what the branch changed is unknown")
}

// decodePackages reads the stream of objects go list -json writes.
func decodePackages(r io.Reader) ([]Package, error) {
	var packages []Package
	decoder := json.NewDecoder(r)
	for {
		var listed struct {
			ImportPath                         string
			Dir                                string
			Imports, TestImports, XTestImports []string
			EmbedPatterns, TestEmbedPatterns   []string
			XTestEmbedPatterns                 []string
		}
		if err := decoder.Decode(&listed); errors.Is(err, io.EOF) {
			return packages, nil
		} else if err != nil {
			return nil, fmt.Errorf("gatetest: read go list: %w", err)
		}
		imports := append(append(listed.Imports, listed.TestImports...), listed.XTestImports...)
		embedPatterns := append(append(listed.EmbedPatterns, listed.TestEmbedPatterns...), listed.XTestEmbedPatterns...)
		packages = append(packages, Package{ImportPath: listed.ImportPath, Dir: fsx.LongPath(listed.Dir), Imports: imports, EmbedPatterns: embedPatterns})
	}
}

// output runs one command in dir and returns its standard output, or an
// error carrying what it wrote to standard error.
func output(ctx context.Context, runner execx.Runner, dir, name string, args ...string) (string, error) {
	result, err := runner.Run(ctx, execx.Request{Dir: dir, Name: name, Args: args})
	if err != nil {
		return "", fmt.Errorf("gatetest: %s %s: %w", name, strings.Join(args, " "), err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("gatetest: %s %s exited %d: %s", name, strings.Join(args, " "), result.ExitCode, bytes.TrimSpace(result.Stderr))
	}
	return string(result.Stdout), nil
}
