package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// stubDocker and stubSystem stand in for the two sources that have no test
// double anywhere else in the tree: a test cannot conjure real containers or
// real listening sockets, and one that read the live machine would assert
// whatever happened to be running that day.
type stubDocker struct {
	containers []Container
	volumes    []Volume
	err        error
}

func (s stubDocker) Containers(context.Context) ([]Container, error) { return s.containers, s.err }
func (s stubDocker) Volumes(context.Context) ([]Volume, error)       { return s.volumes, s.err }
func (s stubDocker) Usage(context.Context) (int64, int64, int64, int64, int64, error) {
	return 1, 2, 3, 4, 5, s.err
}

type stubSystem struct {
	listeners []Listener
	machine   Machine
	err       error
}

func (s stubSystem) Listeners(context.Context) ([]Listener, error) { return s.listeners, s.err }
func (s stubSystem) Machine(context.Context, string) (Machine, error) {
	return s.machine, s.err
}

// collectorHome lays out a CFO home with one live task and one archived one.
func collectorHome(t *testing.T) home.Home {
	t.Helper()
	root := t.TempDir()
	h := home.Home{
		Root:  root,
		State: filepath.Join(root, "state"),
		Data:  filepath.Join(root, "data"),
	}
	if err := os.MkdirAll(filepath.Join(h.State, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "projects", "siqsermon"), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := "backend=herdr\nproject=" + filepath.Join(root, "projects", "siqsermon") +
		"\nworktree=" + filepath.Join(root, "projects", "siqsermon", ".worktrees", "gb-live-task") + "\n"
	if err := os.WriteFile(filepath.Join(h.State, "live-task.meta"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.State, "archive", "dead-task.20260917T115954Z"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "archive", "dead-task.status.20260917T124916Z"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestCollectReadsTasksArchiveAndCheckouts(t *testing.T) {
	h := collectorHome(t)
	inv, err := Collector{Home: h, Docker: stubDocker{}, System: stubSystem{}}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(inv.Tasks) != 1 || inv.Tasks[0].ID != "live-task" {
		t.Errorf("tasks = %+v, want the one live record", inv.Tasks)
	}
	if !inv.Retired["dead-task"] {
		t.Errorf("retired = %v, want the archived task recognised", inv.Retired)
	}
	// One archived task, recorded twice (a directory and a status log), is
	// still one task.
	if len(inv.Retired) != 1 {
		t.Errorf("retired = %v, want one id from the two archive entries", inv.Retired)
	}
	var found bool
	for _, checkout := range inv.Checkouts {
		if checkout.Name == "siqsermon" {
			found = true
		}
	}
	if !found {
		t.Errorf("checkouts = %+v, want the clone under projects/", inv.Checkouts)
	}
}

// A source that cannot be read must say so. A section that renders empty
// because Docker was not running reads exactly like a machine with nothing on
// it, which is the answer that says there is room to dispatch.
func TestCollectNamesEverySourceItCouldNotRead(t *testing.T) {
	h := collectorHome(t)
	broken := errors.New("error during connect: Docker Desktop is not running")
	inv, err := Collector{
		Home:   h,
		Docker: stubDocker{err: broken},
		System: stubSystem{err: errors.New("Get-NetTCPConnection failed")},
	}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect returned an error instead of degrading: %v", err)
	}
	joined := strings.Join(inv.Notes, "\n")
	for _, want := range []string{
		"CONTAINERS UNREADABLE",
		"VOLUMES UNREADABLE",
		"DOCKER USAGE UNREADABLE",
		"SERVERS UNREADABLE",
		"HEADROOM UNREADABLE",
		"Docker Desktop is not running",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes = %q, want one naming %q", joined, want)
		}
	}
	// Every note must say what the reader should do about it rather than
	// only that something failed.
	for _, note := range inv.Notes {
		if !strings.Contains(note, " - ") {
			t.Errorf("note %q gives no remedy", note)
		}
	}
	// The report is still produced.
	if report := Build(h.Root, inv); len(report.Notes) != len(inv.Notes) {
		t.Errorf("report carried %d notes, want all %d", len(report.Notes), len(inv.Notes))
	}
}

// A missing source is not the same as no source. With neither configured the
// report must still say it is blind rather than report an empty machine.
func TestCollectWithNoSourcesStillReportsItIsBlind(t *testing.T) {
	inv, err := Collector{Home: collectorHome(t)}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	joined := strings.Join(inv.Notes, "\n")
	if !strings.Contains(joined, "CONTAINERS UNREADABLE") || !strings.Contains(joined, "SERVERS UNREADABLE") {
		t.Errorf("notes = %q, want both sources reported missing", joined)
	}
}

// A task record the collector could not read must be named like every other
// degraded source. Dropped silently it is indistinguishable from a task that
// never existed, and a live goblin's worktree then reads as one nothing holds.
func TestCollectNamesATaskRecordItCouldNotRead(t *testing.T) {
	h := collectorHome(t)
	path := filepath.Join(h.State, "unreadable-task.meta")
	if err := os.WriteFile(path, []byte("project=C:\\dev\\proj\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer holdOpenExclusively(t, path)()

	inv, err := Collector{Home: h, Docker: stubDocker{}, System: stubSystem{}}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect returned an error instead of degrading: %v", err)
	}
	for _, task := range inv.Tasks {
		if task.ID == "unreadable-task" {
			t.Fatalf("tasks = %+v, want the unreadable record absent", inv.Tasks)
		}
	}
	joined := strings.Join(inv.Notes, "\n")
	if !strings.Contains(joined, "TASK RECORDS UNREADABLE") || !strings.Contains(joined, "unreadable-task") {
		t.Errorf("notes = %q, want one naming the record it could not read", joined)
	}
}

// holdOpenExclusively opens path with no sharing, so every other read of it
// fails the way a record being rewritten under the collector does. It returns
// the function that releases it.
func holdOpenExclusively(t *testing.T, path string) func() {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("lock %s: %v", path, err)
	}
	if _, err := os.ReadFile(path); err == nil {
		syscall.CloseHandle(handle)
		t.Fatalf("%s is still readable, so the test proves nothing", path)
	}
	return func() { syscall.CloseHandle(handle) }
}

// Whether a directory is on disk is the evidence that separates a leftover
// from something the Overlord still wants, so the collector must actually
// look - and must record the answer for both outcomes.
func TestCollectChecksEveryWorkingDirectoryOnDisk(t *testing.T) {
	h := collectorHome(t)
	present := filepath.Join(h.Root, "projects", "siqsermon")
	missing := filepath.Join(h.Root, "projects", "siqsermon", ".worktrees", "gb-gone")
	inv, err := Collector{
		Home:   h,
		Docker: stubDocker{containers: []Container{{Name: "a", WorkDir: present}, {Name: "b", WorkDir: missing}}},
		System: stubSystem{listeners: []Listener{{Port: 3000, WorkDir: missing}}},
	}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if found, checked := inv.Present[normalize(present)]; !checked || !found {
		t.Errorf("present[%q] = %v/%v, want checked and found", present, found, checked)
	}
	if found, checked := inv.Present[normalize(missing)]; !checked || found {
		t.Errorf("present[%q] = %v/%v, want checked and missing", missing, found, checked)
	}
}
