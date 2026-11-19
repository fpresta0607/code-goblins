package janitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/layout"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// caseSpellings makes a folder named name in parent and returns it, its
// name in capitals beside it, and whether the file system opens the one by
// the other.
func caseSpellings(t *testing.T, parent, name string) (lower, upper string, isOnePath bool) {
	t.Helper()
	lower, upper = filepath.Join(parent, name), filepath.Join(parent, strings.ToUpper(name))
	if err := os.MkdirAll(lower, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(upper)
	return lower, upper, err == nil
}

// caseHome is a home with its state and data folders and nothing in them.
func caseHome(t *testing.T) home.Home {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	for _, dir := range []string{h.State, h.Data, h.Scratch()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// A temporary folder a running process names in another case is in use, so
// it stays, exactly where the file system opens the folder by that spelling.
func TestRemoveTempLeakReadsCommandLinesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	lower, upper, isOnePath := caseSpellings(t, t.TempDir(), "pd-1")
	makeOld(t, lower)
	cfg := Config{Inventory: reap.Inventory{Processes: []reap.Process{selfProcess(t), {PID: 77, CommandLine: `tool "` + filepath.Join(upper, "run.log") + `"`}}}, Now: time.Now(), SelfPID: os.Getpid()}
	record := Record{}
	processes, isRead := cfg.readableProcesses(&record)
	if !isRead {
		t.Fatalf("the process list was not read: %v", record.Notes)
	}

	// Act
	cfg.removeTempLeak(lower, processes, func() ([]home.LiveTemp, error) { return nil, nil }, &record)

	// Assert
	if isKept := exists(lower); isKept != isOnePath {
		t.Fatalf("%s, named by a process as %s, kept: %t, want %t", lower, upper, isKept, isOnePath)
	}
}

// A temp pattern names a folder in another case exactly where the file
// system of the folder that holds it ignores case.
func TestFleetTempsMatchPatternsByTheFileSystemsCase(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	_, _, isOnePath := caseSpellings(t, dir, "testleak1")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Settings: fleetconfig.Settings{TempPatterns: []string{"Test*"}}}

	// Act
	temps := cfg.fleetTemps(dir, entries)

	// Assert
	if isFleet := len(temps) == 1; isFleet != isOnePath {
		t.Fatalf("fleetTemps(%s) under Test* = %v, want testleak1 named: %t", dir, temps, isOnePath)
	}
}

// A scratch folder named for a live task in another case is that task's
// exactly where the file system opens the one name by the other, and the
// folder every goblin's TMP names is left alone only in its own spelling
// elsewhere.
func TestRemoveTempLeaksNamesScratchFoldersByTheFileSystemsCase(t *testing.T) {
	// Arrange
	h := caseHome(t)
	stubLiveTemps(t, func() ([]home.LiveTemp, error) { return nil, nil })
	if err := os.WriteFile(filepath.Join(h.State, "live.meta"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Stat(filepath.Join(h.State, "LIVE.meta"))
	isOnePath := statErr == nil
	live := folder(t, filepath.Join(h.Scratch(), "LIVE"), true)
	shared := folder(t, filepath.Join(h.Scratch(), strings.ToUpper(home.SharedTempDir)), true)
	cfg := Config{Home: h, Inventory: reap.Inventory{Tasks: []reap.Task{{ID: "live", Meta: state.TaskMeta{ID: "live"}}}}, Now: time.Now()}

	// Act
	cfg.removeTempLeaks(nil, &Record{})

	// Assert
	for _, path := range []string{live, shared} {
		if isKept := exists(path); isKept != isOnePath {
			t.Errorf("%s kept: %t, want %t, as the file system opens the one spelling by the other: %t", path, isKept, isOnePath, isOnePath)
		}
	}
}

// A retired task's archived and finished folders named for a live task in
// another case are that live task's exactly where the file system opens the
// one name by the other, and are trimmed elsewhere.
func TestTrimRetiredTasksNamesALiveTaskByTheFileSystemsCase(t *testing.T) {
	// Arrange
	h := caseHome(t)
	if err := os.WriteFile(filepath.Join(h.State, "live.meta"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Stat(filepath.Join(h.State, "LIVE.meta"))
	isOnePath := statErr == nil
	f := &sweepFixture{home: h, now: time.Now()}
	archived, finished := retiredTask(t, f, "LIVE", f.now.Add(-48*time.Hour))
	cfg := f.config(reap.Inventory{Tasks: []reap.Task{{ID: "live", Meta: state.TaskMeta{ID: "live"}}}})

	// Act
	cfg.trimRetiredTasks(nil, &Record{})

	// Assert
	for _, path := range []string{filepath.Join(archived, "evidence.zip"), filepath.Join(finished, "run.log")} {
		if isKept := exists(path); isKept != isOnePath {
			t.Errorf("%s kept: %t, want %t", path, isKept, isOnePath)
		}
	}
}

// A retired folder's deliverables and the entries filing keeps, spelled in
// another case, stay exactly where the file system opens them by that
// spelling.
func TestKeepTextRecordKeepsNamesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	_, _, isOnePath := caseSpellings(t, parent, "probe")
	retired := filepath.Join(parent, "retired")
	deliverables := folder(t, filepath.Join(retired, "DELIVERABLES"), false)
	named := folder(t, filepath.Join(retired, "EVIDENCE"), false)
	makeOld(t, retired)
	cfg := Config{Now: time.Now()}

	// Act
	cfg.keepTextRecord(retired, "a retired task's folder", nil, &Record{}, []string{"evidence"})

	// Assert
	for _, path := range []string{deliverables, named} {
		if isKept := exists(path); isKept != isOnePath {
			t.Errorf("%s kept: %t, want %t", path, isKept, isOnePath)
		}
	}
}

// A folder named .git in another case is a repository exactly where the
// file system opens it as .git.
func TestFindRepositoryNamesGitByTheFileSystemsCase(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	_, upper, isOnePath := caseSpellings(t, parent, ".git")
	if err := os.Remove(filepath.Join(parent, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(upper, 0o755); err != nil {
		t.Fatal(err)
	}

	// Act
	repository, err := findRepository(parent)

	// Assert
	if isFound := err == nil && repository == upper; isFound != isOnePath {
		t.Fatalf("findRepository(%s) = %q, %v; want %s found: %t", parent, repository, err, upper, isOnePath)
	}
}

// Backups whose kinds differ only in case are one kind, of which the newest
// two stay, exactly where the file system opens the one name by the other.
func TestKeepRecentBackupsGroupsKindsByTheFileSystemsCase(t *testing.T) {
	// Arrange
	h := caseHome(t)
	backups := filepath.Join(h.State, "backups")
	_, _, isOnePath := caseSpellings(t, backups, "probe")
	oldest := filepath.Join(backups, "kind-20261101")
	for _, path := range []string{oldest, filepath.Join(backups, "Kind-20261102"), filepath.Join(backups, "KIND-20261103")} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{Home: h, Now: time.Now()}

	// Act
	cfg.keepRecentBackups(nil, &Record{})

	// Assert
	if isRemoved := !exists(oldest); isRemoved != isOnePath {
		t.Fatalf("%s removed: %t, want %t", oldest, isRemoved, isOnePath)
	}
}

// Why filing keeps a data folder is told for the folder named by the filing
// record in another case exactly where the file system opens it by that name.
func TestReportDataSizeNamesKeptFoldersByTheFileSystemsCase(t *testing.T) {
	// Arrange
	h := caseHome(t)
	_, _, isOnePath := caseSpellings(t, h.Data, "task")
	if err := layout.WriteRecord(h.State, layout.Record{Kept: []layout.Kept{{ID: "TASK", Reason: "under way"}}}); err != nil {
		t.Fatal(err)
	}
	record := Record{Buckets: Buckets{Data: DataLimit + 1}}

	// Act
	Config{Home: h}.reportDataSize(&record)

	// Assert
	if len(record.Strays) != 1 {
		t.Fatalf("strays = %+v, want the data folder", record.Strays)
	}
	if isTold := strings.Contains(record.Strays[0].Detail, "data/task 0.0 MB (under way)"); isTold != isOnePath {
		t.Fatalf("detail = %q, want why task stays told: %t", record.Strays[0].Detail, isOnePath)
	}
}

// An entry of the projects root the janitor has seen in another case is seen
// exactly where the file system opens the one name by the other.
func TestReportProjectsRootKnowsEntriesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	h := caseHome(t)
	root := t.TempDir()
	_, _, isOnePath := caseSpellings(t, root, "stray")
	known, err := json.Marshal([]string{"STRAY"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, knownName), known, 0o600); err != nil {
		t.Fatal(err)
	}
	record := Record{}

	// Act
	Config{Home: h, ProjectsRoot: root}.reportProjectsRoot(&record)

	// Assert
	if isReported := len(record.Strays) == 1; isReported == isOnePath {
		t.Fatalf("strays = %+v, want stray reported: %t", record.Strays, !isOnePath)
	}
}
