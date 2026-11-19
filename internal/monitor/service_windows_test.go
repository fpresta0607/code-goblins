package monitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestScanFindsCaseInsensitiveMetadataExtension(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("Foo")
	writeTask(t, stateDir, meta)
	renameMetadataExtension(t, stateDir, meta.ID, "META")
	probe := &fakeProber{samples: map[string]EndpointSample{"Foo": sampleFor(meta, herdr.BusyWorking, "first")}}

	result, err := testService(stateDir, probe, &now).Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Observations) != 1 || result.Observations[0].TaskID != "Foo" {
		t.Fatalf("observations = %+v, want Foo from Foo.META", result.Observations)
	}
	if got := probe.calls; len(got) != 1 || got[0] != "Foo" {
		t.Errorf("probe calls = %v, want Foo", got)
	}
}

func renameMetadataExtension(t *testing.T, stateDir, id, extension string) {
	t.Helper()
	path := filepath.Join(stateDir, id+".meta")
	temporary := filepath.Join(stateDir, id+".metadata-swap")
	if err := os.Rename(path, temporary); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, filepath.Join(stateDir, id+"."+extension)); err != nil {
		t.Fatal(err)
	}
}

func TestScanDoesNotPublishWhenObservationPersistenceFails(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
	service := testService(stateDir, probe, &now)

	monitorDir := filepath.Join(stateDir, "monitor")
	if err := os.MkdirAll(monitorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(monitorDir, "tasks"), []byte("blocks task records"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Scan(context.Background()); err == nil {
		t.Fatal("Scan succeeded despite an unwritable observation path")
	}
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("wake records = %+v, want none before observation persistence", records)
	}
}
