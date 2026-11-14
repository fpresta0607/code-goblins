package gatetest

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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

// ReadFocused feeds repair paths through the same planner as Read. A repair
// it cannot narrow runs the configured level over its repaired paths, never
// the full level, and says what it leaves to CI.
func ReadFocused(ctx context.Context, runner execx.Runner, dir, filename string, configured Level) (Plan, string, error) {
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
	return focused(ctx, runner, found, string(data), configured)
}

// focused plans a repair round's check: the repair's own reach and its failed
// tests by name. A repair it cannot narrow so, for a failed test it cannot
// map, a repair path it cannot classify, a repair that names no failed test or
// no path, or one that requires the full level, runs the configured level over
// the repaired paths instead of the full level, which once held the machine's
// heavy line for hours (#2176). Its one line says why, and what it leaves to
// CI, which runs every package, a failed test it could not map by name.
func focused(ctx context.Context, runner execx.Runner, found findings, text string, configured Level) (Plan, string, error) {
	if !utf8.ValidString(text) {
		return Plan{}, "", errors.New("gatetest: focus file is not UTF-8")
	}
	var failures, mapped []FailedTest
	var unmapped, paths []string
	reason := ""
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "test "):
			record := strings.TrimPrefix(line, "test ")
			match := failedTestLine.FindStringSubmatch(record)
			if match == nil {
				reason = "an unrecognized or unmappable failed test"
				unmapped = append(unmapped, strconv.Quote(record))
				continue
			}
			failure := FailedTest{Package: match[1], Name: match[2]}
			if slices.Contains(failures, failure) {
				continue
			}
			failures = append(failures, failure)
			if !slices.ContainsFunc(found.packages, func(p Package) bool { return p.ImportPath == failure.Package }) {
				reason = "an unrecognized or unmappable failed test"
				unmapped = append(unmapped, failure.Package+" "+failure.Name)
				continue
			}
			mapped = append(mapped, failure)
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
	over := "the repaired paths"
	switch {
	case len(paths) == 0:
		over, paths = "the branch's change", found.changed
		if reason == "" {
			reason = "the repair named no repaired path"
		}
	case len(failures) == 0 && reason == "":
		reason = "the repair named no failed test"
	}
	policy, _, _ := policyOf(found)
	for _, file := range paths {
		reach := Classify(found.root, found.module, []string{file}, found.packages, policy)
		if reason == "" && !reach.Everything && len(reach.Choices) == 0 && len(reach.Outside) == 0 {
			reason = "a repair path has no mapped check or outside classification"
		}
	}
	found.changed = paths
	plan := build(found, "")
	if reason == "" && plan.Required == Full {
		reason = plan.Why
	}
	note := ""
	if reason != "" {
		if configured == Changed {
			edited, err := editedTests(ctx, runner, found)
			if err != nil {
				return Plan{}, "", err
			}
			found.edited = edited
		}
		plan = build(found, configured)
		note = fmt.Sprintf("focused check runs the configured level %s over %s, not the full level, since %s; left to CI, which runs every package: the %s level this repair requires (%s)", configured, over, reason, plan.Required, plan.Why)
		if len(unmapped) > 0 {
			note += ", and the failed tests it could not map: " + strings.Join(unmapped, ", ")
		}
	}
	plan.Failures = failures
	byPackage := map[string][]string{}
	for _, failure := range mapped {
		if slices.Contains(plan.Tests, failure.Package) || slices.Contains(plan.Alone, failure.Package) || slices.Contains(found.edited[failure.Package], failure.Name) {
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
	return plan, note, nil
}
