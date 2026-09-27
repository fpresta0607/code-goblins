// Package gatetest chooses what a no-mistakes gate's local test step runs in
// this repository: the Go packages a branch changed and the packages that
// import them directly. CI still runs every package; this bounds the local
// step so that it fits the gate on a loaded machine.
package gatetest

import (
	"path/filepath"
	"slices"
	"strings"
)

// Package is what go list reports about one package. Imports holds the
// packages it imports from its code and from its tests.
type Package struct {
	ImportPath string
	Dir        string
	Imports    []string
}

// Choice is one package the step tests and why: Imports names the changed
// package it imports, and is empty when the package changed itself.
type Choice struct {
	ImportPath string
	Imports    string
}

func (c Choice) String() string {
	if c.Imports == "" {
		return c.ImportPath + " (changed)"
	}
	return c.ImportPath + " (imports " + c.Imports + ")"
}

// Select returns the packages a change to files, given relative to root, asks
// to test: every package with a changed file in its directory or below it
// short of another package (so testdata and embedded files count), then every
// package that imports one of those directly, from its code or its tests. The
// changed packages come first, each group in import-path order. everything
// reports that go.mod or go.sum changed, which can change what every package
// builds against, so no package list bounds it.
func Select(root string, files []string, packages []Package) (choices []Choice, everything bool) {
	byDir := make(map[string]Package, len(packages))
	for _, p := range packages {
		byDir[strings.ToLower(filepath.Clean(p.Dir))] = p
	}
	changed := map[string]bool{}
	for _, file := range files {
		if base := filepath.Base(file); filepath.Dir(filepath.FromSlash(file)) == "." && (base == "go.mod" || base == "go.sum") {
			return nil, true
		}
		for dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(file))); ; dir = filepath.Dir(dir) {
			if p, ok := byDir[strings.ToLower(dir)]; ok {
				changed[p.ImportPath] = true
				break
			}
			if len(dir) <= len(root) || dir == filepath.Dir(dir) {
				break
			}
		}
	}
	changedPaths := make([]string, 0, len(changed))
	for path := range changed {
		changedPaths = append(changedPaths, path)
	}
	slices.Sort(changedPaths)
	for _, path := range changedPaths {
		choices = append(choices, Choice{ImportPath: path})
	}

	var importers []Choice
	for _, p := range packages {
		if changed[p.ImportPath] {
			continue
		}
		for _, path := range changedPaths {
			if slices.Contains(p.Imports, path) {
				importers = append(importers, Choice{ImportPath: p.ImportPath, Imports: path})
				break
			}
		}
	}
	slices.SortFunc(importers, func(a, b Choice) int { return strings.Compare(a.ImportPath, b.ImportPath) })
	return append(choices, importers...), false
}
