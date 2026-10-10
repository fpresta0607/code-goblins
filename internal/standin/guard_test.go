package standin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// stillCopies are the test files that still write a copy of their own test
// binary to run, each with what its copy stands in for. Each stand-in here
// has to run its package's own code under a program's name, which the one
// stand-in program cannot do. A copy is a new unsigned program at every
// build, which antivirus asks its cloud about before it lets it start, so
// its first start is slow (0.4 to 3 s on 2026-10-10, and past a launch
// deadline under load). None of these copies appears in Microsoft Defender's
// records: the ones it convicted were the install tests', which a script
// downloaded, renamed and ran, and those are gone. Take a file out of the
// list with its copy, and add none.
var stillCopies = map[string]string{
	"cmd/cfo/attach_test.go":                        "claude.exe on PATH, answered by this package's TestMain",
	"cmd/cfo/cfo_restart_test.go":                   "claude.exe on PATH, answered by this package's TestMain",
	"cmd/cfo/doctor_test.go":                        "claude.exe, answered by this package's TestMain",
	"cmd/cfo/hook_handover_windows_test.go":         "a watcher lock holder under a program's name",
	"cmd/cfo/reap_kill_windows_test.go":             "a stand-in Docker Desktop.exe that runs this package's code",
	"cmd/cfo/serve_herdr_env_test.go":               "herdr.exe on PATH, answered by this package's TestMain",
	"cmd/cfo/serve_test.go":                         "claude.exe on PATH, answered by this package's TestMain",
	"cmd/cfo/session_standin_windows_test.go":       "the session's programs, answered by this package's TestMain",
	"cmd/cfo/update_standin_windows_test.go":        "reads the build marker at the end of its own file, where it is, and copies nothing",
	"cmd/cfo/update_windows_test.go":                "builds of cfo told apart by a marker, which run this package's update code",
	"internal/conpty/conpty_windows_test.go":        "a stand-in Docker Desktop.exe that runs this package's code",
	"internal/janitor/processes_windows_test.go":    "a stand-in chrome.exe that runs this package's code",
	"internal/lifecycle/resources_windows_test.go":  "another task's test binary that runs this package's code",
	"internal/lifecycle/services_windows_test.go":   "a stand-in Docker Desktop.exe that runs this package's code",
	"internal/monitor/polls_windows_test.go":        "a poller under a program's name",
	"internal/spawn/native_launch_windows_test.go":  "the harness a native spawn starts, answered by this package's TestMain",
	"internal/spawn/prove_windows_test.go":          "a staged harness build, answered by this package's TestMain",
	"internal/supervisor/git_deadline_test.go":      "git.exe on PATH, answered by this package's TestMain",
	"internal/supervisor/handover_windows_test.go":  "a watcher lock holder under a program's name",
	"internal/supervisor/terminal_windows_test.go":  "git.exe on PATH, answered by this package's TestMain",
	"internal/watch/orphan_standin_windows_test.go": "a fleet of stand-in processes under programs' names",
}

// ownBinaryCopy matches the functions that read or duplicate a file: the
// calls a test makes to copy its own test binary somewhere.
var ownBinaryCopy = regexp.MustCompile(`(?i)^(readfile|open|openfile|link)$|copy`)

