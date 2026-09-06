package state

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestScanIDs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"alpha.meta", "alpha.status",
		"beta.meta",
		"gamma.status",
		".reap.status",
		".session-start-complete",
		"notes.txt",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "tasktmp"), 0o755); err != nil {
		t.Fatal(err)
	}

	scan, err := ScanIDs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(scan.MetaIDs, []string{"alpha", "beta"}) {
		t.Fatalf("metas = %v, want [alpha beta]", scan.MetaIDs)
	}
	// gamma has no meta beside it; alpha's status is not an orphan, and the
	// reaper's own dotted log must never be reported as one.
	if !slices.Equal(scan.OrphanStatusIDs, []string{"gamma"}) {
		t.Fatalf("orphan status = %v, want [gamma]", scan.OrphanStatusIDs)
	}
}

func TestScanIDsMissingDirectoryIsEmpty(t *testing.T) {
	scan, err := ScanIDs(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("a home with no state directory returned %v", err)
	}
	if len(scan.MetaIDs) != 0 || len(scan.OrphanStatusIDs) != 0 {
		t.Fatalf("scan = %+v, want empty", scan)
	}
}
