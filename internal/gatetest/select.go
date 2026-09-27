// Package gatetest chooses what a no-mistakes gate's local test step runs in
// this repository: the Go packages a branch changed and the packages that
// import them directly. CI still runs every package; this bounds the local
// step so that it fits the gate on a loaded machine.
package gatetest

import (
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

// buildInputs are the extensions of the files go builds a package from.
var buildInputs = []string{".go", ".s", ".c", ".h", ".syso"}

// Select returns the packages a change to files, given relative to root, asks
// to test: every package that owns a changed file, then every package that
// imports one of those directly, from its code or its tests. A package owns a
// build input directly in its directory (.go, .s, .c, .h or .syso), a file
// under its testdata directory, and a file one of its embed patterns covers,
// present or deleted. A file no package owns this way, such as README.md, a
// doc nothing embeds or one in a deleted package, chooses nothing. The
// changed packages come first, each group in import-path order. everything
// reports that go.mod or go.sum changed, which can change what every package
// builds against, so no package list bounds it.
func Select(root string, files []string, packages []Package) (choices []Choice, everything bool) {
	changed := map[string]bool{}
	for _, file := range files {
		file = strings.ToLower(filepath.ToSlash(file))
		if file == "go.mod" || file == "go.sum" {
			return nil, true
		}
		full := strings.ToLower(filepath.ToSlash(filepath.Join(root, file)))
		for _, p := range packages {
			if rel, found := strings.CutPrefix(full, strings.ToLower(filepath.ToSlash(filepath.Clean(p.Dir)))+"/"); found && owns(rel, p.EmbedPatterns) {
				changed[p.ImportPath] = true
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
