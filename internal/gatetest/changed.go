package gatetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
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

// changedScope is what the changed level runs: go vet over what the required
// level vets, the tests edited names, by name, package by package, one
// at a time for a package the policy runs alone, and each test run of the
// required level it leaves to CI, which runs every test before the merge.
func changedScope(required Level, choices []Choice, everything bool, edited map[string][]string, alone map[string]bool) (vet []string, named []Step, left []Deferred) {
	vet, tests, _ := scope(required, choices, everything, nil)
	for _, importPath := range slices.Sorted(maps.Keys(edited)) {
		names := edited[importPath]
		quoted := make([]string, len(names))
		for index, name := range names {
			quoted[index] = regexp.QuoteMeta(name)
		}
		command := slices.Clone(StepTestArgs)
		if alone[importPath] {
			command[5] = "1"
		}
		named = append(named, Step{What: "tests of " + importPath, Why: "added or edited: " + strings.Join(names, ", "), Detail: names, Command: append(command, "-run", "^("+strings.Join(quoted, "|")+")$", importPath)})
	}
	for _, importPath := range tests {
		check := "tests of " + importPath
		switch {
		case importPath == "./...":
			check = "tests of every package"
		case len(edited[importPath]) > 0:
			check = "the other tests of " + importPath
		}
		left = append(left, Deferred{Check: check, Why: "CI runs them before the merge"})
	}
	return vet, named, left
}

// editedTests are the tests the change added or edited, by the import path of
// their package: each top-level test function of a changed _test.go file
// whose text, from func to its closing brace, differs from the one of that
// name in the file as base has it, or that base lacks. A test whose comment
// alone changed is not edited, a test file the branch deleted names none, and
// a base copy that does not parse counts each of its file's tests as edited.
func editedTests(ctx context.Context, runner execx.Runner, found findings) (map[string][]string, error) {
	byDir := make(map[string]string, len(found.packages))
	for _, p := range found.packages {
		if dir, err := filepath.Rel(found.root, p.Dir); err == nil {
			byDir[strings.ToLower(filepath.ToSlash(dir))] = p.ImportPath
		}
	}
	edited := map[string][]string{}
	for _, name := range found.changed {
		file := filepath.ToSlash(name)
		importPath, isPackage := byDir[strings.ToLower(path.Dir(file))]
		if !strings.HasSuffix(file, "_test.go") || !isPackage {
			continue
		}
		source, err := fsx.ReadFileContext(ctx, filepath.Join(found.root, filepath.FromSlash(file)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("gatetest: read %s: %w", file, err)
		}
		now, err := testFunctions(file, source)
		if err != nil {
			return nil, err
		}
		text, isAtBase, err := fileAt(ctx, runner, found.root, found.base, file)
		if err != nil {
			return nil, err
		}
		before := map[string]string{}
		if isAtBase {
			if parsed, err := testFunctions(file, []byte(text)); err == nil {
				before = parsed
			}
		}
		for test, body := range now {
			if before[test] != body {
				edited[importPath] = append(edited[importPath], test)
			}
		}
	}
	for importPath, names := range edited {
		slices.Sort(names)
		edited[importPath] = slices.Compact(names)
	}
	return edited, nil
}

// testFunctions are the top-level test functions of a Go test file, by name,
// each with its text from func to its closing brace, with Windows line ends
// read as Unix ones, as git can check a file out with either.
func testFunctions(name string, source []byte) (map[string]string, error) {
	source = bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n"))
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, name, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("gatetest: parse %s: %w", name, err)
	}
	functions := map[string]string{}
	for _, declaration := range parsed.Decls {
		function, isFunction := declaration.(*ast.FuncDecl)
		if !isFunction || function.Recv != nil || !isTestName(function.Name.Name) {
			continue
		}
		functions[function.Name.Name] = string(source[files.Position(function.Pos()).Offset:files.Position(function.End()).Offset])
	}
	return functions, nil
}

// isTestName says go test runs a function of this name as a test: Test, then
// nothing or a character that is not a lower-case letter. TestMain runs a
// package's tests rather than being one of them.
func isTestName(name string) bool {
	rest, isTest := strings.CutPrefix(name, "Test")
	if !isTest || name == "TestMain" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(rest)
	return rest == "" || !unicode.IsLower(first)
}
