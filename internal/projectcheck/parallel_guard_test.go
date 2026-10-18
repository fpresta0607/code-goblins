package projectcheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// Every test of this package runs beside the others, and every subtest
// beside its siblings. A test here builds a git repository and runs a whole
// check, and its time is the git processes those start, about 14 a check,
// each waited on in turn: a local run of the package started 2,814 of them
// and spent 218 of its 227 s waiting on them. Nothing ties one test to
// another, since each works in a temporary folder of its own, yet one after
// another the tests took 162 to 313 s of go test's 20-minute limit on CI's
// runners, so a runner 4.3 times slow, as run 38974520692's was, would cut
// the package off with no test hung (#1062). A test or a subtest whose first
// statement is not t.Parallel() is reported with where it is.
func TestEveryTestRunsBesideTheOthers(t *testing.T) {
	t.Parallel()
	// Arrange
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	positions := token.NewFileSet()

	// Act
	var serial []string
	tests, subtests := 0, 0
	for _, name := range files {
		file, err := parser.ParseFile(positions, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || !strings.HasPrefix(function.Name.Name, "Test") || !takesTestingT(function.Type) {
				continue
			}
			tests++
			if !startsParallel(function.Type, function.Body) {
				serial = append(serial, positions.Position(function.Pos()).String()+": "+function.Name.Name)
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) != 2 {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				subtest, isLiteral := call.Args[1].(*ast.FuncLit)
				if !ok || selector.Sel.Name != "Run" || !isLiteral || !takesTestingT(subtest.Type) {
					return true
				}
				subtests++
				if !startsParallel(subtest.Type, subtest.Body) {
					serial = append(serial, positions.Position(call.Pos()).String()+": a subtest of "+function.Name.Name)
				}
				return true
			})
		}
	}

	// Assert
	// A guard that stops reading tests approves every one, so it counts
	// what it read: the package has over a hundred tests and a score of
	// subtests.
	if tests < 100 || subtests < 20 {
		t.Fatalf("read %d tests and %d subtests in %d files, want the package's more than 100 and 20", tests, subtests, len(files))
	}
	for _, test := range serial {
		t.Errorf("%s does not start with t.Parallel(), so it waits for the tests before it", test)
	}
}

// takesTestingT reports whether a function's one parameter is a *testing.T.
func takesTestingT(signature *ast.FuncType) bool {
	parameters := signature.Params.List
	if len(parameters) != 1 || len(parameters[0].Names) != 1 {
		return false
	}
	pointer, ok := parameters[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	qualifier, ok := selector.X.(*ast.Ident)
	return ok && qualifier.Name == "testing" && selector.Sel.Name == "T"
}

// startsParallel reports whether a test's first statement is t.Parallel()
// on its own *testing.T.
func startsParallel(signature *ast.FuncType, body *ast.BlockStmt) bool {
	if len(body.List) == 0 {
		return false
	}
	statement, ok := body.List[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := statement.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Parallel" {
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	return ok && receiver.Name == signature.Params.List[0].Names[0].Name
}
