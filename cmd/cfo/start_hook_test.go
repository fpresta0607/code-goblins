package main

import (
	"bytes"
	"maps"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// namespaceLine is the line setuptools writes into the -nspkg.pth file of
// zope.interface 6.4, which declares the namespace package zope.
const namespaceLine = "import sys, types, os;p = os.path.join(sys._getframe(1).f_locals['sitedir'], *('zope',));importlib = __import__('importlib.util');__import__('importlib.machinery');m = sys.modules.setdefault('zope', importlib.util.module_from_spec(importlib.machinery.PathFinder.find_spec('zope', [os.path.dirname(p)])));m = m or sys.modules.setdefault('zope', types.ModuleType('zope'));mp = (m or []) and m.__dict__.setdefault('__path__',[]);(p not in mp) and mp.append(p)\n"

// plantNamespaceHook makes worktree's environment the one a locked install of
// zope.interface 6.4 leaves, its -nspkg.pth file holding content, and returns
// that file.
func plantNamespaceHook(t *testing.T, worktree, content string) string {
	t.Helper()
	site := filepath.Join(worktree, ".venv", "Lib", "site-packages")
	planted := filepath.Join(site, "zope.interface-6.4-py3.12-nspkg.pth")
	writeTestFile(t, filepath.Join(worktree, ".venv", "pyvenv.cfg"), "uv = 0.9.30\nversion_info = 3.12.10\n")
	writeTestFile(t, planted, content)
	writeTestFile(t, filepath.Join(site, "zope.interface-6.4.dist-info", "RECORD"), "zope.interface-6.4-py3.12-nspkg.pth,sha256=AAAA,1\nzope.interface-6.4.dist-info/RECORD,,\n")
	return planted
}

// recordGoblin records task as a goblin of the project checked out at
// project, working in worktree, and runs the goblin hooks as its terminal
// names them.
func recordGoblin(t *testing.T, stateDir, task, project, worktree string) {
	t.Helper()
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: task, Project: project, Worktree: worktree}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_TASK_ID", task)
	t.Setenv("CLAUDE_PROJECT_DIR", worktree)
}

// Since #1296 a goblin's seal stops it while its environment holds a start
// hook no known package ships, which stopped every goblin of a project that
// locks a package declaring a namespace package, at every spawn: setuptools
// writes that package a -nspkg.pth file. The CFO accepts such a file once he
// judged it safe, for its project or for the fleet, and every later goblin's
// seal lets that file through while it still stops one whose content
// changed (#1302).
func TestTheCFOAcceptsAStartHookHeJudgedSafeAndAChangedOneIsStillStopped(t *testing.T) {
	for _, test := range []struct {
		name                 string
		flags                []string
		isOtherProjectPassed bool
		whose                string
	}{
		{name: "for its project", whose: "for shop's goblins"},
		{name: "for the fleet", flags: []string{"--fleet"}, isOtherProjectPassed: true, whose: "for every project's goblins"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := goblinHookHome(t)
			projects := t.TempDir()
			shop, garden := filepath.Join(projects, "shop"), filepath.Join(projects, "garden")
			first := t.TempDir()
			recordGoblin(t, stateDir, "shop-1", shop, first)
			planted := plantNamespaceHook(t, first, namespaceLine)
			var stdout, stderr, output, refusal bytes.Buffer
			stopped := runHook("goblin-seal", shellCall(t, "uv sync --locked"), &stdout, &stderr)

			// Act
			t.Setenv(harness.RoleVariable, "")
			accepted := runWithRuntime(append([]string{"start-hook", "accept", "shop-1", planted}, test.flags...), &output, &refusal, commandRuntime{resolveHome: home.Resolve})
			t.Setenv(harness.RoleVariable, harness.RoleGoblin)
			exits := map[string]int{"the goblin it stopped": runHook("goblin-seal", shellCall(t, "uv sync --locked"), &stdout, &stderr)}
			for _, later := range []struct{ name, task, project, content string }{
				{name: "a later goblin of its project", task: "shop-2", project: shop, content: namespaceLine},
				{name: "a goblin of another project", task: "garden-1", project: garden, content: namespaceLine},
				{name: "a later goblin of its project whose file changed", task: "shop-3", project: shop, content: namespaceLine + "import os; os.system('x')\n"},
			} {
				worktree := t.TempDir()
				recordGoblin(t, stateDir, later.task, later.project, worktree)
				plantNamespaceHook(t, worktree, later.content)
				exits[later.name] = runHook("goblin-seal", shellCall(t, "uv sync --locked"), &stdout, &stderr)
			}

			// Assert
			if stopped != 2 {
				t.Fatalf("seal before the CFO accepted it = %d, want the goblin stopped", stopped)
			}
			if accepted != 0 || !strings.Contains(output.String(), "Accepted zope.interface-6.4-py3.12-nspkg.pth from zope.interface-6.4 "+test.whose) {
				t.Fatalf("accept = %d, stdout = %q, stderr = %q, want the file accepted %s", accepted, output.String(), refusal.String(), test.whose)
			}
			want := map[string]int{"the goblin it stopped": 0, "a later goblin of its project": 0, "a goblin of another project": 2, "a later goblin of its project whose file changed": 2}
			if test.isOtherProjectPassed {
				want["a goblin of another project"] = 0
			}
			if !maps.Equal(exits, want) {
				t.Fatalf("seal exits = %v, want %v (0 passed, 2 stopped)", exits, want)
			}
		})
	}
}

