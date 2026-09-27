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
// packages it imports from its code and from its tests, and Embeds the files
// they embed, relative to Dir.
type Package struct {
	ImportPath string
	Dir        string
	Imports    []string
	Embeds     []string
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
// to test: every package with a changed file directly in its directory, under
// its testdata directory or among the files it embeds, then every package that
// imports one of those directly, from its code or its tests. A file no package
// owns this way, such as one in a deleted package, chooses nothing. The
// changed packages come first, each group in import-path order. everything
// reports that go.mod or go.sum changed, which can change what every package
// builds against, so no package list bounds it.
func Select(root string, files []string, packages []Package) (choices []Choice, everything bool) {
	byDir := make(map[string]string, len(packages))
	embedded := map[string][]string{}
	for _, p := range packages {
		dir := strings.ToLower(filepath.Clean(p.Dir))
		byDir[dir] = p.ImportPath
		for _, file := range p.Embeds {
			path := filepath.Join(dir, strings.ToLower(filepath.FromSlash(file)))
			embedded[path] = append(embedded[path], p.ImportPath)
		}
	}
	separator := string(filepath.Separator)
	changed := map[string]bool{}
	for _, file := range files {
		file = strings.ToLower(filepath.FromSlash(file))
		if file == "go.mod" || file == "go.sum" {
			return nil, true
		}
		path := filepath.Join(strings.ToLower(root), file)
		dir := filepath.Dir(path)
		if owner, _, found := strings.Cut(separator+file, separator+"testdata"+separator); found {
			dir = filepath.Join(strings.ToLower(root), owner)
		}
		if owner, ok := byDir[dir]; ok {
			changed[owner] = true
		}
		for _, owner := range embedded[path] {
			changed[owner] = true
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
