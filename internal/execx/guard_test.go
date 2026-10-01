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
// package, each with why it cannot.
var startsElsewhere = map[string]string{
	"internal/conpty/conpty_windows.go windows.CreateProcess":   "the process runs in a pseudo console, which has no window",
	"internal/supervisor/runs_windows.go windows.CreateProcess": "a run item's window is meant to show and to read what the Overlord types in it, which takes starting it without standard handles; an exec.Cmd always hands it some",
}

// Every process a fleet program starts goes through Command or
// CommandContext, which start it without a console window, so a new call
// site cannot forget to. A process starter named outside this package,
// called or not, an exec.Cmd made by value, or a SysProcAttr or
// CreationFlags set other than with |=, which would drop the flag Command
// sets, is reported with where it is. Test files and tests/, a test
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
		if filepath.ToSlash(filepath.Dir(relative)) == "internal/execx" {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found, allowedStarts, err := bypasses(relative, source)
		if err != nil {
			return err
		}
		violations = append(violations, found...)
		for _, key := range allowedStarts {
			allowed[key]++
		}
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

func TestBypassesReportsEveryStartAroundCommand(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		imports    string
		body       string
		isReported bool
		isAllowed  bool
	}{
		{name: "exec.Command call", imports: `"os/exec"`, body: `exec.Command("git").Run()`, isReported: true},
		{name: "exec.CommandContext call", imports: `"context"; "os/exec"`, body: `exec.CommandContext(context.Background(), "git").Run()`, isReported: true},
		{name: "exec.Command as a value", imports: `"os/exec"`, body: `start := exec.Command; start("git").Run()`, isReported: true},
		{name: "renamed os/exec import", imports: `run "os/exec"`, body: `run.Command("git").Run()`, isReported: true},
		{name: "os.StartProcess", imports: `"os"`, body: `os.StartProcess("git", nil, nil)`, isReported: true},
		{name: "syscall.StartProcess", imports: `"syscall"`, body: `syscall.StartProcess("git", nil, nil)`, isReported: true},
		{name: "syscall.CreateProcess", imports: `"syscall"`, body: `syscall.CreateProcess(nil, nil, nil, nil, false, 0, nil, nil, nil, nil)`, isReported: true},
		{name: "syscall.CreateProcessAsUser", imports: `"syscall"`, body: `syscall.CreateProcessAsUser(0, nil, nil, nil, nil, false, 0, nil, nil, nil, nil)`, isReported: true},
		{name: "windows.CreateProcess", imports: `"golang.org/x/sys/windows"`, body: `windows.CreateProcess(nil, nil, nil, nil, false, 0, nil, nil, nil, nil)`, isReported: true},
		{name: "windows.CreateProcessAsUser", imports: `"golang.org/x/sys/windows"`, body: `windows.CreateProcessAsUser(0, nil, nil, nil, nil, false, 0, nil, nil, nil, nil)`, isReported: true},
		{name: "windows.ShellExecute as a value", imports: `"golang.org/x/sys/windows"`, body: `open := windows.ShellExecute; _ = open`, isReported: true},
		{name: "exec.Cmd composite literal", imports: `"os/exec"`, body: `(&exec.Cmd{Path: "git"}).Run()`, isReported: true},
		{name: "new exec.Cmd", imports: `"os/exec"`, body: `started := new(exec.Cmd); started.Path = "git"`, isReported: true},
		{name: "exec.Cmd variable", imports: `"os/exec"`, body: `var started exec.Cmd; started.Run()`, isReported: true},
		{name: "SysProcAttr assigned", imports: `"os/exec"; "syscall"`, body: `cmd.SysProcAttr = &syscall.SysProcAttr{}`, isReported: true},
		{name: "SysProcAttr dereference assigned", imports: `"os/exec"; "syscall"`, body: `*cmd.SysProcAttr = syscall.SysProcAttr{}`, isReported: true},
		{name: "CreationFlags assigned", imports: `"os/exec"`, body: `cmd.SysProcAttr.CreationFlags = flags`, isReported: true},
		{name: "CreationFlags cleared", imports: `"os/exec"`, body: `cmd.SysProcAttr.CreationFlags &^= flags`, isReported: true},
		{name: "execx.Command", imports: `"os/exec"; "github.com/fpresta0607/code-goblins/internal/execx"`, body: `execx.Command("git").Run()`},
		{name: "execx.CommandContext", imports: `"context"; "os/exec"; "github.com/fpresta0607/code-goblins/internal/execx"`, body: `execx.CommandContext(context.Background(), "git").Run()`},
		{name: "CreationFlags added to", imports: `"os/exec"`, body: `cmd.SysProcAttr.CreationFlags |= flags`},
		{name: "HideWindow assigned", imports: `"os/exec"`, body: `cmd.SysProcAttr.HideWindow = true`},
		{name: "allowed conpty start", path: "internal/conpty/conpty_windows.go", imports: `"os/exec"; "golang.org/x/sys/windows"`, body: `windows.CreateProcess(nil, nil, nil, nil, false, 0, nil, nil, nil, nil)`, isAllowed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			path := test.path
			if path == "" {
				path = "internal/example/example.go"
			}
			source := "package example\n\nimport (" + test.imports + ")\n\nfunc example(cmd *exec.Cmd, flags uint32) {\n" + test.body + "\n}\n"
			wantViolations, wantAllowed := 0, 0
			if test.isReported {
				wantViolations = 1
			}
			if test.isAllowed {
				wantAllowed = 1
			}

			// Act
			violations, allowed, err := bypasses(path, []byte(source))

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(violations) != wantViolations {
				t.Errorf("violations = %q, want %d", violations, wantViolations)
			}
			if len(allowed) != wantAllowed {
				t.Errorf("allowed starts = %q, want %d", allowed, wantAllowed)
			}
		})
	}
}

// bypasses parses a fleet file's source and reports, as path:line: what,
// each place it starts a process around Command, and the startsElsewhere
// keys of the allowed starts it holds.
func bypasses(relative string, source []byte) (violations, allowed []string, err error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, relative, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, err
	}
	imported := importNames(file)
	report := func(node ast.Node, what string) {
		violations = append(violations, relative+":"+strconv.Itoa(fileSet.Position(node.Pos()).Line)+": "+what)
	}
	pointed := map[ast.Expr]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.StarExpr:
			pointed[node.X] = true
		case *ast.SelectorExpr:
			name, ok := qualified(node, imported)
			if !ok {
				return true
			}
			if name == "os/exec.Cmd" && !pointed[node] {
				report(node, "makes an exec.Cmd itself instead of with execx.Command")
			}
			if !starts(name) {
				return true
			}
			key := relative + " " + imported.local(name)
			if _, ok := startsElsewhere[key]; ok {
				allowed = append(allowed, key)
				return true
			}
			report(node, "starts a process with "+imported.local(name)+" instead of execx.Command")
		case *ast.AssignStmt:
			if node.Tok == token.OR_ASSIGN {
				return true
			}
			for _, left := range node.Lhs {
				if star, ok := left.(*ast.StarExpr); ok {
					left = star.X
				}
				if selector, ok := left.(*ast.SelectorExpr); ok && (selector.Sel.Name == "SysProcAttr" || selector.Sel.Name == "CreationFlags") {
					report(node, "sets "+selector.Sel.Name+" with "+node.Tok.String()+", dropping the flags execx.Command set; add to CreationFlags with |= instead")
				}
			}
		}
		return true
	})
	return violations, allowed, nil
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