// A list of accepted start hooks the seal cannot read lets none through, an
// accepted one included, so the seal never fails open.
func TestGoblinSealStopsAnAcceptedStartHookWhileTheAcceptedListCannotBeRead(t *testing.T) {
	// Arrange
	stateDir := goblinHookHome(t)
	worktree := t.TempDir()
	recordGoblin(t, stateDir, "shop-1", filepath.Join(t.TempDir(), "shop"), worktree)
	planted := plantNamespaceHook(t, worktree, namespaceLine)
	t.Setenv(harness.RoleVariable, "")
	var stdout, stderr bytes.Buffer
	if exit := runWithRuntime([]string{"start-hook", "accept", "shop-1", planted}, &stdout, &stderr, commandRuntime{resolveHome: home.Resolve}); exit != 0 {
		t.Fatalf("accept = %d, stderr = %q, want the file accepted", exit, stderr.String())
	}
	t.Setenv(harness.RoleVariable, harness.RoleGoblin)
	writeTestFile(t, filepath.Join(stateDir, "accepted-start-hooks.json"), "{accepted")
	stderr.Reset()

	// Act
	exit := runHook("goblin-seal", shellCall(t, "uv sync --locked"), &stdout, &stderr)

	// Assert
	if exit != 2 || !strings.Contains(stderr.String(), "the start hooks the CFO accepted could not be read") {
		t.Fatalf("seal = %d, stderr = %q, want the goblin stopped", exit, stderr.String())
	}
}

// A start hook is the CFO's to accept, and only one a goblin's seal stops
// with the distribution that installs it named: the command refuses a goblin
// and a gate agent, a task it has no record of, a file the seal does not
// stop, one no installed distribution names and a folder, which has no one
// content, and accepts nothing then.
func TestStartHookAcceptRefusesWhatItCannotAccept(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(t *testing.T, worktree string)
		args    func(planted, worktree string) []string
		exit    int
		want    string
	}{
		{name: "a goblin", arrange: func(t *testing.T, worktree string) { t.Setenv(harness.RoleVariable, harness.RoleGoblin) }, args: func(planted, worktree string) []string { return []string{"accept", "shop-1", planted} }, exit: 2, want: "never a goblin's or a gate agent's"},
		{name: "a gate agent", arrange: func(t *testing.T, worktree string) { t.Setenv(gateAgentVariable, "1") }, args: func(planted, worktree string) []string { return []string{"accept", "shop-1", planted} }, exit: 2, want: "never a goblin's or a gate agent's"},
		{name: "a task it has no record of", args: func(planted, worktree string) []string { return []string{"accept", "shop-9", planted} }, exit: 1, want: "shop-9"},
		{name: "a file the seal does not stop", args: func(planted, worktree string) []string {
			return []string{"accept", "shop-1", filepath.Join(worktree, ".venv", "pyvenv.cfg")}
		}, exit: 1, want: "is no start hook shop-1's seal stops"},
		{name: "a start hook no installed distribution names", arrange: func(t *testing.T, worktree string) {
			writeTestFile(t, filepath.Join(worktree, ".venv", "Lib", "site-packages", "orphan.pth"), "import os; os.system('x')\n")
		}, args: func(planted, worktree string) []string {
			return []string{"accept", "shop-1", filepath.Join(worktree, ".venv", "Lib", "site-packages", "orphan.pth")}
		}, exit: 1, want: "no installed distribution's RECORD names"},
		{name: "a folder", arrange: func(t *testing.T, worktree string) {
			site := filepath.Join(worktree, ".venv", "Lib", "site-packages")
			writeTestFile(t, filepath.Join(site, "usercustomize", "__init__.py"), "import os\n")
			writeTestFile(t, filepath.Join(site, "tune-1.0.dist-info", "RECORD"), "usercustomize/__init__.py,sha256=AAAA,1\n")
		}, args: func(planted, worktree string) []string {
			return []string{"accept", "shop-1", filepath.Join(worktree, ".venv", "Lib", "site-packages", "usercustomize")}
		}, exit: 1, want: "is a folder"},
		{name: "no file named", args: func(planted, worktree string) []string { return []string{"accept", "shop-1"} }, exit: 2, want: "usage: cfo start-hook accept"},
		{name: "another verb", args: func(planted, worktree string) []string { return []string{"list"} }, exit: 2, want: "usage: cfo start-hook accept"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := goblinHookHome(t)
			worktree := t.TempDir()
			recordGoblin(t, stateDir, "shop-1", filepath.Join(t.TempDir(), "shop"), worktree)
			planted := plantNamespaceHook(t, worktree, namespaceLine)
			t.Setenv(harness.RoleVariable, "")
			if test.arrange != nil {
				test.arrange(t, worktree)
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime(append([]string{"start-hook"}, test.args(planted, worktree)...), &stdout, &stderr, commandRuntime{resolveHome: home.Resolve})

			// Assert
			if exit != test.exit || !strings.Contains(stderr.String(), test.want) || stdout.Len() != 0 {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q, want %d and %q", exit, stdout.String(), stderr.String(), test.exit, test.want)
			}
			t.Setenv(harness.RoleVariable, harness.RoleGoblin)
			t.Setenv(gateAgentVariable, "")
			if seal := runHook("goblin-seal", shellCall(t, "uv sync --locked"), &stdout, &stderr); seal != 2 {
				t.Fatalf("seal after the refusal = %d, want the goblin still stopped", seal)
			}
		})
	}
}
