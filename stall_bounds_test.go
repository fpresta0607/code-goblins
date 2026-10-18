package codegoblins

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// stallFloor is the shortest bound a test may hold the runner's clock to. A
// hosted runner is an Azure virtual machine, which Azure pauses for host
// maintenance, and a paused machine stops every process at once: Azure says
// almost every such pause lasts under ten seconds, and the one #1048 saw
// lasted eleven. A wait, a deadline or a measure of how long something took
// that is shorter fails by chance when a pause falls inside it, whatever the
// code under test did. This floor outlasts such a pause with as long again
// left for the work the bound waits on. Azure's rare pause of about thirty
// seconds, no more than once in eighteen months on one machine, is accepted:
// citest runs a test that failed once more.
const stallFloor = 30 * time.Second

// stallAccepted marks a bound shorter than stallFloor that is meant to be:
// one a stall cannot fail, such as a wait for nothing to happen or a context
// the test means to run out, or one whose failure is accepted. The mark is
// a comment on the bound's line or just above the statement that holds it,
// and the reason follows it.
const stallAccepted = "stall-accepted:"

const modulePath = "github.com/fpresta0607/code-goblins"

// clockBound is one place a test holds the runner's clock to a duration.
type clockBound struct {
	position token.Position
	// shape is how the bound is written: a timer, a deadline, a context's
	// timeout, a measure of how long something took, a wait on a handle in
	// milliseconds, or a helper's bound handed its duration at a call.
	shape    string
	function string
	source   string
	value    time.Duration
	isKnown  bool
	// marks are the lines a stallAccepted comment may stand on to accept the
	// bound: the statement's own lines and the comment just above it.
	marks [2]int
}

// stallScan is what scanClockBounds read and found.
type stallScan struct {
	files    int
	bounds   []clockBound
	findings []string
}

// sourcePackage is one directory's Go files as the scan reads them, every
// build constraint's alike.
type sourcePackage struct {
	files     map[string]*ast.File
	imports   map[*ast.File]map[string]string
	constants map[string]packageValue
}

// packageValue is a package-level constant or variable's declared value and
// the file it is declared in, whose imports name what the value uses.
type packageValue struct {
	value ast.Expr
	file  *ast.File
}

// helperBound is a bound whose duration is a parameter of the function
// holding it, judged at each call of that function in its package instead.
// A method is known by its calls through a selector and a function by its
// plain calls, so a closure that shares a method's name is not taken for it.
type helperBound struct {
	directory string
	function  string
	isMethod  bool
	parameter int
	shape     string
}

type stallScanner struct {
	root     string
	fileSet  *token.FileSet
	packages map[string]*sourcePackage
	errors   []string
}

// scanClockBounds reads every Go test file under root and finds each bound
// shorter than stallFloor that no stallAccepted mark accepts, each bound it
// cannot tell the length of, and each mark that accepts no such bound.
func scanClockBounds(root string) stallScan {
	scanner := &stallScanner{root: root, fileSet: token.NewFileSet(), packages: map[string]*sourcePackage{}}
	var testFiles []string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			scanner.errors = append(scanner.errors, err.Error())
			return nil
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			testFiles = append(testFiles, path)
		}
		return nil
	})
	var bounds []clockBound
	var helpers []helperBound
	for _, path := range testFiles {
		source := scanner.load(filepath.Dir(path))
		file := source.files[path]
		if file == nil {
			continue
		}
		found, foundHelpers := scanner.scanFile(path, file)
		bounds = append(bounds, found...)
		helpers = append(helpers, foundHelpers...)
	}
	seen := map[helperBound]bool{}
	for round := 0; len(helpers) > 0 && round < 8; round++ {
		helpers = slices.DeleteFunc(helpers, func(helper helperBound) bool {
			isSeen := seen[helper]
			seen[helper] = true
			return isSeen
		})
		var next []helperBound
		for _, path := range testFiles {
			found, foundHelpers := scanner.scanCalls(path, helpers)
			bounds = append(bounds, found...)
			next = append(next, foundHelpers...)
		}
		helpers = next
	}
	scan := stallScan{files: len(testFiles), bounds: bounds, findings: slices.Clone(scanner.errors)}
	scan.findings = append(scan.findings, scanner.judge(testFiles, bounds)...)
	return scan
}

