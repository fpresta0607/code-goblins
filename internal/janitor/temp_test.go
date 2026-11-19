package janitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func stubLiveTemps(t *testing.T, answer func() ([]home.LiveTemp, error)) {
	t.Helper()
	liveTemps = func(context.Context) ([]home.LiveTemp, error) { return answer() }
	t.Cleanup(func() { liveTemps = home.LiveTemps })
}

// The folder every goblin's TMP names is no task's, so no record names it,
// and it may be /tmp for every Git Bash of the user: the sweep that takes a
// scratch folder no task owns for a finished task's leaves it, however old
// and whatever is in it, in the home and on its Dev Drive alike.
func TestSweepLeavesTheSharedTemporaryFolderAlone(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	f.home.DevDrive = filepath.Join(t.TempDir(), "CodeGoblins")
	var shared []string
	for _, root := range f.home.ScratchRoots() {
		folder(t, filepath.Join(root, home.SharedTempDir, "his-own-folder"), false)
		// Aged last, with everything in it: making the folder inside it is
		// a write to it.
		shared = append(shared, folder(t, filepath.Join(root, home.SharedTempDir), true))
	}
	deadScratch := folder(t, filepath.Join(f.home.Scratch(), "gone-task"), true)
	cfg := f.config(reap.Inventory{Processes: []reap.Process{selfProcess(t)}})
	cfg.TempDir = t.TempDir()

	// Act
	record := Sweep(context.Background(), cfg)

	// Assert
	for _, root := range shared {
		for _, kept := range []string{root, filepath.Join(root, "file"), filepath.Join(root, "his-own-folder", "file")} {
			if _, err := os.Stat(kept); err != nil {
				t.Errorf("%s was removed: %v; removed %+v", kept, err, record.Removed)
			}
		}
	}
	if _, err := os.Stat(deadScratch); !os.IsNotExist(err) {
		t.Errorf("the finished task's scratch folder %s survived: %v; notes %v", deadScratch, err, record.Notes)
	}
}

// What asks Windows for the temporary folder, as a Go test does, gets the
// shared folder, so the fleet's leaks there go by the rule the machine's
// temporary folder has: named by temp_patterns, a day old, no process naming
// them. Nothing else in it is touched.
func TestSweepRemovesOldFleetTempFoldersFromTheSharedTemporaryFolder(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	shared := filepath.Join(f.home.Scratch(), home.SharedTempDir)
	leak := folder(t, filepath.Join(shared, "TestLeak123"), true)
	used := folder(t, filepath.Join(shared, "Test4567890"), true)
	recent := folder(t, filepath.Join(shared, "go-build9"), false)
	foreign := folder(t, filepath.Join(shared, "ssh-agent-socket"), true)
	cfg := f.config(reap.Inventory{Processes: []reap.Process{selfProcess(t), {PID: 77, CommandLine: `pkg.test.exe -test.testlogfile=` + used + `\log`}}})
	cfg.TempDir = t.TempDir()

	// Act
	Sweep(context.Background(), cfg)

	// Assert
	if _, err := os.Stat(leak); !os.IsNotExist(err) {
		t.Errorf("the old leak %s survived: %v", leak, err)
	}
	for _, kept := range []string{shared, used, recent, foreign} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
}

// A finished task's scratch folder that is still /tmp for a running Git
// Bash, as on a machine whose first shell was a goblin's started before TMP
// named the shared folder, stays until no running Git Bash has it.
func TestSweepLeavesAFolderThatIsALiveMsysTmp(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	liveTmp := folder(t, filepath.Join(f.home.Scratch(), "first-shell-task"), true)
	deadScratch := folder(t, filepath.Join(f.home.Scratch(), "gone-task"), true)
	temp := t.TempDir()
	holdsTmp := folder(t, filepath.Join(temp, "Test123"), true)
	stubLiveTemps(t, func() ([]home.LiveTemp, error) {
		return []home.LiveTemp{
			{Runtime: `C:\Program Files\Git\usr\bin`, Folder: liveTmp},
			{Runtime: `C:\msys64\usr\bin`, Folder: filepath.Join(holdsTmp, "001")},
		}, nil
	})
	cfg := f.config(reap.Inventory{Processes: []reap.Process{selfProcess(t)}})
	cfg.TempDir = temp

	// Act
	record := Sweep(context.Background(), cfg)

	// Assert
	for _, kept := range []string{liveTmp, holdsTmp} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s, a running Git Bash's /tmp or the folder holding it, was removed: %v", kept, err)
		}
	}
	if _, err := os.Stat(deadScratch); !os.IsNotExist(err) {
		t.Errorf("the scratch folder no Git Bash has as /tmp survived: %v", err)
	}
	var kept []string
	for _, item := range record.Kept {
		if item.Kind == "temp" && strings.Contains(item.Detail, "/tmp") {
			kept = append(kept, item.Path+": "+item.Detail)
		}
	}
	if len(kept) != 2 || !strings.Contains(strings.Join(kept, "\n"), `C:\Program Files\Git\usr\bin`) {
		t.Errorf("kept = %q, want both folders reported as a running Git Bash's /tmp, with the runtime named", kept)
	}
}

