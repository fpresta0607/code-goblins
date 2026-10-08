package cleanup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// releases records each release cleanup asks for, and whether the task's
// record was already retired when it asked.
type releases struct {
	stateDir string
	tasks    []string
	retired  []bool
	lines    []string
	err      error
}

func (r *releases) release(_ context.Context, task string) ([]string, error) {
	r.tasks = append(r.tasks, task)
	_, err := state.ReadTaskMeta(r.stateDir, task)
	r.retired = append(r.retired, err != nil)
	return r.lines, r.err
}

// A finished task's cleanup releases the local services it held once its
// record is retired, so the last holder's cleanup stops the stack.
func TestCleanupReleasesTheLocalServicesTheTaskHeld(t *testing.T) {
	fixture := newCleanupFixture(t)
	released := &releases{stateDir: fixture.stateDir, lines: []string{"g1 was the last to hold them: stopped PrecisionDocs-AI's services"}}
	fixture.service.ReleaseServices = released.release

	result, err := fixture.service.Cleanup(context.Background(), "g1")
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(released.tasks) != 1 || released.tasks[0] != "g1" || !released.retired[0] {
		t.Fatalf("releases = %v retired = %v, want one release of g1 after its record was retired", released.tasks, released.retired)
	}
	if !strings.Contains(result.Output, "\ng1 was the last to hold them: stopped PrecisionDocs-AI's services") {
		t.Errorf("output = %q, want the release line", result.Output)
	}
}

// A release that fails does not undo a finished cleanup: the janitor stops a
// stack no live task holds, and the output says so.
func TestCleanupLeavesAFailedReleaseToTheJanitor(t *testing.T) {
	fixture := newCleanupFixture(t)
	released := &releases{stateDir: fixture.stateDir, err: errors.New("docker compose down exited 1")}
	fixture.service.ReleaseServices = released.release

	result, err := fixture.service.Cleanup(context.Background(), "g1")
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	want := "\nwarning: its local services were not released (docker compose down exited 1), so the janitor stops them once no live task holds them"
	if !strings.Contains(result.Output, want) {
		t.Errorf("output = %q, want %q", result.Output, want)
	}
}

// Force archive retires the record too, so it releases the same way.
func TestForceArchiveReleasesTheLocalServicesTheTaskHeld(t *testing.T) {
	fixture := newCleanupFixture(t)
	fixture.git.top = fixture.project
	fixture.service.ForceArchive = true
	released := &releases{stateDir: fixture.stateDir, lines: []string{"g1 released PrecisionDocs-AI's services, which other-task still holds"}}
	fixture.service.ReleaseServices = released.release

	result, err := fixture.service.Cleanup(context.Background(), "g1")
	if err != nil {
		t.Fatalf("force archive: %v", err)
	}
	if len(released.tasks) != 1 || !released.retired[0] {
		t.Fatalf("releases = %v retired = %v, want one release after the record was retired", released.tasks, released.retired)
	}
	if !strings.Contains(result.Output, "\ng1 released PrecisionDocs-AI's services, which other-task still holds") {
		t.Errorf("output = %q, want the release line", result.Output)
	}
}

// A refused cleanup keeps the task, so it keeps its hold too.
func TestARefusedCleanupReleasesNothing(t *testing.T) {
	fixture := newCleanupFixture(t)
	fixture.runner.tabCloseErr = true
	released := &releases{stateDir: fixture.stateDir}
	fixture.service.ReleaseServices = released.release

	if _, err := fixture.service.Cleanup(context.Background(), "g1"); err == nil {
		t.Fatal("Cleanup succeeded with a failing tab close")
	}
	if len(released.tasks) != 0 {
		t.Errorf("releases = %v, want none for a refused cleanup", released.tasks)
	}
}