// load parses every Go file in directory once.
func (scanner *stallScanner) load(directory string) *sourcePackage {
	if source, ok := scanner.packages[directory]; ok {
		return source
	}
	source := &sourcePackage{files: map[string]*ast.File{}, imports: map[*ast.File]map[string]string{}, constants: map[string]packageValue{}}
	scanner.packages[directory] = source
	entries, err := os.ReadDir(directory)
	if err != nil {
		scanner.errors = append(scanner.errors, err.Error())
		return source
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		file, err := parser.ParseFile(scanner.fileSet, path, nil, parser.ParseComments)
		if err != nil {
			scanner.errors = append(scanner.errors, fmt.Sprintf("%s does not parse, so its bounds went unread: %v", scanner.relative(path), err))
			continue
		}
		source.files[path] = file
		imports := map[string]string{}
		for _, spec := range file.Imports {
			importPath, _ := strconv.Unquote(spec.Path.Value)
			name := importPath[strings.LastIndex(importPath, "/")+1:]
			if spec.Name != nil {
				name = spec.Name.Name
			}
			imports[name] = importPath
		}
		source.imports[file] = imports
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || (general.Tok != token.CONST && general.Tok != token.VAR) {
				continue
			}
			for _, spec := range general.Specs {
				values := spec.(*ast.ValueSpec)
				for index, name := range values.Names {
					if index < len(values.Values) {
						source.constants[name.Name] = packageValue{value: values.Values[index], file: file}
					}
				}
			}
		}
	}
	return source
}

func (scanner *stallScanner) relative(path string) string {
	relative, err := filepath.Rel(scanner.root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

// scope is what a duration in one function is evaluated against.
type scope struct {
	scanner   *stallScanner
	directory string
	file      *ast.File
	locals    map[string]ast.Expr
	// timeNow are the locals that hold a reading of time.Now, and elapsed
	// those that hold a measure of how long something took.
	timeNow, elapsed map[string]bool
	parameters       map[string]int
	depth            int
}

func (scanner *stallScanner) scopeOf(path string, file *ast.File, function *ast.FuncDecl) scope {
	in := scope{scanner: scanner, directory: filepath.Dir(path), file: file, locals: map[string]ast.Expr{}, timeNow: map[string]bool{}, elapsed: map[string]bool{}, parameters: map[string]int{}}
	index := 0
	for _, field := range function.Type.Params.List {
		if len(field.Names) == 0 {
			index++
		}
		for _, name := range field.Names {
			in.parameters[name.Name] = index
			index++
		}
	}
	define := func(name string, value ast.Expr) {
		if _, ok := in.locals[name]; !ok {
			in.locals[name] = value
		}
		if in.isTimeCall(value, "Now") {
			in.timeNow[name] = true
		}
		if in.isElapsed(value) {
			in.elapsed[name] = true
		}
	}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.AssignStmt:
			if len(statement.Lhs) == len(statement.Rhs) {
				for index, left := range statement.Lhs {
					if name, ok := left.(*ast.Ident); ok {
						define(name.Name, statement.Rhs[index])
					}
				}
			}
		case *ast.ValueSpec:
			for index, name := range statement.Names {
				if index < len(statement.Values) {
					define(name.Name, statement.Values[index])
				}
			}
		}
		return true
	})
	return in
}

// isTimeCall reports whether expression calls one of the time package's
// functions names.
func (in scope) isTimeCall(expression ast.Expr, names ...string) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && in.scanner.packages[in.directory].imports[in.file][pkg.Name] == "time" && slices.Contains(names, selector.Sel.Name)
}

// isElapsed reports whether expression measures how long something took on
// the runner's clock: time.Since, or one reading subtracted from another.
func (in scope) isElapsed(expression ast.Expr) bool {
	if in.isTimeCall(expression, "Since") {
		return true
	}
	if name, ok := expression.(*ast.Ident); ok {
		return in.elapsed[name.Name]
	}
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Sub" && len(call.Args) == 1
}

