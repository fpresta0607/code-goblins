package connections

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A goblin whose checkout's folder differs only in case from its project's
// manifest folder is listed under that one project, which is checked once,
// as Windows reads both names as one folder.
func TestAGoblinWhoseCheckoutDiffersInCaseIsListedUnderItsManifestProject(t *testing.T) {
	// Arrange
	f := newSnapshotFixture(t)
	meta := f.goblin("acme", "acme-one")
	meta.Project = filepath.Join(filepath.Dir(meta.Project), "Acme")
	f.running = []state.TaskMeta{meta}

	// Act
	f.tick()

	// Assert
	var names []string
	for _, project := range f.snapshots.Read().Projects {
		names = append(names, project.Name)
	}
	if !slices.Equal(names, []string{"acme", "globex"}) {
		t.Fatalf("projects = %v, want [acme globex]: the checkout Acme is the manifest acme", names)
	}
	if got := f.counters.get("project:acme") + f.counters.get("project:Acme"); got != 1 {
		t.Errorf("acme checked %d times, want once", got)
	}
}
