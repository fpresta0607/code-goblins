package janitor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// retiredTask makes the folders a task retired two days ago left: its task
// temporary folder archived in state, and its data folder filed as finished,
// each holding its text record and what else the task left.
func retiredTask(t *testing.T, f *sweepFixture, id string, archivedAt time.Time) (archived, finished string) {
	t.Helper()
	archived = filepath.Join(f.home.State, state.ArchiveDirName, id+"."+archivedAt.UTC().Format("20060102T150405Z"))
	writeFiles(t, archived, map[string]string{
		"handoff-1.md":  "where it stood",
		id + ".status":  "done: https://github.com/o/r/pull/1",
		"notes.txt":     "notes",
		"evidence.zip":  "an evidence archive",
		"build/out.bin": "build output",
	})
	finished = filepath.Join(f.home.Data, "archive", "finished", id)
	writeFiles(t, finished, map[string]string{
		"brief.md":                "the brief",
		"deliverables/report.pdf": "what he was handed",
		"evidence/shot.png":       "a screenshot",
		"run.log":                 "a log",
	})
	makeOld(t, archived)
	makeOld(t, finished)
	return archived, finished
}

// A retired task keeps its text record, and its data folder its
// deliverables; its scratch, logs, build output and evidence go.
func TestSweepKeepsARetiredTaskToItsTextRecordAndDeliverables(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	archived, finished := retiredTask(t, f, "gone", f.now.Add(-48*time.Hour))
	cfg := f.config(reap.Inventory{Processes: []reap.Process{selfProcess(t)}})

	// Act
	record := Sweep(context.Background(), cfg)

	// Assert
	for _, kept := range []string{"handoff-1.md", "gone.status", "notes.txt"} {
		if !exists(filepath.Join(archived, kept)) {
			t.Errorf("the archived %s was removed; notes %v", kept, record.Notes)
		}
	}
	for _, kept := range []string{"brief.md", filepath.Join("deliverables", "report.pdf")} {
		if !exists(filepath.Join(finished, kept)) {
			t.Errorf("the finished %s was removed; notes %v", kept, record.Notes)
		}
	}
	for _, gone := range []string{filepath.Join(archived, "evidence.zip"), filepath.Join(archived, "build"), filepath.Join(finished, "evidence"), filepath.Join(finished, "run.log")} {
		if exists(gone) {
			t.Errorf("%s survived; notes %v, strays %v", gone, record.Notes, record.Strays)
		}
	}
	for _, folder := range []string{archived, finished} {
		if item, ok := has(record.Removed, folder); !ok || item.Bytes == 0 {
			t.Errorf("the record does not show %s trimmed: %+v", folder, record.Removed)
		}
	}
}

// Every reason a retired task's folder might still matter keeps all of it.
func TestSweepLeavesARetiredTasksFolderThatMightStillMatter(t *testing.T) {
	tests := []struct {
		name      string
		arrange   func(t *testing.T, f *sweepFixture, archived string, inv *reap.Inventory)
		archiveAt time.Duration
		stray     bool
	}{
		{name: "the task is live again", arrange: func(_ *testing.T, _ *sweepFixture, _ string, inv *reap.Inventory) {
			inv.Tasks = []reap.Task{{ID: "gone", Meta: state.TaskMeta{ID: "gone"}}}
		}},
		{name: "it was archived within the day", archiveAt: -2 * time.Hour},
		{name: "something in it was written within the day", arrange: func(t *testing.T, _ *sweepFixture, archived string, _ *reap.Inventory) {
			now := time.Now()
			if err := os.Chtimes(filepath.Join(archived, "build", "out.bin"), now, now); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "it holds a git repository", stray: true, arrange: func(t *testing.T, _ *sweepFixture, archived string, _ *reap.Inventory) {
			gitIn(t, archived, "init", "-q", filepath.Join(archived, "build", "verification-root"))
			makeOld(t, archived)
		}},
		{name: "a running process names it", arrange: func(_ *testing.T, _ *sweepFixture, archived string, inv *reap.Inventory) {
			inv.Processes = append(inv.Processes, reap.Process{PID: 77, CommandLine: `pwsh -File ` + filepath.Join(archived, "run.ps1")})
		}},
		{name: "a task record cannot be read", arrange: func(_ *testing.T, _ *sweepFixture, _ string, inv *reap.Inventory) {
			inv.UnreadableTasks = []string{"other"}
		}},
		{name: "the process list is not proven read", arrange: func(_ *testing.T, _ *sweepFixture, _ string, inv *reap.Inventory) {
			inv.Processes = []reap.Process{{PID: 77, CommandLine: "unrelated"}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newSweepFixture(t)
			at := -48 * time.Hour
			if test.archiveAt != 0 {
				at = test.archiveAt
			}
			archived, _ := retiredTask(t, f, "gone", f.now.Add(at))
			inv := reap.Inventory{Processes: []reap.Process{selfProcess(t)}}
			if test.arrange != nil {
				test.arrange(t, f, archived, &inv)
			}

			// Act
			record := Sweep(context.Background(), f.config(inv))

			// Assert
			for _, kept := range []string{"evidence.zip", filepath.Join("build", "out.bin")} {
				if !exists(filepath.Join(archived, kept)) {
					t.Errorf("%s was removed", kept)
				}
			}
			if _, ok := has(record.Strays, archived); ok != test.stray {
				t.Errorf("reported as a stray: %v, want %v; strays %+v", ok, test.stray, record.Strays)
			}
		})
	}
}

// state\backups keeps the newest two of each kind; a backup whose name
// carries no time is a kind of its own.
func TestSweepKeepsTheNewestTwoBackupsOfEachKind(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	backups := filepath.Join(f.home.State, "backups")
	writeFiles(t, backups, map[string]string{
		"home-migrate-20260901T000000Z/data/backlog.md": "oldest",
		"home-migrate-20260926T221124Z/data/backlog.md": "older",
		"home-migrate-20261001T120000Z/data/backlog.md": "newest",
		"skills-20261004.tar":                           "skills",
		"skills-20261005.tar":                           "skills",
		"settings.json.bak":                             "no time in its name",
	})
	cfg := f.config(reap.Inventory{Processes: []reap.Process{selfProcess(t)}})

	// Act
	record := Sweep(context.Background(), cfg)

	// Assert
	if exists(filepath.Join(backups, "home-migrate-20260901T000000Z")) {
		t.Errorf("the oldest home-migrate backup survived; notes %v", record.Notes)
	}
	for _, kept := range []string{"home-migrate-20260926T221124Z", "home-migrate-20261001T120000Z", "skills-20261004.tar", "skills-20261005.tar", "settings.json.bak"} {
		if !exists(filepath.Join(backups, kept)) {
			t.Errorf("%s was removed", kept)
		}
	}
}

// A data folder over 200 MB is reported, never trimmed for its size.
func TestSweepReportsADataFolderOverItsLimit(t *testing.T) {
	tests := []struct {
		name  string
		bytes int64
		stray bool
	}{
		{name: "at the limit", bytes: DataLimit},
		{name: "over the limit", bytes: DataLimit + 1, stray: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newSweepFixture(t)
			record := Record{Buckets: Buckets{Data: test.bytes}}

			// Act
			f.config(reap.Inventory{}).reportDataSize(&record)

			// Assert
			item, ok := has(record.Strays, f.home.Data)
			if ok != test.stray || ok && !strings.Contains(item.Detail, "over the 200 MB") {
				t.Errorf("strays = %+v, want a report: %v", record.Strays, test.stray)
			}
		})
	}
}
