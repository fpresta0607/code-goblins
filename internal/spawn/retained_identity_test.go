package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchivedTaskIDCannotReuseBrowserCustody(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.brief, "Delivery contract: mode=local-only\nSynthetic task.\n")
	if err := os.MkdirAll(filepath.Join(f.stateDir, "tasks", "task-7"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Spawn(context.Background(), Request{ID: "task-7", Project: f.project, BriefPath: f.brief, Harness: "claude", Mode: "local-only", Kind: "ship"})
	if err == nil || !strings.Contains(err.Error(), "retained context or browser custody") {
		t.Fatalf("retired task id reused: %v", err)
	}
	if len(f.events) != 0 {
		t.Fatalf("allocated before ownership refusal: %v", f.events)
	}
}