// No test serves or runs a copy of its own test binary, beyond the files in
// stillCopies: a test that needs some program to start puts the one stand-in
// there (Put, Bytes), whose bytes are the same at every run. The guard reads
// every test file for a call that reads or copies the file os.Executable or
// os.Args[0] names. It follows a name within one file only, so a copy made
// through a helper in another file, or through a struct field, gets past it.
func TestNoTestCopiesItsOwnTestBinary(t *testing.T) {
	root := repositoryRoot(t)
	files := 0
	copies := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".worktrees", "node_modules", "testdata", "frontend":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files++
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		found, err := ownBinaryCopies(relative, source)
		if err != nil {
			return err
		}
		if len(found) > 0 {
			copies[relative] = found
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A guard that stops reading files approves everything, so it counts what
	// it read: the repository has several hundred test files.
	if files < 300 {
		t.Fatalf("the guard read %d test files under %s; it is not reading the repository", files, root)
	}
	for file, found := range copies {
		if _, isKnown := stillCopies[file]; isKnown {
			t.Logf("still copies: %s", strings.Join(found, ", "))
			continue
		}
		{
			for _, copy := range found {
				t.Errorf("%s: a test that needs a program to start uses standin.Put or standin.Bytes, never a copy of its own test binary", copy)
			}
		}
	}
	for file := range stillCopies {
		if _, stillDoes := copies[file]; !stillDoes {
			t.Errorf("%s no longer copies its own test binary; take it out of stillCopies", file)
		}
	}
}

func TestOwnBinaryCopiesReportsEveryCopyOfTheTestBinary(t *testing.T) {
	for _, test := range []struct {
		name       string
		body       string
		isReported bool
	}{
		{"read to write elsewhere", `self, _ := os.Executable(); data, _ := os.ReadFile(self); _ = os.WriteFile("cfo.exe", data, 0o755)`, true},
		{"opened to copy", `self, err := os.Executable(); _ = err; source, _ := os.Open(self); _ = source`, true},
		{"handed to a copy helper", `program, _ := os.Executable(); copyFile(t, program, "claude.exe")`, true},
		{"handed to a package's copy helper", `program, _ := os.Executable(); fsx.CopyFile(program, "claude.exe")`, true},
		{"linked", `self, _ := os.Executable(); _ = os.Link(self, "git.exe")`, true},
		{"its first argument read", `data, _ := os.ReadFile(os.Args[0]); _ = data`, true},
		{"its first argument named and copied", `self := os.Args[0]; copyExecutable(t, self, "git.exe")`, true},
		{"run where it is", `self, _ := os.Executable(); _ = exec.Command(self, "-test.run=^TestChild$").Run()`, false},
		{"its first argument run where it is", `_ = exec.Command(os.Args[0], "-test.run=^TestChild$").Run()`, false},
		{"named in a command line", `self, _ := os.Executable(); _ = []string{self, "host"}`, false},
		{"another file read", `self, _ := os.Executable(); _ = self; data, _ := os.ReadFile("install.ps1"); _ = data`, false},
		{"the stand-in put", `standin.Put(t, "cfo.exe")`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			source := "package example\n\nfunc TestExample(t *testing.T) {\n" + test.body + "\n}\n"

			// Act
			found, err := ownBinaryCopies("internal/example/example_test.go", []byte(source))

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if isReported := len(found) > 0; isReported != test.isReported {
				t.Errorf("reported %q, want reported = %v", found, test.isReported)
			}
		})
	}
}

// ownBinaryCopies parses a test file's source and reports, as path:line: what,
// each call that reads or copies the file os.Executable or os.Args[0] names.
func ownBinaryCopies(relative string, source []byte) ([]string, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, relative, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	// The names the file gives its own test binary's path.
	own := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) == 0 || len(assign.Rhs) != 1 || !isOwnBinary(assign.Rhs[0], nil) {
			return true
		}
		if name, ok := assign.Lhs[0].(*ast.Ident); ok && name.Name != "_" {
			own[name.Name] = true
		}
		return true
	})
	var found []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !ownBinaryCopy.MatchString(calleeName(call)) {
			return true
		}
		if slices.ContainsFunc(call.Args, func(argument ast.Expr) bool { return isOwnBinary(argument, own) }) {
			found = append(found, relative+":"+strconv.Itoa(fileSet.Position(call.Pos()).Line)+": "+calleeName(call)+" reads or copies this test binary")
		}
		return true
	})
	return found, nil
}

// isOwnBinary reports whether expression is the test binary's own path:
// os.Executable(), os.Args[0], or one of the names own holds.
func isOwnBinary(expression ast.Expr, own map[string]bool) bool {
	switch expression := expression.(type) {
	case *ast.Ident:
		return own[expression.Name]
	case *ast.CallExpr:
		return isSelector(expression.Fun, "os", "Executable")
	case *ast.IndexExpr:
		index, ok := expression.Index.(*ast.BasicLit)
		return ok && index.Value == "0" && isSelector(expression.X, "os", "Args")
	}
	return false
}

func isSelector(expression ast.Expr, pkg, name string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	owner, ok := selector.X.(*ast.Ident)
	return ok && owner.Name == pkg
}

// calleeName is the name a call names its function by, without its package
// or receiver.
func calleeName(call *ast.CallExpr) string {
	switch function := call.Fun.(type) {
	case *ast.Ident:
		return function.Name
	case *ast.SelectorExpr:
		return function.Sel.Name
	}
	return ""
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