// Which folders are a live /tmp is the evidence a removal rests on: when it
// cannot be read, nothing old enough to go goes, and the sweep says so once.
func TestSweepRemovesNoTempFolderWhenTheLiveTmpCannotBeRead(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	deadScratch := folder(t, filepath.Join(f.home.Scratch(), "gone-task"), true)
	temp := t.TempDir()
	leak := folder(t, filepath.Join(temp, "go-build123"), true)
	asked := 0
	stubLiveTemps(t, func() ([]home.LiveTemp, error) {
		asked++
		return nil, errors.New("cygpath.exe is missing")
	})
	cfg := f.config(reap.Inventory{Processes: []reap.Process{selfProcess(t)}})
	cfg.TempDir = temp

	// Act
	record := Sweep(context.Background(), cfg)

	// Assert
	for _, kept := range []string{deadScratch, leak} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed without knowing whether it is a live /tmp: %v", kept, err)
		}
	}
	if notes := strings.Join(record.Notes, "\n"); strings.Count(notes, "cygpath.exe is missing") != 1 {
		t.Errorf("notes = %q, want the unread /tmp named once", notes)
	}
	if asked != 1 {
		t.Errorf("the running runtimes were asked %d times in one sweep, want once", asked)
	}
}

// A sweep with nothing old enough to remove starts no program to ask.
func TestSweepAsksNoRuntimeWhenNothingIsOldEnoughToRemove(t *testing.T) {
	f := newSweepFixture(t)
	folder(t, filepath.Join(f.home.Scratch(), "gone-task"), false)
	folder(t, filepath.Join(f.home.Scratch(), "live"), true)
	stubLiveTemps(t, func() ([]home.LiveTemp, error) {
		t.Error("the running runtimes were asked with nothing to remove")
		return nil, nil
	})
	cfg := f.config(reap.Inventory{
		Tasks:     []reap.Task{{ID: "live", Meta: state.TaskMeta{ID: "live"}}},
		Processes: []reap.Process{selfProcess(t)},
	})
	cfg.TempDir = t.TempDir()

	Sweep(context.Background(), cfg)
}

// makeAged dates path and everything under it age ago.
func makeAged(t *testing.T, path string, age time.Duration) {
	t.Helper()
	when := time.Now().Add(-age)
	if err := filepath.Walk(path, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, when, when)
	}); err != nil {
		t.Fatal(err)
	}
}

// Under the clean mark the sweep frees at once what a finished task left: its
// scratch folder goes an hour after its last write rather than a day, the
// hour a spawn takes between making a scratch folder and writing the record
// that names it. A folder temp_patterns names keeps the day, since a test
// running now uses it without naming it.
func TestSweepUnderTheCleanMarkTakesAFinishedTasksScratchAfterAnHour(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		isTight   bool
		isScratch bool
		age       time.Duration
		isGone    bool
	}{
		{name: "tight, a finished task's scratch unwritten for two hours", isTight: true, isScratch: true, age: 2 * time.Hour, isGone: true},
		{name: "not tight, a finished task's scratch unwritten for two hours", isScratch: true, age: 2 * time.Hour},
		{name: "tight, a scratch folder written twenty minutes ago", isTight: true, isScratch: true, age: 20 * time.Minute},
		{name: "tight, a test's folder in the temporary folder unwritten for two hours", isTight: true, age: 2 * time.Hour},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			f := newSweepFixture(t)
			temp := t.TempDir()
			path := filepath.Join(temp, "Test123")
			if testCase.isScratch {
				path = filepath.Join(f.home.Scratch(), "gone-task")
			}
			folder(t, path, false)
			makeAged(t, path, testCase.age)
			stubLiveTemps(t, func() ([]home.LiveTemp, error) { return nil, nil })
			cfg := f.config(reap.Inventory{Processes: []reap.Process{selfProcess(t)}})
			cfg.TempDir = temp
			cfg.IsTight = testCase.isTight

			// Act
			record := Sweep(context.Background(), cfg)

			// Assert
			_, err := os.Stat(path)
			if isGone := os.IsNotExist(err); isGone != testCase.isGone {
				t.Fatalf("%s gone = %v, want %v; removed %+v, notes %q", path, isGone, testCase.isGone, record.Removed, record.Notes)
			}
			if item, ok := has(record.Removed, path); testCase.isGone && (!ok || !strings.Contains(item.Detail, "under the clean mark")) {
				t.Errorf("removed = %+v, want %s removed because free disk is under the clean mark", record.Removed, path)
			}
		})
	}
}

