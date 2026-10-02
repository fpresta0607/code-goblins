package fsx

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// rawOpens are the standard library's ways to open a file for reading or
// appending, by import path and name, each with the function of this package
// a fleet program uses instead.
var rawOpens = map[string]string{
	"os.ReadFile":        "ReadFile",
	"io/ioutil.ReadFile": "ReadFile",
	"os.Open":            "Open",
	"os.O_RDONLY":        "Open",
	"os.O_APPEND":        "OpenAppend",
}

// Every file a fleet program reads or appends to is opened through this
// package, which shares it for deletion and waits out another process's
// brief hold on it, so a new call site cannot forget to. On 2026-10-01 the
// two pull requests merged after every read had moved here added six with
// os.ReadFile and os.Open, and nothing failed. A raw open named outside this
// package, called or not, is reported with where it is. Test files and
// tests/, a test fixture's own program, are not fleet programs.
func TestEveryFleetReadGoesThroughFsx(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	var violations []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".worktrees", "node_modules", "testdata", "frontend":
				return filepath.SkipDir
			}
			if relative == "tests" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files++
		if filepath.ToSlash(filepath.Dir(relative)) == "internal/fsx" {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found, err := opensAround(relative, source)
		if err != nil {
			return err
		}
		violations = append(violations, found...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A guard that stops reading files approves everything, so it counts
	// what it read: the fleet has well over a hundred files.
	if files < 100 {
		t.Fatalf("the guard read %d Go files under %s; it is not reading the repository", files, root)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

func TestOpensAroundReportsEveryRawOpen(t *testing.T) {
	tests := []struct {
		name       string
		imports    string
		body       string
		isReported bool
	}{
		{name: "os.ReadFile call", imports: `"os"`, body: `os.ReadFile("state.json")`, isReported: true},
		{name: "os.Open call", imports: `"os"`, body: `os.Open("state.json")`, isReported: true},
		{name: "os.ReadFile as a value", imports: `"os"`, body: `read := os.ReadFile; read("state.json")`, isReported: true},
		{name: "renamed os import", imports: `host "os"`, body: `host.ReadFile("state.json")`, isReported: true},
		{name: "ioutil.ReadFile call", imports: `"io/ioutil"`, body: `ioutil.ReadFile("state.json")`, isReported: true},
		{name: "os.OpenFile to read", imports: `"os"`, body: `os.OpenFile("state.json", os.O_RDONLY, 0)`, isReported: true},
		{name: "os.OpenFile to append", imports: `"os"`, body: `os.OpenFile("log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)`, isReported: true},
		{name: "fsx.ReadFile", imports: `"github.com/fpresta0607/code-goblins/internal/fsx"`, body: `fsx.ReadFile("state.json")`},
		{name: "fsx.Open", imports: `"github.com/fpresta0607/code-goblins/internal/fsx"`, body: `fsx.Open("state.json")`},
		{name: "fsx.OpenAppend", imports: `"github.com/fpresta0607/code-goblins/internal/fsx"`, body: `fsx.OpenAppend("log", 0o644)`},
		{name: "os.OpenFile to create", imports: `"os"`, body: `os.OpenFile("brief.md", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)`},
		{name: "a method named Open", imports: `"os"`, body: `root, _ := os.OpenRoot("."); root.Open("state.json")`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			source := "package example\n\nimport (" + test.imports + ")\n\nfunc example() {\n" + test.body + "\n}\n"
			want := 0
			if test.isReported {
				want = 1
			}

			// Act
			violations, err := opensAround("internal/example/example.go", []byte(source))

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(violations) != want {
				t.Errorf("violations = %q, want %d", violations, want)
			}
		})
	}
}

// opensAround parses a fleet file's source and reports, as path:line: what,
// each place it names a raw open instead of this package's.
func opensAround(relative string, source []byte) ([]string, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, relative, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	imported := map[string]string{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imported[name] = path
	}
	var violations []string
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		if instead, ok := rawOpens[imported[pkg.Name]+"."+selector.Sel.Name]; ok {
			violations = append(violations, fmt.Sprintf("%s:%d: opens a file with %s.%s instead of fsx.%s", relative, fileSet.Position(selector.Pos()).Line, pkg.Name, selector.Sel.Name, instead))
		}
		return true
	})
	return violations, nil
}