// evaluate is the duration expression stands for, when the scan can tell:
// literals, the time package's units, arithmetic, conversions, min and max,
// a duration's Milliseconds, and constants and variables of the function,
// of its package or of another package of this module.
func (in scope) evaluate(expression ast.Expr) (float64, bool) {
	if in.depth > 16 {
		return 0, false
	}
	in.depth++
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind == token.INT || value.Kind == token.FLOAT {
			number, err := strconv.ParseFloat(strings.ReplaceAll(value.Value, "_", ""), 64)
			return number, err == nil
		}
	case *ast.ParenExpr:
		return in.evaluate(value.X)
	case *ast.UnaryExpr:
		number, ok := in.evaluate(value.X)
		if value.Op == token.SUB {
			return -number, ok
		}
		return number, ok && value.Op == token.ADD
	case *ast.BinaryExpr:
		left, isLeftKnown := in.evaluate(value.X)
		right, isRightKnown := in.evaluate(value.Y)
		if !isLeftKnown || !isRightKnown {
			return 0, false
		}
		switch value.Op {
		case token.MUL:
			return left * right, true
		case token.QUO:
			return left / right, right != 0
		case token.ADD:
			return left + right, true
		case token.SUB:
			return left - right, true
		}
	case *ast.CallExpr:
		if function, ok := value.Fun.(*ast.Ident); ok && (function.Name == "min" || function.Name == "max") && len(value.Args) > 0 {
			var numbers []float64
			for _, argument := range value.Args {
				number, ok := in.evaluate(argument)
				if !ok {
					return 0, false
				}
				numbers = append(numbers, number)
			}
			if function.Name == "min" {
				return slices.Min(numbers), true
			}
			return slices.Max(numbers), true
		}
		if len(value.Args) == 1 && (in.isTimeCall(value, "Duration") || isNumericConversion(value.Fun)) {
			return in.evaluate(value.Args[0])
		}
		if milliseconds, ok := value.Fun.(*ast.SelectorExpr); ok && milliseconds.Sel.Name == "Milliseconds" && len(value.Args) == 0 {
			duration, ok := in.evaluate(milliseconds.X)
			return duration / float64(time.Millisecond), ok
		}
	case *ast.SelectorExpr:
		pkg, ok := value.X.(*ast.Ident)
		if !ok {
			return 0, false
		}
		importPath := in.scanner.packages[in.directory].imports[in.file][pkg.Name]
		if importPath == "time" {
			units := map[string]time.Duration{"Nanosecond": time.Nanosecond, "Microsecond": time.Microsecond, "Millisecond": time.Millisecond, "Second": time.Second, "Minute": time.Minute, "Hour": time.Hour}
			unit, ok := units[value.Sel.Name]
			return float64(unit), ok
		}
		if strings.HasPrefix(importPath, modulePath+"/") {
			directory := filepath.Join(in.scanner.root, filepath.FromSlash(strings.TrimPrefix(importPath, modulePath+"/")))
			return in.packageValue(directory, value.Sel.Name)
		}
	case *ast.Ident:
		if local, ok := in.locals[value.Name]; ok {
			return in.evaluate(local)
		}
		return in.packageValue(in.directory, value.Name)
	}
	return 0, false
}

func isNumericConversion(function ast.Expr) bool {
	name, ok := function.(*ast.Ident)
	return ok && slices.Contains([]string{"int", "int64", "float64", "uint", "uint32", "uint64"}, name.Name)
}

func (in scope) packageValue(directory, name string) (float64, bool) {
	declared, ok := in.scanner.load(directory).constants[name]
	if !ok {
		return 0, false
	}
	return scope{scanner: in.scanner, directory: directory, file: declared.file, depth: in.depth}.evaluate(declared.value)
}

