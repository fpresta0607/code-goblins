package execx

import (
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

// starters are the calls that start a process, by import path and name.
var starters = map[string][]string{
	"os/exec":                  {"Command", "CommandContext"},
	"os":                       {"StartProcess"},
	"syscall":                  {"StartProcess", "CreateProcess", "CreateProcessAsUser"},
	"golang.org/x/sys/windows": {"CreateProcess", "CreateProcessAsUser", "ShellExecute"},
}

// startsElsewhere are the process starts that do not go through this
// package, each with why it opens no window.
var startsElsewhere = map[string]string{
	"internal/conpty/conpty_windows.go windows.CreateProcess": "the process runs in a pseudo console, which has no window",
}

// Every process a fleet program starts goes through Command or
// CommandContext, which start it without a console window, so a new call
// site cannot forget to. A start elsewhere, an exec.Cmd built by hand, or a
// SysProcAttr or CreationFlags assigned outright, which would drop the flag
// Command sets, is reported with where it is. Test files and tests/, a test
// fixture's own program, are not fleet programs.
func TestEveryProcessStartGoesThroughCommand(t *testing.T) {
	root := repositoryRoot(t)
	files, allowed := 0, map[string]int{}
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
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
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		inExecx := filepath.ToSlash(filepath.Dir(relative)) == "internal/execx"
		imported := importNames(file)
		report := func(node ast.Node, what string) {
			violations = append(violations, relative+":"+strconv.Itoa(fileSet.Position(node.Pos()).Line)+": "+what)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.CallExpr:
				name, ok := qualified(node.Fun, imported)
				if !ok || !starts(name) || inExecx {
					return true
				}
				key := relative + " " + imported.local(name)
				if _, ok := startsElsewhere[key]; ok {
					allowed[key]++
					return true
				}
				report(node, "starts a process with "+imported.local(name)+" instead of execx.Command")
			case *ast.CompositeLit:
				if name, ok := qualified(node.Type, imported); ok && name == "os/exec.Cmd" && !inExecx {
					report(node, "builds an exec.Cmd by hand instead of with execx.Command")
				}
			case *ast.AssignStmt:
				if node.Tok != token.ASSIGN || inExecx {
					return true
				}
				for _, left := range node.Lhs {
					if selector, ok := left.(*ast.SelectorExpr); ok && (selector.Sel.Name == "SysProcAttr" || selector.Sel.Name == "CreationFlags") {
						report(node, "assigns "+selector.Sel.Name+" outright, dropping the flags execx.Command set; add to CreationFlags with |= instead")
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A guard that stops reading files approves everything, so it counts
	// what it read: the fleet has well over a hundred files, and the one
	// allowed start is in a Windows-only file.
	if files < 100 {
		t.Fatalf("the guard read %d Go files under %s; it is not reading the repository", files, root)
	}
	for key := range startsElsewhere {
		if allowed[key] == 0 {
			t.Errorf("the guard no longer sees the allowed start %s; remove it from startsElsewhere if it is gone", key)
		}
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// imports maps an import path to the name a file refers to it by.
type imports map[string]string

func importNames(file *ast.File) imports {
	names := imports{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		names[path] = name
	}
	return names
}

// local writes an import-qualified name the way the file does, such as
// exec.Command for os/exec.Command.
func (names imports) local(name string) string {
	path, member := name[:strings.LastIndex(name, ".")], name[strings.LastIndex(name, ".")+1:]
	return names[path] + "." + member
}

// qualified names expression as import path and member, such as
// os/exec.Command, when it is a member of an imported package.
func qualified(expression ast.Expr, names imports) (string, bool) {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	for path, name := range names {
		if name == pkg.Name {
			return path + "." + selector.Sel.Name, true
		}
	}
	return "", false
}

func starts(name string) bool {
	for path, members := range starters {
		for _, member := range members {
			if name == path+"."+member {
				return true
			}
		}
	}
	return false
}

// repositoryRoot is the directory holding this module's go.mod.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.TrimSpace(line) == "module github.com/fpresta0607/code-goblins" {
					return dir
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no code-goblins go.mod above the test's directory")
		}
		dir = parent
	}
}
