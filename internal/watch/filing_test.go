package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/layout"
)

// laidOutHome is a home whose data folder cfo install laid out, so the
// watcher may file it.
func laidOutHome(t *testing.T) home.Home {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	if err := os.MkdirAll(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := layout.Ensure(h.Data); err != nil {
		t.Fatal(err)
	}
	return h
}

// finishTask leaves what a cleaned-up task leaves: its data folder and a
// status log with no metadata beside it.
func finishTask(t *testing.T, h home.Home, id string) {
	t.Helper()
	for path, content := range map[string]string{
		filepath.Join(h.Data, id, "brief.md"): "# Brief " + id + "\n",
		filepath.Join(h.State, id+".status"):  "done: PR https://github.com/o/r/pull/1\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func filed(t *testing.T, h home.Home, id string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(h.Data, "archive", "finished", id, "brief.md"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

// Filing rides the watcher's cycle, as the orphan sweep does, so finished
// work is filed without anyone asking.
func TestRunFilesFinishedWork(t *testing.T) {
	h := laidOutHome(t)
	finishTask(t, h, "g1")
	cfg := reapConfig(h.State, orphanFleet())
	cfg.Home = h
	cfg.FileEvery = time.Hour

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !filed(t, h, "g1") {
		t.Error("a watcher cycle did not file the finished task g1")
	}
}

func TestFileDataRunsAtMostOncePerInterval(t *testing.T) {
	h := laidOutHome(t)
	finishTask(t, h, "g1")
	cfg := baseConfig(h.State)
	cfg.Home = h
	cfg.FileEvery = time.Hour
	var last time.Time

	fileData(cfg, &last)
	finishTask(t, h, "g2")
	fileData(cfg, &last)

	if !filed(t, h, "g1") || filed(t, h, "g2") {
		t.Fatalf("filed g1 %v, g2 %v; want only the first pass to have run", filed(t, h, "g1"), filed(t, h, "g2"))
	}

	last = last.Add(-cfg.FileEvery)
	fileData(cfg, &last)

	if !filed(t, h, "g2") {
		t.Error("the pass after the interval did not file g2")
	}
}