// scanFile finds the bounds of one test file, and the helpers whose bound is
// a duration their callers hand them.
func (scanner *stallScanner) scanFile(path string, file *ast.File) ([]clockBound, []helperBound) {
	var bounds []clockBound
	var helpers []helperBound
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		in := scanner.scopeOf(path, file, function)
		var stack []ast.Node
		found := func(node ast.Node, shape string, duration ast.Expr) {
			if name, ok := duration.(*ast.Ident); ok {
				if parameter, isParameter := in.parameters[name.Name]; isParameter {
					if _, isLocal := in.locals[name.Name]; !isLocal {
						helpers = append(helpers, helperBound{directory: in.directory, function: function.Name.Name, isMethod: function.Recv != nil, parameter: parameter, shape: shape})
						return
					}
				}
			}
			bounds = append(bounds, scanner.bound(in, stack, node, shape, function.Name.Name, duration))
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			parent := ast.Node(nil)
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			stack = append(stack, node)
			switch expression := node.(type) {
			case *ast.CallExpr:
				switch {
				case in.isTimeCall(expression, "After", "NewTimer", "AfterFunc") && len(expression.Args) > 0:
					found(expression, "timer", expression.Args[0])
				case scanner.isContextTimeout(in, expression):
					found(expression, "context", expression.Args[1])
				case isDeadline(in, expression):
					found(expression, "deadline", expression.Args[0])
				default:
					if milliseconds, isHandleWait := handleWaitArgument(expression); isHandleWait {
						found(expression, "handle wait", milliseconds)
					}
				}
			case *ast.BinaryExpr:
				loop, isLoopCondition := parent.(*ast.ForStmt)
				isLoopCondition = isLoopCondition && loop.Cond == expression
				switch {
				case (expression.Op == token.GTR || expression.Op == token.GEQ) && in.isElapsed(expression.X):
					found(expression, "elapsed", expression.Y)
				case (expression.Op == token.LSS || expression.Op == token.LEQ) && in.isElapsed(expression.Y):
					found(expression, "elapsed", expression.X)
				case (expression.Op == token.LSS || expression.Op == token.LEQ) && in.isElapsed(expression.X) && isLoopCondition:
					found(expression, "deadline", expression.Y)
				}
			}
			return true
		})
	}
	return bounds, helpers
}

func (scanner *stallScanner) isContextTimeout(in scope, call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) < 2 {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && scanner.packages[in.directory].imports[in.file][pkg.Name] == "context" && (selector.Sel.Name == "WithTimeout" || selector.Sel.Name == "WithTimeoutCause")
}

// isDeadline reports whether call is a reading of time.Now moved on by a
// duration that is not plainly into the past.
func isDeadline(in scope, call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Add" || len(call.Args) != 1 {
		return false
	}
	if isPast(call.Args[0]) {
		return false
	}
	if value, ok := in.evaluate(call.Args[0]); ok && value <= 0 {
		return false
	}
	if in.isTimeCall(selector.X, "Now") {
		return true
	}
	name, ok := selector.X.(*ast.Ident)
	return ok && in.timeNow[name.Name]
}

// handleWaitArgument is the milliseconds a Win32 wait on a handle, as
// WaitForSingleObject and WaitForMultipleObjects take them, holds the clock
// to, unless it waits for as long as it takes.
func handleWaitArgument(call *ast.CallExpr) (ast.Expr, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	index := map[string]int{"WaitForSingleObject": 1, "WaitForMultipleObjects": 2}[selector.Sel.Name]
	if index == 0 || len(call.Args) <= index {
		return nil, false
	}
	if infinite, ok := call.Args[index].(*ast.SelectorExpr); ok && infinite.Sel.Name == "INFINITE" {
		return nil, false
	}
	return call.Args[index], true
}

// isPast reports whether duration is plainly negative as written: a minus on
// it, or on the first factor of a product it is, as in -n * time.Second.
func isPast(duration ast.Expr) bool {
	switch value := duration.(type) {
	case *ast.UnaryExpr:
		return value.Op == token.SUB
	case *ast.ParenExpr:
		return isPast(value.X)
	case *ast.BinaryExpr:
		return (value.Op == token.MUL || value.Op == token.QUO) && isPast(value.X) && !isPast(value.Y)
	}
	return false
}

// bound is the clockBound node holds, with the lines a mark accepting it may
// stand on: from the comment just above the statement holding it to the
// bound's own line.
func (scanner *stallScanner) bound(in scope, stack []ast.Node, node ast.Node, shape, function string, duration ast.Expr) clockBound {
	position := scanner.fileSet.Position(node.Pos())
	first := position.Line
	for index := len(stack) - 1; index >= 0; index-- {
		if statement, ok := stack[index].(ast.Stmt); ok {
			if _, isBlock := statement.(*ast.BlockStmt); !isBlock {
				first = scanner.fileSet.Position(statement.Pos()).Line
				break
			}
		}
	}
	value, isKnown := in.evaluate(duration)
	if strings.HasPrefix(shape, "handle wait") {
		value *= float64(time.Millisecond)
	}
	return clockBound{position: position, shape: shape, function: function, source: scanner.source(node), value: time.Duration(value), isKnown: isKnown, marks: [2]int{first, position.Line}}
}

