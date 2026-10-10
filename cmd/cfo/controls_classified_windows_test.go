package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/priority/prioritytest"
)

// staysAtNormal is every command of the dispatch that is not a control, with
// why it is not raised.
var staysAtNormal = map[string]string{
	"host":              "a terminal's host raises itself, whoever starts it (host.Run)",
	"install":           "copies programs and writes the user's settings",
	"uninstall":         "removes an installation",
	"hooks":             "writes a harness's hook files",
	"version":           "prints one line",
	"doctor":            "runs every tool's own version check",
	"dictation":         "downloads and checks the speech engine",
	"dev-drive":         "reads and sets up drives",
	"defender":          "reads Microsoft Defender's own records, which takes seconds",
	"allowance-floor":   "sets one number nothing waits on",
	"home":              "hashes and moves a whole home",
	"pipeline":          "starts and answers gate runs, which are work",
	"afk":               "a switch and a log nothing waits on",
	"auth":              "runs each service's own sign-in check",
	"connection-repair": "runs a sign-in",
	"project":           "reads a whole checkout",
	"route":             "prints a routing decision",
	"verify":            "runs a project's checks, which are work",
	"security":          "runs a project's checks, which are work",
	"hygiene":           "runs a project's checks, which are work",
	"gate":              "runs a gate's tests, which are work",
	"deploy":            "runs a project's deploy",
	"evidence":          "gathers a pull request's evidence from GitHub",
	"supersede":         "closes pull requests on GitHub",
	"spawn":             "makes a worktree and starts a harness in it",
	"switch":            "starts a harness, and its sweep of what the last one left is raised by itself",
	"cleanup":           "removes a worktree, and its sweep of what the terminal left is raised by itself",
	"title":             "writes one title nothing waits on",
	"backlog":           "edits the backlog file",
	"services":          "starts a project's services, which are work",
	"runtime":           "runs the container engine's own listings",
	"tickets":           "reads a repository from GitHub",
	"brief":             "writes a brief's scaffold",
	"pr":                "reads and merges on GitHub",
	"merge-local":       "runs git merges",
	"worktree":          "makes a worktree",
	"reap":              "reads worktrees and returns them through cleanup",
	"helper":            "a goblin's own request",
	"present":           "a goblin's own report",
	"review":            "starts a review page's server",
	"deliver":           "copies a document",
	"run-request":       "files a card nothing waits on",
	"setup":             "the launcher's setup, which waits on a person",
	"voice-worker":      "runs the speech engine, which is heavy, and leaves before the dispatch",
}

// dispatched reads main.go for every command cfo dispatches on: each label of
// the one switch on args[0], and each word args[0] is compared with in run
// and runWithRuntime, a flag aside. A label or a comparison that is not a
// plain string fails the test, which could not classify it.
func dispatched(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	isFirstArgument := func(expression ast.Expr) bool {
		index, isIndex := expression.(*ast.IndexExpr)
		if !isIndex {
			return false
		}
		name, isName := index.X.(*ast.Ident)
		position, isLiteral := index.Index.(*ast.BasicLit)
		return isName && name.Name == "args" && isLiteral && position.Value == "0"
	}
	word := func(expression ast.Expr) (string, bool) {
		literal, isLiteral := expression.(*ast.BasicLit)
		if !isLiteral || literal.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(literal.Value)
		return value, err == nil
	}
	var commands []string
	switches := 0
	for _, declaration := range file.Decls {
		function, isFunction := declaration.(*ast.FuncDecl)
		if !isFunction || function.Name.Name != "run" && function.Name.Name != "runWithRuntime" {
			continue
		}
		ast.Inspect(function, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.SwitchStmt:
				if node.Tag == nil || !isFirstArgument(node.Tag) {
					return true
				}
				switches++
				for _, clause := range node.Body.List {
					for _, label := range clause.(*ast.CaseClause).List {
						command, isWord := word(label)
						if !isWord {
							t.Errorf("a label of the dispatch in main.go is not a plain string, so this test cannot classify it")
							continue
						}
						commands = append(commands, command)
					}
				}
			case *ast.BinaryExpr:
				if node.Op != token.EQL && node.Op != token.NEQ {
					return true
				}
				for _, sides := range [][2]ast.Expr{{node.X, node.Y}, {node.Y, node.X}} {
					if !isFirstArgument(sides[0]) {
						continue
					}
					command, isWord := word(sides[1])
					if !isWord {
						t.Errorf("main.go compares args[0] with something that is not a plain string, so this test cannot classify it")
						continue
					}
					// A flag of the launcher is no command.
					if !strings.HasPrefix(command, "-") {
						commands = append(commands, command)
					}
				}
			}
			return true
		})
	}
	if switches != 1 {
		t.Fatalf("found %d switches on args[0] in run and runWithRuntime of main.go, want the one dispatch", switches)
	}
	slices.Sort(commands)
	return slices.Compact(commands)
}

// Every command is a control, which is raised, or stays at normal for a
// reason given here, so a command added to cfo is one or the other on
// purpose.
func TestEveryCommandIsAControlOrStaysAtNormalOnPurpose(t *testing.T) {
	// Arrange
	commands := dispatched(t)
	// A reading that stops seeing the dispatch would pass every command.
	if len(commands) < 50 {
		t.Fatalf("read %d commands from main.go, so the test is not reading its dispatch", len(commands))
	}

	// Act
	var unclassified, both []string
	for _, command := range commands {
		_, isLeft := staysAtNormal[command]
		switch {
		case controls[command] && isLeft:
			both = append(both, command)
		case !controls[command] && !isLeft:
			unclassified = append(unclassified, command)
		}
	}

	// Assert
	if len(unclassified) > 0 {
		t.Errorf("these commands are neither a control nor left at normal for a reason: %v", unclassified)
	}
	if len(both) > 0 {
		t.Errorf("these commands are both a control and left at normal: %v", both)
	}
	for name := range controls {
		if !slices.Contains(commands, name) {
			t.Errorf("control %q is no command of cfo", name)
		}
	}
	for name := range staysAtNormal {
		if !slices.Contains(commands, name) {
			t.Errorf("%q is left at normal but is no command of cfo", name)
		}
	}
	if !maps.EqualFunc(controls, controlRuns, func(bool, controlRun) bool { return true }) {
		t.Errorf("the controls are %v and the tests run %v: every control needs a run in controlRuns", slices.Sorted(maps.Keys(controls)), slices.Sorted(maps.Keys(controlRuns)))
	}
}

// A control a goblin or a gate agent starts is not raised, whether or not it
// goes on to say anything: a hook leaves a goblin's session without a word.
func TestAControlAGoblinOrAGateAgentStartsIsNotRaised(t *testing.T) {
	for who, variable := range map[string][2]string{
		"a goblin":     {harness.RoleVariable, harness.RoleGoblin},
		"a gate agent": {gateAgentVariable, "1"},
	} {
		for name := range controls {
			t.Run(who+"/"+name, func(t *testing.T) {
				// Arrange
				prioritytest.FromNormal(t)
				t.Setenv(variable[0], variable[1])

				// Act
				restore := aboveTheWork([]string{name})
				during := processClass(t)
				restore()

				// Assert
				if during != windows.NORMAL_PRIORITY_CLASS {
					t.Errorf("cfo %s started by %s was raised to priority class %#x, want normal, %#x", name, who, during, uint32(windows.NORMAL_PRIORITY_CLASS))
				}
			})
		}
	}
}
