// Package gatetest chooses what a no-mistakes gate's local test step runs in
// this repository: the Go packages a branch changed, the packages that import
// them directly or transitively, and the packages the repository's policy
// names for a changed file their tests read. CI still runs every package;
// this bounds the local step so that it fits the gate on a loaded machine.
package gatetest

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Package is what go list reports about one package. Imports holds the
// packages it imports from its code and from its tests, and EmbedPatterns
// the //go:embed patterns they use, relative to Dir.
type Package struct {
	ImportPath    string
	Dir           string
	Imports       []string
	EmbedPatterns []string
}

// Choice is one package the step tests and why. Imports names the selected
// package it imports directly, the first step on a shortest path to a changed
// one. Reads names a changed file its tests read under a contract of the
// policy, More how many further such files changed, and Why the contract's
// reason. All are empty when the package changed itself.
type Choice struct {
	ImportPath string
	Imports    string
	Reads      string
	More       int
	Why        string
}

// Reason says why the package is tested: it changed, it imports a changed
// package, or its tests read a changed file, given with the contract's
// reason.
func (c Choice) Reason() string {
	switch {
	case c.Imports != "":
		return "imports " + c.Imports
	case c.Reads != "":
		return c.Why + ": " + andMore([]string{c.Reads}, c.More)
	}
	return "changed"
}

func (c Choice) String() string {
	return c.ImportPath + " (" + c.Reason() + ")"
}

// andMore names the files, and counts those left out.
func andMore(files []string, more int) string {
	named := strings.Join(files, ", ")
	if more == 0 {
		return named
	}
	return fmt.Sprintf("%s and %d more", named, more)
}

// buildInputs are the extensions of the files go builds a package from.
var buildInputs = []string{".go", ".s", ".c", ".h", ".syso"}

// Select returns the packages a change to files, given relative to root, the
// directory of module, asks to test: every package that owns a changed file,
// then every package that imports one of those directly or transitively, from
// its code or tests. A package owns a build input directly in its directory (.go, .s, .c,
// .h or .syso), a file under its testdata directory, and a file one of its
// embed patterns covers, present or deleted. A file no package owns this way,
// such as README.md or a doc nothing embeds, chooses nothing. A changed build
// input in a directory with no package names a deleted or moved package, the
// module path joined with that directory: it is not tested, but every package
// that still imports it directly or transitively is. Reasons name the first
// immediate import on a shortest path, with changed seeds before deleted seeds
// and subsequent frontiers sorted. The changed packages come first, each group
// in import-path order. everything reports that go.mod or go.sum
// changed, which can change what every package builds against, so no package
// list bounds it.
func Select(root, module string, files []string, packages []Package) (choices []Choice, everything bool) {
	owned := ownership(root, module, files, packages)
	if owned.everything {
		return nil, true
	}
	return owned.choices(packages, nil), false
}

// Reach is what a change reaches under a policy that classifies files: the
// packages to test, each with why, the checks besides Go's it selects, with
// the changed files that select each, the changed files the policy puts
// outside the Go checks, by its reason, and the changed files nothing
// accounts for.
type Reach struct {
	Choices    []Choice
	Everything bool
	Checks     []Selected
	Outside    []Set
	Unknown    []string
}

// Selected is a check besides Go's that a change selects, and the changed
// files that select it.
type Selected struct {
	Check Check
	Files []string
}

// Set is the changed files one rule of the policy puts outside the Go checks.
type Set struct {
	Why   string
	Files []string
}

func (s Set) String() string {
	if len(s.Files) <= 2 {
		return andMore(s.Files, 0) + " (" + s.Why + ")"
	}
	return andMore(s.Files[:2], len(s.Files)-2) + " (" + s.Why + ")"
}

// Classify returns what a change to files reaches under policy. A file a
// package owns, as Select judges it, is that package's whatever the policy
// says of its folder. A file one of the policy's contracts names also selects
// the contract's packages, each listed with the contract's reason and the
// file, unless the package changed itself; a contract selects its packages
// alone, not their importers. A file one of the policy's checks besides Go's
// names selects that check, in the policy's order. Of the remaining files,
// one the policy lists as outside the Go checks is reported under that rule's
// reason, and one nothing accounts for is unknown. A policy that classifies
// nothing leaves the selection as Select makes it, with nothing outside and
// nothing unknown.
func Classify(root, module string, files []string, packages []Package, policy Policy) Reach {
	owned := ownership(root, module, files, packages)
	if owned.everything {
		return Reach{Everything: true}
	}
	if !policy.Classifies() {
		return Reach{Choices: owned.choices(packages, nil)}
	}
	byDir := make(map[string]string, len(packages))
	for _, p := range packages {
		if dir, err := filepath.Rel(root, p.Dir); err == nil {
			byDir[strings.ToLower(filepath.ToSlash(dir))] = p.ImportPath
		}
	}
	var reach Reach
	read := map[string]*Choice{}
	outside := make([]Set, len(policy.Outside))
	checked := make([]Selected, len(policy.Checks))
	for _, name := range files {
		file := filepath.ToSlash(name)
		contracted := false
		for _, contract := range policy.Contracts {
			if !slices.ContainsFunc(contract.Paths, func(pattern string) bool { return matches(pattern, file) }) {
				continue
			}
			contracted = true
			for _, dir := range contract.Packages {
				importPath, found := byDir[strings.ToLower(dir)]
				if !found {
					continue
				}
				if choice := read[importPath]; choice != nil {
					choice.More++
				} else {
					read[importPath] = &Choice{ImportPath: importPath, Reads: file, Why: contract.Why}
				}
			}
		}
		isChecked := false
		for i, check := range policy.Checks {
			if slices.ContainsFunc(check.Paths, func(pattern string) bool { return matches(pattern, file) }) {
				isChecked = true
				checked[i].Check = check
				checked[i].Files = append(checked[i].Files, file)
			}
		}
		if owned.files[name] || contracted || isChecked {
			continue
		}
		rule := slices.IndexFunc(policy.Outside, func(rule Outside) bool {
			return slices.ContainsFunc(rule.Paths, func(pattern string) bool { return matches(pattern, file) })
		})
		if rule < 0 {
			reach.Unknown = append(reach.Unknown, file)
			continue
		}
		outside[rule].Why = policy.Outside[rule].Why
		outside[rule].Files = append(outside[rule].Files, file)
	}
	for _, set := range outside {
		if len(set.Files) > 0 {
			reach.Outside = append(reach.Outside, set)
		}
	}
	for _, selected := range checked {
		if len(selected.Files) > 0 {
			reach.Checks = append(reach.Checks, selected)
		}
	}
	slices.Sort(reach.Unknown)
	var readers []Choice
	for _, choice := range read {
		readers = append(readers, *choice)
	}
	reach.Choices = owned.choices(packages, readers)
	return reach
}