func (scanner *stallScanner) source(node ast.Node) string {
	start, end := scanner.fileSet.Position(node.Pos()), scanner.fileSet.Position(node.End())
	content, err := os.ReadFile(start.Filename)
	if err != nil || end.Offset > len(content) {
		return "?"
	}
	return strings.Join(strings.Fields(string(content[start.Offset:end.Offset])), " ")
}

// scanCalls finds, in one test file, the calls of the helpers that hold a
// bound their caller hands them, judging the duration each call hands.
func (scanner *stallScanner) scanCalls(path string, helpers []helperBound) ([]clockBound, []helperBound) {
	directory := filepath.Dir(path)
	file := scanner.packages[directory].files[path]
	if file == nil {
		return nil, nil
	}
	var bounds []clockBound
	var next []helperBound
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		in := scanner.scopeOf(path, file, function)
		var stack []ast.Node
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, node)
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			var name string
			var isMethodCall bool
			switch callee := call.Fun.(type) {
			case *ast.Ident:
				name = callee.Name
			case *ast.SelectorExpr:
				name, isMethodCall = callee.Sel.Name, true
			}
			for _, helper := range helpers {
				if helper.directory != directory || helper.function != name || helper.isMethod != isMethodCall || helper.parameter >= len(call.Args) {
					continue
				}
				duration := call.Args[helper.parameter]
				if argument, ok := duration.(*ast.Ident); ok {
					if parameter, isParameter := in.parameters[argument.Name]; isParameter {
						if _, isLocal := in.locals[argument.Name]; !isLocal {
							next = append(next, helperBound{directory: directory, function: function.Name.Name, isMethod: function.Recv != nil, parameter: parameter, shape: helper.shape})
							continue
						}
					}
				}
				bounds = append(bounds, scanner.bound(in, stack, call, helper.shape+" in "+name, function.Name.Name, duration))
			}
			return true
		})
	}
	return bounds, next
}

// judge turns the bounds into findings: each one shorter than stallFloor or
// of a length the scan cannot tell that no mark accepts, and each mark that
// accepts no such bound or gives no reason.
func (scanner *stallScanner) judge(testFiles []string, bounds []clockBound) []string {
	type mark struct {
		path   string
		line   int
		reason string
	}
	marks := map[string]map[int]*mark{}
	var every []*mark
	used := map[*mark]bool{}
	for _, path := range testFiles {
		file := scanner.packages[filepath.Dir(path)].files[path]
		if file == nil {
			continue
		}
		marks[path] = map[int]*mark{}
		for _, group := range file.Comments {
			_, reason, isMark := strings.Cut(group.Text(), stallAccepted)
			if !isMark {
				continue
			}
			first, last := scanner.fileSet.Position(group.Pos()).Line, scanner.fileSet.Position(group.End()).Line
			found := &mark{path: path, line: first, reason: strings.TrimSpace(reason)}
			every = append(every, found)
			for line := first; line <= last; line++ {
				marks[path][line] = found
			}
		}
	}
	var findings []string
	for _, bound := range bounds {
		if bound.isKnown && (bound.value <= 0 || bound.value >= stallFloor) {
			continue
		}
		var accepting *mark
		for line := bound.marks[0] - 1; line <= bound.marks[1] && accepting == nil; line++ {
			accepting = marks[bound.position.Filename][line]
		}
		if accepting != nil {
			used[accepting] = true
			continue
		}
		place := fmt.Sprintf("%s:%d: %s", scanner.relative(bound.position.Filename), bound.position.Line, bound.function)
		if !bound.isKnown {
			findings = append(findings, fmt.Sprintf("%s holds the clock to a %s whose length the scan cannot tell: %s", place, bound.shape, bound.source))
			continue
		}
		findings = append(findings, fmt.Sprintf("%s holds the clock to %s, under the %s floor, with a %s: %s", place, bound.value, stallFloor, bound.shape, bound.source))
	}
	for _, mark := range every {
		place := fmt.Sprintf("%s:%d", scanner.relative(mark.path), mark.line)
		if len(strings.Fields(mark.reason)) < 3 {
			findings = append(findings, place+": a "+stallAccepted+" mark gives no reason")
		}
		if !used[mark] {
			findings = append(findings, place+": a "+stallAccepted+" mark accepts no bound shorter than the floor")
		}
	}
	slices.Sort(findings)
	return slices.Compact(findings)
}

