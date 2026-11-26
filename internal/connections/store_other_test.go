//go:build !windows

package connections

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A goblin whose checkout's folder differs only in case from a project's
// manifest folder is listed under its own project, and each is checked once,
// as Linux and macOS read the two names as two folders.
func TestAGoblinWhoseCheckoutDiffersInCaseIsListedUnderItsOwnProject(t *testing.T) {
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
	if !slices.Equal(names, []string{"Acme", "acme", "globex"}) {
		t.Fatalf("projects = %v, want [Acme acme globex]: the checkout Acme is not the manifest acme", names)
	}
	for _, key := range []string{"project:Acme", "project:acme"} {
		if got := f.counters.get(key); got != 1 {
			t.Errorf("%s checked %d times, want once", key, got)
		}
	}
}
