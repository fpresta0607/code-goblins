package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var started = time.Date(2026, 10, 2, 14, 30, 0, 0, time.UTC)

// run records one finished run of a project's commit in the store, and
// returns where its report went.
func run(t *testing.T, project string, start time.Time, commit string) string {
	t.Helper()
	log, path, err := Begin(project, start, commit, "affected")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.WriteString("ok\n"); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Finish(path, Report{Version: ReportVersion, Project: project, Commit: commit, Level: "affected", Status: "passed", Start: start, Log: log.Name()}); err != nil {
		t.Fatal(err)
	}
	return path
}

// A run's report lands beside its log under the project's name in the store,
// named for when it started, its commit and its level, and reads back as it
// was written.
func TestFinishWritesTheReportBesideItsLogUnderTheProject(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	log, path, err := Begin("code-goblins", started, "0123456789abcdef0123456789abcdef01234567", "fast")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	report := Report{
		Version: ReportVersion, Project: "code-goblins", Commit: "0123456789abcdef0123456789abcdef01234567", Level: "fast", RequiredLevel: "affected",
		Selected: []Selection{{Package: "example.com/m/a", Why: "changed"}},
		Left:     []Left{{Check: "tests of example.com/m/b", Why: "imports example.com/m/a"}},
		Checks:   []Result{{Command: []string{"go", "vet", "example.com/m/a"}, Status: "passed"}},
		Status:   "passed", Start: started, Log: log.Name(),
	}

	// Act
	err = Finish(path, report)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	dir, name := filepath.Split(path)
	if filepath.Clean(dir) != filepath.Join(store, "reports", "code-goblins") || !strings.HasPrefix(name, "20261002T143000Z-01234567-fast-") || !strings.HasSuffix(name, ".json") {
		t.Errorf("report path = %s; want %s with a name that starts 20261002T143000Z-01234567-fast- and ends .json", path, filepath.Join(store, "reports", "code-goblins"))
	}
	if want := strings.TrimSuffix(path, ".json") + ".log"; log.Name() != want {
		t.Errorf("log = %s; want it beside the report as %s", log.Name(), want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var read Report
	if err := json.Unmarshal(data, &read); err != nil {
		t.Fatal(err)
	}
	if read.Level != "fast" || read.RequiredLevel != "affected" || len(read.Selected) != 1 || read.Selected[0].Why != "changed" || len(read.Left) != 1 || read.Left[0].Check != "tests of example.com/m/b" || len(read.Checks) != 1 || read.Checks[0].Status != "passed" || read.Log != log.Name() {
		t.Errorf("the report read back as %+v", read)
	}
}

// Two runs of one commit at one level that start in the same second each
// get their own log and report, so neither overwrites the other's evidence.
func TestBeginGivesRunsThatStartTogetherTheirOwnFiles(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())

	// Act
	first := run(t, "code-goblins", started, "0123456789abcdef")
	second := run(t, "code-goblins", started, "0123456789abcdef")

	// Assert
	if first == second {
		t.Fatalf("both runs report to %s", first)
	}
	for _, path := range []string{first, second} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("a run's report is gone: %v", err)
		}
	}
}

// The store keeps a project's newest reports and removes the oldest with
// their logs, so it never grows without bound. A log with no report belongs
// to a run still going and stays however long ago that run started, and one
// project's runs never remove another's.
func TestFinishKeepsOnlyAProjectsNewestReportsAndTheirLogs(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	other := run(t, "another-project", started.Add(-time.Hour), "ffffffffffffffff")
	running, _, err := Begin("code-goblins", started.Add(-time.Hour), "0123456789abcdef", "full")
	if err != nil {
		t.Fatal(err)
	}
	defer running.Close()

	// Act
	var reports []string
	for minute := range keptReports + 2 {
		reports = append(reports, run(t, "code-goblins", started.Add(time.Duration(minute)*time.Minute), "0123456789abcdef"))
	}

	// Assert
	for index, path := range reports {
		_, reportErr := os.Stat(path)
		_, logErr := os.Stat(strings.TrimSuffix(path, ".json") + ".log")
		if gone := index < 2; gone != os.IsNotExist(reportErr) || gone != os.IsNotExist(logErr) {
			t.Errorf("run %d of %d: report gone %v, log gone %v; want the two oldest gone and the rest kept", index+1, len(reports), os.IsNotExist(reportErr), os.IsNotExist(logErr))
		}
	}
	if _, err := os.Stat(running.Name()); err != nil {
		t.Errorf("the log of a run that had not finished was removed: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("another project's report was removed: %v", err)
	}
}

// gone reports whether a run's report and its log are both removed, and
// fails the test when only one of them is.
func gone(t *testing.T, report string) bool {
	t.Helper()
	_, reportErr := os.Stat(report)
	_, logErr := os.Stat(strings.TrimSuffix(report, ".json") + ".log")
	if os.IsNotExist(reportErr) != os.IsNotExist(logErr) {
		t.Fatalf("%s: report gone %v, log gone %v; want both or neither", report, os.IsNotExist(reportErr), os.IsNotExist(logErr))
	}
	return os.IsNotExist(reportErr)
}

// A run that started before every other report of a full store, as a long
// run does while short ones finish around it, keeps the report it just wrote
// and its log: the oldest of the others makes room for it.
func TestFinishKeepsTheReportItJustWroteHoweverEarlyItsRunStarted(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	var others []string
	for minute := range keptReports {
		others = append(others, run(t, "code-goblins", started.Add(time.Duration(minute)*time.Minute), "0123456789abcdef"))
	}

	// Act
	long := run(t, "code-goblins", started.Add(-time.Hour), "0123456789abcdef")

	// Assert
	if gone(t, long) {
		t.Errorf("the run removed the report it just wrote, %s", long)
	}
	for index, path := range others {
		if removed := gone(t, path); removed != (index == 0) {
			t.Errorf("other run %d of %d: gone %v; want only the oldest gone", index+1, len(others), removed)
		}
	}
}

// The reports kept are the ones written last, whenever their runs started:
// a long run's report outlives a later run's finish while reports written
// before it remain to remove.
func TestFinishKeepsTheReportsWrittenLastWhenALaterRunFinishes(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	var others []string
	for minute := range keptReports - 1 {
		path := run(t, "code-goblins", started.Add(time.Duration(minute)*time.Minute), "0123456789abcdef")
		written := time.Now().Add(time.Duration(minute)*time.Minute - time.Hour)
		if err := os.Chtimes(path, written, written); err != nil {
			t.Fatal(err)
		}
		others = append(others, path)
	}
	long := run(t, "code-goblins", started.Add(-time.Hour), "0123456789abcdef")

	// Act
	later := run(t, "code-goblins", started.Add(time.Hour), "0123456789abcdef")

	// Assert
	if gone(t, long) || gone(t, later) {
		t.Errorf("the two reports written last are not both kept: %s, %s", long, later)
	}
	for index, path := range others {
		if removed := gone(t, path); removed != (index == 0) {
			t.Errorf("other run %d of %d: gone %v; want only the one written first gone", index+1, len(others), removed)
		}
	}
}

// A log with no report that nothing has written to for more than a day
// belongs to a run that never finished, and the project's next finish
// removes it; another project's is not this run's to remove.
func TestFinishRemovesAReportlessLogOlderThanADay(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	abandoned := func(project string) string {
		log, _, err := Begin(project, started, "0123456789abcdef", "fast")
		if err != nil {
			t.Fatal(err)
		}
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-25 * time.Hour)
		if err := os.Chtimes(log.Name(), old, old); err != nil {
			t.Fatal(err)
		}
		return log.Name()
	}
	stale, elsewhere := abandoned("code-goblins"), abandoned("another-project")

	// Act
	run(t, "code-goblins", started, "0123456789abcdef")

	// Assert
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a log with no report, last written 25 hours ago, is still there: %v", err)
	}
	if _, err := os.Stat(elsewhere); err != nil {
		t.Errorf("another project's log was removed: %v", err)
	}
}

