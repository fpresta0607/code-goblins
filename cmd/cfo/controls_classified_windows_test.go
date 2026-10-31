package main

import (
	"bytes"
	"io"
	"maps"
	"slices"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/priority/prioritytest"
)

var staysAtNormal = map[string]string{
	"host":              "a terminal's host raises itself, whoever starts it (host.Run)",
	"install":           "copies programs and writes the user's settings",
	"uninstall":         "removes an installation",
	"hooks":             "writes a harness's hook files",
	"codex-guard":       "the codex a goblin's armed codex guard runs, in the goblin's own terminal, where nothing is raised; it prints one line and starts nothing",
	"version":           "prints one line",
	"help":              "prints embedded command and procedure references without opening a home",
	"doctor":            "runs every tool's own version check",
	"dictation":         "downloads and checks the speech engine",
	"dev-drive":         "reads and sets up drives",
	"defender":          "reads Microsoft Defender's own records, which takes seconds",
	"allowance-floor":   "sets one number nothing waits on",
	"start-hook":        "records one accepted file nothing waits on",
	"home":              "hashes and moves a whole home",
	"memory":            "validates and hashes memory sources without controlling a running task",
	"pipeline":          "starts and answers gate runs, which are work",
	"afk":               "a switch and a log nothing waits on",
	"auth":              "runs each service's own sign-in check",
	"connection-repair": "runs a sign-in",
	"remote":            "runs Tailscale setup and status checks, which can wait for connection or HTTPS consent",
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
	"issue":             "reads and files issues on GitHub",
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
	"ci-runner":         "waits on a desktop CI job, which is work",
}

func TestNonControlsStayAtNormalPriority(t *testing.T) {
	for command := range staysAtNormal {
		t.Run(command, func(t *testing.T) {
			prioritytest.FromNormal(t)
			t.Setenv(harness.RoleVariable, "")
			t.Setenv(gateAgentVariable, "")

			restore := aboveTheWork([]string{command})
			during := processClass(t)
			restore()

			if during != windows.NORMAL_PRIORITY_CLASS || processClass(t) != windows.NORMAL_PRIORITY_CLASS {
				t.Fatalf("non-control %s changed process priority to %#x", command, during)
			}
		})
	}
}

func TestMemoryCommandsRunAtNormalPriority(t *testing.T) {
	for _, command := range memoryReadOnlyCommands(t) {
		t.Run(command.name, func(t *testing.T) {
			prioritytest.FromNormal(t)
			t.Setenv(harness.RoleVariable, "")
			t.Setenv(gateAgentVariable, "")
			inAnotherAgentsSession(t)
			said := &classSeen{t: t}
			var diagnostics bytes.Buffer

			exit := runWithRuntime(command.args, said, io.MultiWriter(said, &diagnostics), testCommandRuntime(t))

			if exit != command.exit || len(said.classes) == 0 {
				t.Fatalf("exit=%d, want %d; output samples=%d; %s", exit, command.exit, len(said.classes), diagnostics.String())
			}
			for _, class := range said.classes {
				if class != windows.NORMAL_PRIORITY_CLASS {
					t.Fatalf("memory command ran at priority %#x", class)
				}
			}
			if processClass(t) != windows.NORMAL_PRIORITY_CLASS {
				t.Fatal("memory command changed process priority")
			}
		})
	}
}

func TestEveryControlHasAnExecutablePriorityCheck(t *testing.T) {
	if !maps.EqualFunc(controls, controlRuns, func(bool, controlRun) bool { return true }) {
		t.Errorf("the controls are %v and the tests run %v", slices.Sorted(maps.Keys(controls)), slices.Sorted(maps.Keys(controlRuns)))
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
