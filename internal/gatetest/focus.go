package gatetest

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

type FailedTest struct {
	Package string
	Name    string
}

var failedTestLine = regexp.MustCompile(`^(\S+)\s+--- FAIL: (Test[\pL\pN_]+)(?:/\S+)?(?: \([0-9.]+s\))?$`)

// ReadFocused feeds repair paths through the same planner as Read. A broader
// fallback uses that planner at full level and says why it could not narrow.
func ReadFocused(ctx context.Context, runner execx.Runner, dir, filename string) (Plan, string, error) {
	if !filepath.IsAbs(filename) {
		return Plan{}, "", errors.New("gatetest: NO_MISTAKES_TEST_FOCUS must name an absolute UTF-8 file")
	}
	data, err := fsx.ReadFileContext(ctx, filename)
	if err != nil {
		return Plan{}, "", fmt.Errorf("gatetest: read focus file: %w", err)
	}
	base, err := mergeBase(ctx, runner, dir)
	if err != nil {
		return Plan{}, "", err
	}
	found, err := gather(ctx, runner, dir, base)
	if err != nil {
		return Plan{}, "", err
	}
	return focused(found, string(data))
}

func focused(found findings, text string) (Plan, string, error) {
	if !utf8.ValidString(text) {
		return Plan{}, "", errors.New("gatetest: focus file is not UTF-8")
	}
	var failures []FailedTest
	var paths []string
	reason := ""
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "test "):
			match := failedTestLine.FindStringSubmatch(strings.TrimPrefix(line, "test "))
			if match == nil {
				reason = "an unrecognized or unmappable failed test"
				continue
			}
			failure := FailedTest{Package: match[1], Name: match[2]}
			if !slices.Contains(failures, failure) {
				failures = append(failures, failure)
			}
			if !slices.ContainsFunc(found.packages, func(p Package) bool { return p.ImportPath == failure.Package }) {
				reason = "an unrecognized or unmappable failed test"
			}
		case strings.HasPrefix(line, "path "):
			file := strings.TrimPrefix(line, "path ")
			if file == "" || file == "." || file == ".." || path.IsAbs(file) || path.Clean(file) != file || strings.HasPrefix(file, "../") || strings.ContainsAny(file, "\\:") || strings.ContainsFunc(file, unicode.IsControl) {
				return Plan{}, "", fmt.Errorf("gatetest: unsafe or escaped focus path %q", file)
			}
			paths = append(paths, file)
		default:
			reason = "an unrecognized focus record"
		}
	}
	if len(failures) == 0 || len(paths) == 0 {
		reason = "focus lacks failed tests or repair paths"
	}
	original := found
	found.changed = paths
	plan := build(found, "")
	policy, _, _ := policyOf(found)
	for _, file := range paths {
		reach := Classify(found.root, found.module, []string{file}, found.packages, policy)
		if !reach.Everything && len(reach.Choices) == 0 && len(reach.Outside) == 0 {
			reason = "a repair path has no mapped check or outside classification"
		}
	}
	if reason == "" && plan.Required == Full {
		reason = plan.Why
	}
	if reason != "" {
		original.changed = append(slices.Clone(original.changed), paths...)
		broader := build(original, Full)
		broader.Failures = failures
		return broader, "focused check falls back to full: " + reason, nil
	}
	plan.Failures = failures
	byPackage := map[string][]string{}
	for _, failure := range failures {
		if slices.Contains(plan.Tests, failure.Package) || slices.Contains(plan.Alone, failure.Package) {
			continue
		}
		byPackage[failure.Package] = append(byPackage[failure.Package], regexp.QuoteMeta(failure.Name))
	}
	var packages []string
	for importPath := range byPackage {
		packages = append(packages, importPath)
	}
	slices.Sort(packages)
	alone := pathsIn(found.root, found.packages, policy.AlonePackages)
	for _, importPath := range packages {
		names := byPackage[importPath]
		slices.Sort(names)
		command := slices.Clone(StepTestArgs)
		if alone[importPath] {
			command[5] = "1"
		}
		plan.Named = append(plan.Named, Step{What: "named failed tests of " + importPath, Command: append(command, "-run", "^("+strings.Join(names, "|")+")$", importPath)})
	}
	return plan, "", nil
}
