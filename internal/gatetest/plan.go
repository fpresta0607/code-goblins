package gatetest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Plan is what the step tests for the branch checked out in a directory.
type Plan struct {
	// Base is where the branch left the default branch.
	Base string
	// Choices are the packages to test, with why.
	Choices []Choice
	// Everything says a module file changed, so every package is tested.
	Everything bool
}

// Read works out the plan for the branch checked out in dir: the files that
// differ from where it left the default branch (origin/HEAD, else
// origin/main), committed, uncommitted or untracked, with a rename counted at
// both its paths, and the packages go list reports for the module, each with
// the embed patterns of its code, its tests and its external tests, so a
// deleted embedded file still names its package. The root and every package
// directory are spelled with long names, as git and go list can spell one
// directory differently.
func Read(ctx context.Context, runner execx.Runner, dir string) (Plan, error) {
	base, err := mergeBase(ctx, runner, dir)
	if err != nil {
		return Plan{}, err
	}
	top, err := output(ctx, runner, dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return Plan{}, err
	}
	diff, err := output(ctx, runner, dir, "git", "diff", "--name-only", "-z", "--no-renames", base)
	if err != nil {
		return Plan{}, err
	}
	untracked, err := output(ctx, runner, dir, "git", "ls-files", "--others", "--exclude-standard", "--full-name", "-z")
	if err != nil {
		return Plan{}, err
	}
	listed, err := output(ctx, runner, dir, "go", "list", "-e", "-json", "./...")
	if err != nil {
		return Plan{}, err
	}
	packages, err := decodePackages(strings.NewReader(listed))
	if err != nil {
		return Plan{}, err
	}
	var files []string
	for _, file := range strings.Split(diff+"\x00"+untracked, "\x00") {
		if file != "" {
			files = append(files, file)
		}
	}
	choices, everything := Select(fsx.LongPath(filepath.FromSlash(strings.TrimSpace(top))), files, packages)
	return Plan{Base: base, Choices: choices, Everything: everything}, nil
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
