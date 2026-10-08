package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A goblin whose brief needs its project's local services is told in its
// first instruction the one command that holds them, and the spawn says so.
func TestSpawnTellsAGoblinWhoseBriefNeedsServicesTheCommandToRun(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	closeTerminalAtEnd(t, f.stateDir, f.request.ID)
	writeFile(t, f.brief, "Delivery contract: mode=no-mistakes\nDo the work.\n\n## Delivery\n\nkind: ship\nservices: needed\n")
	manifest := filepath.Join(f.dataDir, "projects", "primary", "services.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, manifest, `{"project":"primary","compose":"compose.yml","services":["backend"],"memory_estimate_gb":2}`)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	result, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	submitted := named(f.events(t), "submitted")
	want := "run " + exe + " services up " + f.project + " --task task-7,"
	if len(submitted) != 1 || !strings.Contains(delivered(t, submitted[0].Text), want) {
		t.Fatalf("submitted = %+v; want the goblin told %q", submitted, want)
	}
	if !strings.Contains(result.Output, "\nservices: the goblin holds primary's local services for its full-stack check with cfo services up, and releases them with cfo services down") {
		t.Errorf("output = %q; want the services named as the goblin's", result.Output)
	}
}
