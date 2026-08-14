package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestRunFleetRendersJSONAndMarkdownFromOneSnapshot(t *testing.T) {
	homeRoot := t.TempDir()
	t.Setenv("CFO_HOME", homeRoot)
	deps := defaultCommandRuntime()
	calls := 0
	deps.snapshot = func(_ context.Context, h home.Home) (fleet.Snapshot, error) {
		calls++
		if h.Root != homeRoot {
			t.Errorf("home = %+v, want CFO_HOME %q", h, homeRoot)
		}
		return fleet.Snapshot{Schema: "fleet-snapshot.v1", Home: h.Root, Tasks: []fleet.TaskRow{}, Backlog: fleet.BacklogRows{Queued: []fleet.BacklogRow{}, Done: []fleet.BacklogRow{}}, Secondmates: []fleet.SecondmateRow{}}, nil
	}

	var jsonOut, jsonErr bytes.Buffer
	if exit := runWithRuntime([]string{"fleet-view", "--json"}, &jsonOut, &jsonErr, deps); exit != 0 {
		t.Fatalf("json exit = %d, want 0; stderr=%s", exit, jsonErr.String())
	}
	if !strings.Contains(jsonOut.String(), `"schema":"fleet-snapshot.v1"`) || jsonErr.Len() != 0 {
		t.Errorf("JSON stdout=%q stderr=%q, want typed JSON only", jsonOut.String(), jsonErr.String())
	}

	var markdownOut, markdownErr bytes.Buffer
	if exit := runWithRuntime([]string{"fleet-view"}, &markdownOut, &markdownErr, deps); exit != 0 {
		t.Fatalf("markdown exit = %d, want 0; stderr=%s", exit, markdownErr.String())
	}
	if !strings.Contains(markdownOut.String(), "# Fleet View") || markdownErr.Len() != 0 {
		t.Errorf("Markdown stdout=%q stderr=%q, want Markdown only", markdownOut.String(), markdownErr.String())
	}
	if calls != 2 {
		t.Errorf("snapshot calls = %d, want one per rendered command", calls)
	}
}

func TestRunFleetRejectsUnknownFlagsWithoutState(t *testing.T) {
	h := testHome(t)
	deps := testCommandRuntimeForHome(h)
	called := false
	deps.snapshot = func(context.Context, home.Home) (fleet.Snapshot, error) {
		called = true
		return fleet.Snapshot{}, nil
	}

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"fleet-view", "--unknown"}, &stdout, &stderr, deps)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	if called {
		t.Fatal("unknown fleet flag invoked snapshot builder")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Errorf("stdout=%q stderr=%q, want flag diagnostic only", stdout.String(), stderr.String())
	}
}