// owned is what the module's packages make of a change.
type owned struct {
	// changed are the packages that own a changed file and gone the packages
	// a changed build input names that are no longer there, by import path.
	changed, gone map[string]bool
	// files are the changed files a package owns or a gone package owned.
	files map[string]bool
	// everything says go.mod or go.sum changed.
	everything bool
}

func ownership(root, module string, files []string, packages []Package) owned {
	dirs := make(map[string]bool, len(packages))
	for _, p := range packages {
		dirs[strings.ToLower(filepath.ToSlash(filepath.Clean(p.Dir)))] = true
	}
	found := owned{changed: map[string]bool{}, gone: map[string]bool{}, files: map[string]bool{}}
	for _, name := range files {
		file := strings.ToLower(filepath.ToSlash(name))
		if file == "go.mod" || file == "go.sum" {
			return owned{everything: true}
		}
		full := strings.ToLower(filepath.ToSlash(filepath.Join(root, file)))
		if slices.Contains(buildInputs, path.Ext(file)) && !dirs[path.Dir(full)] {
			found.gone[path.Join(module, path.Dir(filepath.ToSlash(name)))] = true
			found.files[name] = true
		}
		for _, p := range packages {
			if rel, inside := strings.CutPrefix(full, strings.ToLower(filepath.ToSlash(filepath.Clean(p.Dir)))+"/"); inside && owns(rel, p.EmbedPatterns) {
				found.changed[p.ImportPath] = true
				found.files[name] = true
			}
		}
	}
	return found
}

// choices lists the packages to test: those that changed, then readers, the
// packages a contract selects, then the packages that import a changed or a
// gone one directly or transitively, each group in import-path order and each
// package once, under the first of those reasons that holds for it. A reader
// is not a starting point, since its code did not change, but one the walk
// reaches through an import still leads on to its own importers.
func (o owned) choices(packages []Package, readers []Choice) []Choice {
	var choices []Choice
	changedPaths := make([]string, 0, len(o.changed))
	for path := range o.changed {
		changedPaths = append(changedPaths, path)
	}
	slices.Sort(changedPaths)
	for _, path := range changedPaths {
		choices = append(choices, Choice{ImportPath: path})
	}
	slices.SortFunc(readers, func(a, b Choice) int { return strings.Compare(a.ImportPath, b.ImportPath) })
	listed := map[string]bool{}
	for _, reader := range readers {
		if !o.changed[reader.ImportPath] {
			choices = append(choices, reader)
			listed[reader.ImportPath] = true
		}
	}
	gonePaths := make([]string, 0, len(o.gone))
	for path := range o.gone {
		gonePaths = append(gonePaths, path)
	}
	slices.Sort(gonePaths)
	imported := append(slices.Clip(changedPaths), gonePaths...)

	reverseImports := map[string][]string{}
	for _, p := range packages {
		for _, importedPath := range p.Imports {
			reverseImports[importedPath] = append(reverseImports[importedPath], p.ImportPath)
		}
	}
	selected := map[string]bool{}
	for _, importPath := range imported {
		selected[importPath] = true
	}
	var importers []Choice
	for len(imported) > 0 {
		var next []string
		for _, importedPath := range imported {
			for _, importPath := range reverseImports[importedPath] {
				if selected[importPath] {
					continue
				}
				selected[importPath] = true
				if !listed[importPath] {
					importers = append(importers, Choice{ImportPath: importPath, Imports: importedPath})
				}
				next = append(next, importPath)
			}
		}
		slices.Sort(next)
		imported = next
	}
	slices.SortFunc(importers, func(a, b Choice) int { return strings.Compare(a.ImportPath, b.ImportPath) })
	return append(choices, importers...)
}

// owns reports whether a package owns the file at rel, a lower-case slash
// path relative to the package's directory, given the package's embed
// patterns.
func owns(rel string, embedPatterns []string) bool {
	if !strings.Contains(rel, "/") && slices.Contains(buildInputs, path.Ext(rel)) || strings.HasPrefix(rel, "testdata/") {
		return true
	}
	for _, pattern := range embedPatterns {
		pattern = strings.ToLower(strings.TrimPrefix(pattern, "all:"))
		for name := rel; name != "."; name = path.Dir(name) {
			if matched, _ := path.Match(pattern, name); matched {
				return true
			}
		}
	}
	return false
}