// stallFixture is a test file holding a bound of each shape the scan reads,
// short and long, marked and not, with the constants a bound may name in
// its own package and in another package of the module.
const stallFixture = `package wait

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/other"
	"golang.org/x/sys/windows"
)

const long = time.Minute

func TestBounds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(Patience):
	}
	timer := time.NewTimer(other.Grace)
	defer timer.Stop()
	deadline := time.Now().Add(long)
	soonest := time.Now().Add(2 * time.Second)
	started := time.Now()
	soon := started.Add(3 * time.Second)
	_, _, _ = deadline, soonest, soon
	if time.Since(started) > 10*time.Second {
		t.Fatal("slow")
	}
	for time.Since(started) < 7*time.Second {
	}
	if time.Since(started) < time.Second {
		t.Fatal("too soon")
	}
	var handle windows.Handle
	_, _ = windows.WaitForSingleObject(handle, 5000)
	_, _ = windows.WaitForSingleObject(handle, uint32((time.Minute).Milliseconds()))
	_, _ = windows.WaitForSingleObject(handle, windows.INFINITE)
	_, _ = windows.WaitForSingleObject(handle, 0)
	past := time.Now().Add(-time.Hour)
	earlier := started.Add(-time.Duration(len(t.Name())) * time.Second)
	_, _ = past, earlier
	waitFor(t, 4*time.Second)
	waitFor(t, time.Minute)
	var w waiter
	w.await(6 * time.Second)
	await := func(what string, within time.Duration) {}
	await("a closure that shares the method's name", 2*time.Second)
	<-time.After(29 * time.Second)
	<-time.After(30 * time.Second)
	// stall-accepted: nothing is meant to arrive
	// before this runs out.
	<-time.After(100 * time.Millisecond)
	// stall-accepted: it times the call on purpose.
	if ctx.Err() != nil ||
		time.Since(started) > 2*time.Second {
		t.Fatal("slow")
	}
	for _, row := range []struct{ within time.Duration }{{time.Second}} {
		<-time.After(row.within)
	}
	// stall-accepted: this mark accepts nothing at all.
	_ = started
	<-time.After(time.Millisecond) // stall-accepted: none
}

func waitFor(t *testing.T, within time.Duration) {
	<-time.After(within)
}

type waiter struct{}

func (waiter) await(within time.Duration) {
	<-time.After(within)
}
`

