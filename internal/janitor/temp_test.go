package janitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/state"
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
