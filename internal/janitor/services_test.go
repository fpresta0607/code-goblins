package janitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// servicesConfig is a sweep of an empty home whose local services stop as
// stop says.
func servicesConfig(t *testing.T, stop func(context.Context) ([]string, error)) Config {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	for _, dir := range []string{h.State, h.Data, h.Worktrees(), h.Scratch(), h.Bin(), h.Caches()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Config{Home: h, Commands: execx.OSRunner{}, Settings: fleetconfig.Defaults(), Now: time.Now(), SelfPID: os.Getpid(), StopServices: stop}
}

// The sweep stops the local services stacks no live task holds, and records
// each it stopped beside what else it removed.
func TestSweepStopsTheLocalServicesNoLiveTaskHolds(t *testing.T) {
	stopped := "stopped PrecisionDocs-AI's services, and stopped the Docker engine cfo started: no live task held them (pd-a ended)"
	swept := 0
	record := Sweep(context.Background(), servicesConfig(t, func(context.Context) ([]string, error) {
		swept++
		return []string{stopped}, nil
	}))
	if swept != 1 {
		t.Fatalf("the services sweep ran %d times, want once", swept)
	}
	if !slices.Contains(record.Removed, Item{Kind: "services", Detail: stopped}) {
		t.Errorf("removed = %+v, want the stopped stack recorded", record.Removed)
	}
}

// A services sweep that fails is a note: the next sweep looks again, and the
// others still run.
func TestSweepNotesLocalServicesItCouldNotStop(t *testing.T) {
	record := Sweep(context.Background(), servicesConfig(t, func(context.Context) ([]string, error) {
		return nil, errors.New("services: docker compose down exited 1")
	}))
	want := "the local services no live task holds could not be stopped, so the next sweep tries again: services: docker compose down exited 1"
	if !slices.Contains(record.Notes, want) {
		t.Errorf("notes = %q, want %q", record.Notes, want)
	}
	if record.Disk.Free == 0 {
		t.Errorf("disk = %+v, want the sweep to go on past the failure", record.Disk)
	}
}