// The scan reports every short bound of each shape it reads, judges a
// helper's bound at its calls, reads the constants a bound names, and
// reports a mark that accepts nothing or gives no reason. It reports
// nothing else, so a bound at the floor or marked passes.
func TestTheStallScanReportsEachShortBoundItReadsAndNothingElse(t *testing.T) {
	// Arrange
	root := t.TempDir()
	files := map[string]string{
		"wait/wait.go":                    "package wait\n\nimport \"time\"\n\nconst Patience = 2 * time.Second\n",
		"wait/wait_test.go":               stallFixture,
		"wait/broken_test.go":             "package wait\n\nfunc {\n",
		"internal/other/other.go":         "package other\n\nimport \"time\"\n\nconst Grace = 1500 * time.Millisecond\n",
		"frontend/node_modules/x_test.go": "package x\n\nfunc {\n",
		"frontend/tools/tools_test.go":    "package tools\n\nimport \"time\"\n\nfunc TestTool() {\n\t<-time.After(time.Second)\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lineOf := func(text string) int {
		before, _, isFound := strings.Cut(stallFixture, text)
		if !isFound || strings.Count(stallFixture, text) != 1 {
			t.Fatalf("the fixture holds %q other than once", text)
		}
		return strings.Count(before, "\n") + 1
	}
	want := []string{
		fmt.Sprintf("wait/wait_test.go:%d: TestBounds holds the clock to 5s, under the 30s floor, with a context:", lineOf("5*time.Second)")),
		fmt.Sprintf("wait/wait_test.go:%d: TestBounds holds the clock to 2s, under the 30s floor, with a timer:", lineOf("time.After(Patience)")),
		fmt.Sprintf("wait/wait_test.go:%d: TestBounds holds the clock to 1.5s, under the 30s floor, with a timer:", lineOf("other.Grace")),
		fmt.Sprintf("wait/wait_test.go:%d: TestBounds holds the clock to 3s, under the 30s floor, with a deadline:", lineOf("started.Add(3")),
		fmt.Sprintf("wait/wait_test.go:%d: TestBounds holds the clock to 10s, under the 30s floor, with a elapsed:", lineOf("> 10*time.Second")),
		fmt.Sprintf("wait/wait_test.go:%d: TestBounds holds the clock to 7s, under the 30s floor, with a deadline:", lineOf("< 7*time.Second")),
		fmt.Sprintf("wait/wait_test.go:%d: TestBounds holds the clock to 4s, under the 30s floor, with a timer in waitFor:", lineOf("waitFor(t, 4")),
		fmt.Sprintf("wait/wait_test.go:%d: TestBounds holds the clock to 6s, under the 30s floor, with a timer in await:", lineOf("w.await(6")),
		fmt.Sprintf("wait/wait_test.go:%d: TestBounds holds the clock to 5s, under the 30s floor, with a handle wait:", lineOf("(handle, 5000)")),
		fmt.Sprintf("wait/wait_test.go:%d: TestBounds holds the clock to 29s, under the 30s floor, with a timer:", lineOf("29 * time.Second")),
		fmt.Sprintf("wait/wait_test.go:%d: TestBounds holds the clock to a timer whose length the scan cannot tell:", lineOf("row.within)")),
		fmt.Sprintf("wait/wait_test.go:%d: a %s mark accepts no bound shorter than the floor", lineOf("accepts nothing at all"), stallAccepted),
		fmt.Sprintf("wait/wait_test.go:%d: a %s mark gives no reason", lineOf("stall-accepted: none"), stallAccepted),
		"wait/broken_test.go does not parse, so its bounds went unread:",
		fmt.Sprintf("wait/wait_test.go:%d: TestBounds holds the clock to 2s, under the 30s floor, with a deadline:", lineOf("time.Now().Add(2 * time.Second)")),
		"frontend/tools/tools_test.go:6: TestTool holds the clock to 1s, under the 30s floor, with a timer:",
	}

	// Act
	scan := scanClockBounds(root)

	// Assert
	if scan.files != 3 {
		t.Errorf("the scan read %d test files, want the 3 outside node_modules", scan.files)
	}
	for _, prefix := range want {
		if !slices.ContainsFunc(scan.findings, func(finding string) bool { return strings.HasPrefix(finding, prefix) }) {
			t.Errorf("no finding starts %q", prefix)
		}
	}
	if len(scan.findings) != len(want) {
		t.Errorf("%d findings, want %d:\n%s", len(scan.findings), len(want), strings.Join(scan.findings, "\n"))
	}
}

// A hosted runner can stop every process for about ten seconds (#1048), and
// a test that holds the runner's clock to less than that fails by chance
// when such a stall falls inside its bound. Every bound a test holds the
// clock to is at least stallFloor, or carries a stallAccepted mark saying
// why it may be shorter.
func TestNoTestHoldsTheClockToLessThanAStall(t *testing.T) {
	// Act
	scan := scanClockBounds(".")

	// Assert
	if scan.files < 100 {
		t.Fatalf("the scan read %d test files, so it is not reading this repository's tests", scan.files)
	}
	shapes := map[string]int{}
	for _, bound := range scan.bounds {
		shapes[strings.Fields(bound.shape)[0]]++
	}
	for _, shape := range []string{"timer", "deadline", "context", "elapsed", "handle"} {
		if shapes[shape] == 0 {
			t.Errorf("the scan found no %s in %d test files, so it no longer sees that shape", shape, scan.files)
		}
	}
	for _, finding := range scan.findings {
		t.Error(finding)
	}
	if len(scan.findings) > 0 {
		t.Logf("%d findings in %d test files. Lengthen a bound that only keeps a hung test from waiting forever, make one that times the code measure what it means instead, or mark one a stall cannot fail with %q and the reason.", len(scan.findings), scan.files, stallAccepted)
	}
}
