package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"slices"
	"strconv"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/harness"
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
}

// Every command is a control, which is raised, or stays at normal for a
// reason given here, so a command added to the dispatch is one or the other
// on purpose.
func TestEveryCommandIsAControlOrStaysAtNormalOnPurpose(t *testing.T) {
	// Arrange
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	ast.Inspect(file, func(node ast.Node) bool {
		function, isFunction := node.(*ast.FuncDecl)
		if !isFunction || function.Name.Name != "runWithRuntime" {
			return true
		}
		ast.Inspect(function, func(node ast.Node) bool {
			dispatch, isSwitch := node.(*ast.SwitchStmt)
			if !isSwitch || dispatch.Tag == nil {
				return true
			}
			for _, clause := range dispatch.Body.List {
				for _, label := range clause.(*ast.CaseClause).List {
					if literal, isLiteral := label.(*ast.BasicLit); isLiteral && literal.Kind == token.STRING {
						command, err := strconv.Unquote(literal.Value)
						if err != nil {
							t.Fatal(err)
						}
						commands = append(commands, command)
					}
				}
			}
			return false
		})
		return false
	})
	// A reading that stops seeing the dispatch would pass every command.
	if len(commands) < 50 {
		t.Fatalf("read %d commands from the dispatch in main.go; the test is not reading it", len(commands))
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
			t.Errorf("control %q is no command of the dispatch", name)
		}
	}
	for name := range staysAtNormal {
		if !slices.Contains(commands, name) {
			t.Errorf("%q is left at normal but is no command of the dispatch", name)
		}
	}
}

// A control a goblin or a gate agent starts is not raised, whether or not it
// goes on to say anything: a hook leaves a goblin's session without a word.
func TestAControlAGoblinOrAGateAgentStartsIsNotRaised(t *testing.T) {
	if usual := processClass(t); usual != windows.NORMAL_PRIORITY_CLASS {
		t.Skipf("this test runs at priority class %#x, so it cannot tell a raise from the usual one", usual)
	}
	if !maps.EqualFunc(controls, controlRuns, func(bool, []string) bool { return true }) {
		t.Fatalf("the controls are %v and the tests run %v: every control needs a run in controlRuns", slices.Sorted(maps.Keys(controls)), slices.Sorted(maps.Keys(controlRuns)))
	}
	for who, variable := range map[string][2]string{
		"a goblin":     {harness.RoleVariable, harness.RoleGoblin},
		"a gate agent": {gateAgentVariable, "1"},
	} {
		for name := range controls {
			t.Run(who+"/"+name, func(t *testing.T) {
				// Arrange
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