// A finished task's scratch folder stays, however old and however tight the
// disk, while a report, card, question, backlog row or memory file still
// names a path in it, written as Windows or as Git Bash writes it. A name
// that only begins with the folder's, as another task's, keeps nothing.
func TestSweepKeepsAFinishedTasksScratchThatSomethingStillReadNames(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		write  func(t *testing.T, h home.Home, scratch string)
		isKept bool
	}{
		{name: "an open backlog row", isKept: true, write: func(t *testing.T, h home.Home, scratch string) {
			writeFile(t, filepath.Join(h.Data, "backlog.md"), "# Backlog\n\n## Queued\n\n- [ ] next-task - start from "+scratch+`\handoff.md`+"\n")
		}},
		{name: "a memory file in Git Bash's spelling", isKept: true, write: func(t *testing.T, h home.Home, scratch string) {
			writeFile(t, filepath.Join(h.Data, "memory", "fact.md"), "The trace is at /"+strings.ToLower(scratch[:1])+filepath.ToSlash(scratch[2:])+"/trace.zip\n")
		}},
		{name: "an open Command Center question", isKept: true, write: func(t *testing.T, h home.Home, scratch string) {
			writeFile(t, filepath.Join(h.State, ".supervisor.json"), `{"questions":[{"text":"Which page? `+strings.ReplaceAll(filepath.ToSlash(scratch), `"`, ``)+`/options.html","status":"pending"}]}`)
		}},
		{name: "a report the CEO has not read", isKept: true, write: func(t *testing.T, h home.Home, scratch string) {
			if _, err := wake.Append(h.State, "notify", "gone-task", "done: the page is "+scratch+"."); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a live goblin's report", isKept: true, write: func(t *testing.T, h home.Home, scratch string) {
			writeFile(t, state.StatusPath(h.State, "live"), "working: read "+scratch+`\notes.txt`+"\n")
		}},
		{name: "another task's folder", write: func(t *testing.T, h home.Home, scratch string) {
			writeFile(t, filepath.Join(h.Data, "memory", "fact.md"), "See "+scratch+`-2\notes.txt`+"\n")
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			f := newSweepFixture(t)
			scratch := folder(t, filepath.Join(f.home.Scratch(), "gone-task"), true)
			testCase.write(t, f.home, scratch)
			makeOld(t, scratch)
			stubLiveTemps(t, func() ([]home.LiveTemp, error) { return nil, nil })
			cfg := f.config(reap.Inventory{
				Tasks:     []reap.Task{{ID: "live", Meta: state.TaskMeta{ID: "live"}}},
				Processes: []reap.Process{selfProcess(t)},
			})
			cfg.TempDir = t.TempDir()
			cfg.IsTight = true

			// Act
			record := Sweep(context.Background(), cfg)

			// Assert
			_, err := os.Stat(scratch)
			if isKept := err == nil; isKept != testCase.isKept {
				t.Fatalf("%s kept = %v, want %v; removed %+v, kept %+v, notes %q", scratch, isKept, testCase.isKept, record.Removed, record.Kept, record.Notes)
			}
			if item, ok := has(record.Kept, scratch); testCase.isKept && (!ok || !strings.Contains(item.Detail, "still names a path in")) {
				t.Errorf("kept = %+v, want %s kept because something still read names it", record.Kept, scratch)
			}
		})
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
