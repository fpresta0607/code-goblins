package lifecycle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type missingPaneRunner struct{ code string }

func (runner missingPaneRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "herdr" {
		return execx.Result{}, fmt.Errorf("unexpected command: %s", request.Name)
	}
	if strings.Contains(strings.Join(request.Args, " "), "pane get") {
		return execx.Result{ExitCode: 1, Stdout: []byte(`{"error":{"code":"` + runner.code + `"}}`)}, nil
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("pane process info unavailable")}, nil
}

func TestTaskResourcesContinuesAfterConfirmedLegacyPaneAbsence(t *testing.T) {
	for _, code := range []string{"pane_not_found", "server_unavailable"} {
		t.Run(code, func(t *testing.T) {
			directory := t.TempDir()
			stateDir := filepath.Join(directory, "state")
			meta := state.TaskMeta{ID: "fixture", Backend: "herdr", HerdrSession: "missing-lifecycle-fixture", HerdrPaneID: "w1:p2", Project: directory, Worktree: filepath.Join(directory, ".worktrees", "gb-fixture"), TaskTmp: filepath.Join(stateDir, "tasktmp", "fixture")}
			resources, err := TaskResources(t.Context(), stateDir, meta, pipeline.Reader{Root: filepath.Join(directory, "gate"), Commands: missingPaneRunner{code: code}})
			if (err == nil) != (code == "pane_not_found") {
				t.Fatalf("pane evidence=%s resources=%+v error=%v", code, resources, err)
			}
			if len(resources.Hosts) != 0 || len(resources.Directories) < 2 {
				t.Fatalf("missing pane lost detached process directories: %+v", resources)
			}
		})
	}
}

type gateRunRunner struct{ status string }

func (runner gateRunRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	switch request.Name {
	case "git":
		return execx.Result{Stdout: []byte("gb-fixture\n")}, nil
	case "sqlite3":
		row := fmt.Sprintf(`[{"id":"run-1","repo_id":"repo-1","branch":"gb-fixture","status":%q,"head":"%s","intent":"saved intent","worktree":""}]`, runner.status, strings.Repeat("a", 40))
		return execx.Result{Stdout: []byte(row)}, nil
	}
	return execx.Result{}, fmt.Errorf("unexpected command: %s", request.Name)
}

func TestTaskResourcesInterruptsOnlyAnOpenGateRun(t *testing.T) {
	for _, status := range []string{"completed", "failed", "cancelled", "running"} {
		t.Run(status, func(t *testing.T) {
			directory := t.TempDir()
			stateDir := filepath.Join(directory, "state")
			gateRoot := filepath.Join(directory, "gate")
			if err := os.MkdirAll(gateRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(gateRoot, "state.sqlite"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			meta := state.TaskMeta{ID: "fixture", Backend: "native", Project: directory, Worktree: filepath.Join(directory, ".worktrees", "gb-fixture"), TaskTmp: filepath.Join(stateDir, "tasktmp", "fixture")}
			resources, err := TaskResources(t.Context(), stateDir, meta, pipeline.Reader{Root: gateRoot, Commands: gateRunRunner{status: status}})
			if err != nil {
				t.Fatal(err)
			}
			if isInterrupted := resources.Gate.ID != ""; isInterrupted != (status == "running") {
				t.Fatalf("latest %s run interrupted=%v: %+v", status, isInterrupted, resources.Gate)
			}
		})
	}
}
