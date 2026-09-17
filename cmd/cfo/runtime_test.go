package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/runtime"
)

func runtimeInventory() runtime.Inventory {
	return runtime.Inventory{
		Tasks:   []runtime.Task{{ID: "live", Project: `C:\dev\proj`, Worktree: `C:\dev\proj\.worktrees\gb-live`}},
		Retired: map[string]bool{"dead": true},
		Containers: []runtime.Container{
			{Name: "bench-db", State: "running", Stack: "gb-live", WorkDir: `C:\dev\proj\.worktrees\gb-live`},
		},
		Listeners: []runtime.Listener{
			{Port: 3000, PID: 42, Process: "node.exe", WorkDir: `C:\dev\proj\.worktrees\gb-dead`},
		},
		Present: map[string]bool{},
	}
}

func TestRunRuntimeRendersJSONAndMarkdownFromOneInventory(t *testing.T) {
	homeRoot := t.TempDir()
	t.Setenv("CFO_HOME", homeRoot)
	deps := defaultCommandRuntime()
	calls := 0
	deps.localRuntime = func(_ context.Context, h home.Home) (runtime.Inventory, error) {
		calls++
		if h.Root != homeRoot {
			t.Errorf("home = %+v, want CFO_HOME %q", h, homeRoot)
		}
		return runtimeInventory(), nil
	}

	var jsonOut, jsonErr bytes.Buffer
	if exit := runWithRuntime([]string{"runtime", "--json"}, &jsonOut, &jsonErr, deps); exit != 0 {
		t.Fatalf("json exit = %d, want 0; stderr=%s", exit, jsonErr.String())
	}
	if !strings.Contains(jsonOut.String(), `"schema":"runtime-report.v1"`) || jsonErr.Len() != 0 {
		t.Errorf("JSON stdout=%q stderr=%q, want typed JSON only", jsonOut.String(), jsonErr.String())
	}

	var markdownOut, markdownErr bytes.Buffer
	if exit := runWithRuntime([]string{"runtime"}, &markdownOut, &markdownErr, deps); exit != 0 {
		t.Fatalf("markdown exit = %d, want 0; stderr=%s", exit, markdownErr.String())
	}
	out := markdownOut.String()
	if !strings.Contains(out, "# Local Runtime") || markdownErr.Len() != 0 {
		t.Errorf("Markdown stdout=%q stderr=%q, want Markdown only", out, markdownErr.String())
	}
	// The two answers the command exists to give must survive the wiring.
	if !strings.Contains(out, "goblin live") {
		t.Errorf("output does not attribute the live goblin's stack:\n%s", out)
	}
	if !strings.Contains(out, "safe") || !strings.Contains(out, "cfo reap") {
		t.Errorf("output does not report the stale server or hand retiring it to cfo reap:\n%s", out)
	}
	if calls != 2 {
		t.Errorf("collect calls = %d, want one per rendered command", calls)
	}
}

func TestRunRuntimeRejectsUnknownFlagsAndArgumentsWithoutCollecting(t *testing.T) {
	deps := defaultCommandRuntime()
	called := false
	deps.localRuntime = func(context.Context, home.Home) (runtime.Inventory, error) {
		called = true
		return runtime.Inventory{}, nil
	}
	for _, args := range [][]string{{"runtime", "--nope"}, {"runtime", "extra"}} {
		var out, errOut bytes.Buffer
		if exit := runWithRuntime(args, &out, &errOut, deps); exit != 2 {
			t.Errorf("%v exit = %d, want 2", args, exit)
		}
		if out.Len() != 0 {
			t.Errorf("%v wrote %q to stdout, want nothing", args, out.String())
		}
	}
	if called {
		t.Error("the machine was read despite a rejected command line")
	}
}

func TestRunRuntimeReportsACollectionFailureOnStderr(t *testing.T) {
	t.Setenv("CFO_HOME", t.TempDir())
	deps := defaultCommandRuntime()
	deps.localRuntime = func(context.Context, home.Home) (runtime.Inventory, error) {
		return runtime.Inventory{}, errors.New("read state directory: denied")
	}
	var out, errOut bytes.Buffer
	if exit := runWithRuntime([]string{"runtime"}, &out, &errOut, deps); exit != 1 {
		t.Fatalf("exit = %d, want 1", exit)
	}
	if !strings.Contains(errOut.String(), "denied") || out.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want the failure on stderr alone", out.String(), errOut.String())
	}
}

// The command must appear in the usage text, or a CFO that does not already
// know about it never finds it.
func TestUsageNamesTheRuntimeCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	runWithRuntime(nil, &out, &errOut, defaultCommandRuntime())
	if !strings.Contains(errOut.String(), "cfo runtime") {
		t.Errorf("usage does not name the command:\n%s", errOut.String())
	}
}
