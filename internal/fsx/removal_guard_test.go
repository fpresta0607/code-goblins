package fsx

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// Every folder a fleet program removes, a goblin's worktree among them, goes
// through RemoveAll, which removes a link as a link. On 2026-10-11 three
// cleanups failed with "Directory not empty" because `git worktree remove`
// removed the files itself: Git for Windows takes a junction for a folder,
// so it deleted what pnpm's junctions pointed at and stopped at one its own
// deletion had left dangling, and it deletes whatever a junction out of the
// worktree points at. So no fleet program runs `git worktree remove`, and a
// raw os.RemoveAll named outside this package, called or not, is reported
// with where it is.
func TestEveryFleetRemovalGoesThroughFsx(t *testing.T) {
	for _, violation := range fleetViolations(t, removalsAround) {
		t.Error(violation)
	}
}

func TestRemovalsAroundReportsEveryRawRemoval(t *testing.T) {
	tests := []struct {
		name       string
		imports    string
		body       string
		isReported bool
	}{
		{name: "os.RemoveAll call", imports: `"os"`, body: `os.RemoveAll("worktree")`, isReported: true},
		{name: "os.RemoveAll as a value", imports: `"os"`, body: `remove := os.RemoveAll; remove("worktree")`, isReported: true},
		{name: "renamed os import", imports: `host "os"`, body: `host.RemoveAll("worktree")`, isReported: true},
		{name: "git worktree remove in a call", imports: `"os/exec"`, body: `exec.Command("git", "-c", "core.longpaths=true", "worktree", "remove", "--force", "worktree")`, isReported: true},
		{name: "git worktree remove in a list", imports: `"os/exec"`, body: `exec.Command("git", []string{"worktree", "remove", "worktree"}...)`, isReported: true},
		{name: "fsx.RemoveAll", imports: `"github.com/fpresta0607/code-goblins/internal/fsx"`, body: `fsx.RemoveAll("worktree")`},
		{name: "os.Remove", imports: `"os"`, body: `os.Remove("empty")`},
		{name: "git worktree prune", imports: `"os/exec"`, body: `exec.Command("git", "worktree", "prune")`},
		{name: "a usage line", imports: `"fmt"`, body: `fmt.Println("cfo worktree remove <task-id> <name>")`},
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
			violations, err := removalsAround("internal/example/example.go", []byte(source))

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

// removalsAround parses a fleet file's source and reports, as path:line:
// what, each place it names os.RemoveAll or runs git worktree remove.
func removalsAround(relative string, source []byte) ([]string, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, relative, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	imported, err := importPaths(file)
	if err != nil {
		return nil, err
	}
	var violations []string
	report := func(node ast.Node, what string) {
		violations = append(violations, fmt.Sprintf("%s:%d: %s", relative, fileSet.Position(node.Pos()).Line, what))
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := node.X.(*ast.Ident); ok && imported[pkg.Name] == "os" && node.Sel.Name == "RemoveAll" {
				report(node, "removes with os.RemoveAll instead of fsx.RemoveAll")
			}
		case *ast.CallExpr:
			if removesWorktree(node.Args) {
				report(node, "runs git worktree remove, which deletes through a junction; remove a worktree with worktree.RunnerGit.Remove")
			}
		case *ast.CompositeLit:
			if removesWorktree(node.Elts) {
				report(node, "runs git worktree remove, which deletes through a junction; remove a worktree with worktree.RunnerGit.Remove")
			}
		}
		return true
	})
	return violations, nil
}

// removesWorktree reports whether the string literals "worktree" and
// "remove" stand next to each other among arguments.
func removesWorktree(arguments []ast.Expr) bool {
	for index := 1; index < len(arguments); index++ {
		if literal(arguments[index-1]) == "worktree" && literal(arguments[index]) == "remove" {
			return true
		}
	}
	return false
}

// literal is the value of a string literal, and "" for anything else.
func literal(expression ast.Expr) string {
	basic, ok := expression.(*ast.BasicLit)
	if !ok || basic.Kind != token.STRING {
		return ""
	}
	value, err := strconv.Unquote(basic.Value)
	if err != nil {
		return ""
	}
	return value
}
