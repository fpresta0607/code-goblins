package lifecycle

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host/hosttest"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestMain(m *testing.M) {
	hosttest.RunStreamChild()
	os.Exit(m.Run())
}

// A goblin on stream-json pipes holds its stream session's host as a
// goblin in a terminal holds its terminal's, so stopping it ends that host
// with everything else it holds (#2374).
func TestTaskResourcesHoldAStreamGoblinsHost(t *testing.T) {
	// Arrange
	directory := t.TempDir()
	stateDir := filepath.Join(directory, "state")
	meta := state.TaskMeta{ID: "fixture", Backend: "native", Project: directory, Worktree: filepath.Join(directory, ".worktrees", "gb-fixture"), TaskTmp: filepath.Join(stateDir, "tasktmp", "fixture")}
	record := hosttest.RunStream(t, stateDir, meta.ID, false)

	// Act
	resources, err := TaskResources(t.Context(), home.Home{Root: filepath.Dir(stateDir), State: stateDir}, meta, pipeline.Reader{Root: filepath.Join(directory, "gate")})

	// Assert
	if err != nil {
		t.Fatalf("TaskResources: %v", err)
	}
	if !slices.ContainsFunc(resources.Hosts, func(held Identity) bool { return held.PID == record.HostPID }) {
		t.Errorf("hosts = %+v; want the stream session's host, pid %d", resources.Hosts, record.HostPID)
	}
}