// A project's name is one folder inside the store whatever it holds: a
// module path decides it, and a branch under a gate decides the module path.
func TestBeginKeepsAnyProjectNameInsideTheStore(t *testing.T) {
	for _, project := range []string{"..", "../../outside", `up\..\..\outside`, "C:evil", ""} {
		t.Run(project, func(t *testing.T) {
			// Arrange
			store := t.TempDir()
			t.Setenv("CFO_VERIFY_DIR", store)

			// Act
			log, path, err := Begin(project, started, "0123456789abcdef", "fast")

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			inside, err := filepath.Rel(filepath.Join(store, "reports"), path)
			if folder, _, _ := strings.Cut(filepath.ToSlash(inside), "/"); err != nil || folder == ".." || strings.Count(filepath.ToSlash(inside), "/") != 1 {
				t.Errorf("Begin(%q) reports to %s, which is %q from the store's reports; want one folder inside it and the file", project, path, inside)
			}
		})
	}
}

// The store is the folder CFO_VERIFY_DIR names, else cfo/verify in the
// user's cache folder; a test binary that names none is refused, so no test
// writes into the store real runs read.
func TestStoreDirIsTheOverrideElseTheUsersCacheAndNeverThatFromATest(t *testing.T) {
	// Arrange
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	named, namedErr := storeDir(`D:\scratch\verify`, true)
	real, realErr := storeDir("", false)
	_, refused := storeDir("", true)

	// Assert
	if namedErr != nil || named != `D:\scratch\verify` {
		t.Errorf("storeDir with an override = %q, %v; want the override", named, namedErr)
	}
	if want := filepath.Join(cache, "cfo", "verify"); realErr != nil || real != want {
		t.Errorf("storeDir with no override = %q, %v; want %q", real, realErr, want)
	}
	if refused == nil || !strings.Contains(refused.Error(), "CFO_VERIFY_DIR") {
		t.Errorf("storeDir in a test binary with no override = %v; want a refusal naming CFO_VERIFY_DIR", refused)
	}
}

// Reports reads back a project's reports, the run that started last first,
// and none of another project's; a project with no store folder has none, and
// a report that does not read is named rather than skipped.
func TestReportsReadsAProjectsReportsNewestFirst(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	run(t, "code-goblins", started.Add(time.Minute), "1111111111111111")
	run(t, "code-goblins", started.Add(time.Hour), "2222222222222222")
	run(t, "code-goblins", started, "3333333333333333")
	run(t, "another-project", started.Add(2*time.Hour), "4444444444444444")

	// Act
	reports, err := Reports("code-goblins")
	none, noneErr := Reports("never-run")

	// Assert
	var commits []string
	for _, report := range reports {
		commits = append(commits, report.Commit)
	}
	if err != nil || strings.Join(commits, " ") != "2222222222222222 1111111111111111 3333333333333333" {
		t.Errorf("Reports = %q, %v; want the three of code-goblins, newest first", commits, err)
	}
	if none != nil || noneErr != nil {
		t.Errorf("Reports of a project never run = %v, %v; want none", none, noneErr)
	}
	broken := run(t, "code-goblins", started.Add(3*time.Hour), "5555555555555555")
	if err := os.WriteFile(broken, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Reports("code-goblins"); err == nil || !strings.Contains(err.Error(), filepath.Base(broken)) {
		t.Errorf("Reports with a report that does not read = %v, want an error naming it", err)
	}
}
