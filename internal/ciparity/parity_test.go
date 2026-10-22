package ciparity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseCountsReadsPytestsLastSummaryLine(t *testing.T) {
	for _, test := range []struct {
		name    string
		log     string
		want    Counts
		isFound bool
	}{
		{"a pass with skips and warnings", "2026-10-21T22:41:38.7372811Z ========= 7446 passed, 2 skipped, 352246 warnings in 405.90s (0:06:45) =========", Counts{Passed: 7446, Skipped: 2}, true},
		{"failures and an error", "== 3 failed, 10 passed, 1 error in 2.00s ==", Counts{Passed: 10, Failed: 3, Errors: 1}, true},
		{"expected failures and deselected tests", "=== 5 passed, 1 xfailed, 1 xpassed, 4 deselected in 1.50s ===", Counts{Passed: 5, XFailed: 1, XPassed: 1, Deselected: 4}, true},
		{"the last of two lines", "== 1 passed in 1.00s ==\n== 2 passed in 1.00s ==", Counts{Passed: 2}, true},
		{"no summary", "collected 0 items", Counts{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got, isFound := ParseCounts(test.log)

			// Assert
			if got != test.want || isFound != test.isFound {
				t.Fatalf("counts = %+v, %v; want %+v, %v", got, isFound, test.want, test.isFound)
			}
		})
	}
}

// fakeGitHub answers the record's reads from what a test put in it, and
// counts the logs read.
type fakeGitHub struct {
	runs     []Run
	jobs     map[int64][]Job
	logs     map[int64]string
	logReads int
}

func (f *fakeGitHub) MirrorRuns(context.Context, int) ([]Run, error) { return f.runs, nil }

func (f *fakeGitHub) Jobs(_ context.Context, run int64) ([]Job, error) {
	jobs, isKnown := f.jobs[run]
	if !isKnown {
		return nil, fmt.Errorf("no run %d", run)
	}
	return jobs, nil
}

func (f *fakeGitHub) Log(_ context.Context, job int64) (string, error) {
	f.logReads++
	log, isKnown := f.logs[job]
	if !isKnown {
		return "", fmt.Errorf("no job %d", job)
	}
	return log, nil
}

const (
	head = "aff7266449a1129088e8f6f8211633f20f99275f"
	base = "4f6b80233d097e6ee843162e6d48084ab8d5a634"
)

// mirrored is a CI run 100 whose backend-shard (1) its mirror run 200 copied
// on runner cg-desktop-a, both passing with the same counts on the same merge.
func mirrored() *fakeGitHub {
	start := time.Date(2026, 10, 21, 22, 33, 0, 0, time.UTC)
	return &fakeGitHub{
		runs: []Run{{ID: 200, Title: "Desktop mirror of CI run 100", Status: "completed", Conclusion: "success"}},
		jobs: map[int64][]Job{
			100: {{ID: 11, Name: "backend-shard (1)", Conclusion: "success", Started: start, Completed: start.Add(9 * time.Minute), Runner: "GitHub Actions 7"}},
			200: {{ID: 21, Name: "desktop-backend-shard (1)", Conclusion: "success", Started: start.Add(time.Hour), Completed: start.Add(time.Hour + 12*time.Minute), Runner: "cg-desktop-a"}},
		},
		logs: map[int64]string{
			11: "HEAD is now at f04064ade Merge " + head + " into " + base + "\n===== 7446 passed, 2 skipped in 405.90s =====",
			21: "desktop mirror: Merge " + head + " into " + base + ", tree 0123\n===== 7446 passed, 2 skipped in 433.10s =====",
		},
	}
}

func TestCollectPairsEachDesktopJobWithItsHostedCopyAndItsWait(t *testing.T) {
	// Arrange
	github := mirrored()
	turns := t.TempDir()
	turnLog := "cfo gate turn: waiting for its turn (1m0s so far): the turn is held by cfo verify fast of task Ike (pid 1), for 2m0s; this run is next in line\ncfo gate turn: took its turn after 3m0s\ncfo ci-runner: the VM started with runner cg-desktop-a\n"
	if err := os.WriteFile(filepath.Join(turns, "job-200-20261021-223300.log"), []byte(turnLog), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	pairs, err := Collect(context.Background(), github, Record{Repository: "o/r"}, 30, turns)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 {
		t.Fatalf("pairs = %+v, want one", pairs)
	}
	pair := pairs[0]
	if pair.CIRun != 100 || pair.MirrorRun != 200 || pair.Job != "backend-shard (1)" || pair.Hosted.Job != 11 || pair.Desktop.Job != 21 {
		t.Fatalf("pair = %+v, want CI run 100's backend-shard (1) beside mirror run 200's copy", pair)
	}
	if pair.Hosted.Head != head || pair.Desktop.Base != base || pair.Desktop.Counts == nil || pair.Desktop.Counts.Passed != 7446 || pair.Desktop.Seconds != 720 {
		t.Fatalf("pair = %+v, want each copy's merge, counts and time", pair)
	}
	if pair.Waited != "3m0s" || pair.HeldBy != "cfo verify fast of task Ike (pid 1), for 2m0s" {
		t.Fatalf("waited %q, held by %q: want the turn's wait and what held it last", pair.Waited, pair.HeldBy)
	}
	if differences := pair.Differences(); len(differences) != 0 {
		t.Fatalf("differences = %q, want none", differences)
	}
}

func TestCollectSkipsWhatItCannotPairOrAlreadyHolds(t *testing.T) {
	for _, test := range []struct {
		name      string
		arrange   func(github *fakeGitHub) Record
		wantReads int
	}{
		{"a mirror that had nothing to mirror", func(github *fakeGitHub) Record {
			github.logs[21] = "The hosted backend-shard (1) did not run to a verdict, so there is nothing to mirror."
			return Record{Repository: "o/r"}
		}, 1},
		{"a run of another title", func(github *fakeGitHub) Record {
			github.runs[0].Title = "Desktop mirror"
			return Record{Repository: "o/r"}
		}, 0},
		{"a run not finished", func(github *fakeGitHub) Record {
			github.runs[0].Status = "in_progress"
			return Record{Repository: "o/r"}
		}, 0},
		{"a desktop job that was skipped", func(github *fakeGitHub) Record {
			github.jobs[200][0].Conclusion = "skipped"
			return Record{Repository: "o/r"}
		}, 0},
		{"a pair the record holds", func(*fakeGitHub) Record {
			return Record{Repository: "o/r", Pairs: []Pair{{MirrorRun: 200, Job: "backend-shard (1)"}}}
		}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			github := mirrored()
			record := test.arrange(github)

			// Act
			pairs, err := Collect(context.Background(), github, record, 30, t.TempDir())

			// Assert
			if err != nil || len(pairs) != 0 || github.logReads != test.wantReads {
				t.Fatalf("pairs = %+v, %v, logs read %d: want none, after %d log reads", pairs, err, github.logReads, test.wantReads)
			}
		})
	}
}

func TestDifferencesNameHowTheDesktopCopyDiffered(t *testing.T) {
	counts := Counts{Passed: 10}
	same := Pair{Hosted: Copy{Conclusion: "success", Counts: &counts, Head: head, Base: base}, Desktop: Copy{Conclusion: "success", Counts: &counts, Head: head, Base: base}}
	for _, test := range []struct {
		name   string
		change func(pair *Pair)
		want   string
	}{
		{"a lost desktop run", func(pair *Pair) { pair.Desktop.Conclusion = "cancelled" }, "the desktop run was lost, cancelled"},
		{"another merge", func(pair *Pair) { pair.Desktop.Base = head }, "the two copies did not show the same merge tested"},
		{"another conclusion", func(pair *Pair) { pair.Desktop.Conclusion = "failure" }, "hosted success, desktop failure"},
		{"other counts", func(pair *Pair) { pair.Desktop.Counts = &Counts{Passed: 8, Skipped: 2} }, "hosted 10 passed, 0 failed, 0 skipped, 0 errors, desktop 8 passed, 0 failed, 2 skipped, 0 errors"},
		{"no summary", func(pair *Pair) { pair.Hosted.Counts = nil }, "a copy's log has no pytest summary"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			pair := same

			// Act
			test.change(&pair)

			// Assert
			if differences := pair.Differences(); !slices.Contains(differences, test.want) {
				t.Fatalf("differences = %q, want %q among them", differences, test.want)
			}
		})
	}
	if differences := same.Differences(); len(differences) != 0 {
		t.Fatalf("differences of matching copies = %q, want none", differences)
	}
}

func TestStandingsCountTheRunsInARowEachJobMatched(t *testing.T) {
	// Arrange
	counts := Counts{Passed: 10}
	pairOn := func(day int, job, desktopConclusion string) Pair {
		return Pair{
			CIRun: int64(day), Job: job, Finished: time.Date(2026, 10, day, 0, 0, 0, 0, time.UTC),
			Hosted:  Copy{Conclusion: "success", Counts: &counts, Head: head, Base: base},
			Desktop: Copy{Conclusion: desktopConclusion, Counts: &counts, Head: head, Base: base},
		}
	}
	var pairs []Pair
	for day := 1; day <= 22; day++ {
		pairs = append(pairs, pairOn(day, "backend-shard (1)", "success"))
	}
	pairs = append(pairs, pairOn(1, "backend-shard (2)", "success"), pairOn(2, "backend-shard (2)", "cancelled"), pairOn(3, "backend-shard (2)", "success"))
	slices.Reverse(pairs)

	// Act
	standings := Standings(Record{Repository: "o/r", Pairs: pairs})

	// Assert
	if len(standings) != 2 {
		t.Fatalf("standings = %+v, want one for each job", standings)
	}
	first, second := standings[0], standings[1]
	if first.Job != "backend-shard (1)" || first.Streak != 22 || first.Pairs != 22 || !first.IsReady() {
		t.Fatalf("first = %+v, want 22 in a row and ready", first)
	}
	if second.Streak != 1 || second.Lost != 1 || second.DifferedOn != 2 || second.IsReady() || !strings.Contains(second.Difference, "lost") {
		t.Fatalf("second = %+v, want its streak started again after the lost run of CI run 2", second)
	}
}

func TestARecordIsKeptAndReadBackForItsRepositoryAlone(t *testing.T) {
	// Arrange
	path := Path(t.TempDir(), "o/r")
	record := Record{Repository: "o/r", Pairs: []Pair{
		{CIRun: 2, MirrorRun: 4, Job: "backend-shard (1)", Finished: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)},
		{CIRun: 1, MirrorRun: 3, Job: "backend-shard (1)", Finished: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
	}}

	// Act
	err := Write(path, record)
	read, readErr := Read(path, "o/r")
	_, otherErr := Read(path, "o/other")
	empty, emptyErr := Read(filepath.Join(t.TempDir(), "none.json"), "o/r")

	// Assert
	if err != nil || readErr != nil {
		t.Fatal(err, readErr)
	}
	if len(read.Pairs) != 2 || read.Pairs[0].CIRun != 1 || read.Pairs[1].CIRun != 2 {
		t.Fatalf("read = %+v, want both pairs in the order their hosted jobs finished", read)
	}
	if otherErr == nil {
		t.Fatal("read another repository's record, want it refused")
	}
	if emptyErr != nil || empty.Repository != "o/r" || len(empty.Pairs) != 0 {
		t.Fatalf("empty = %+v, %v: want an empty record when there is none yet", empty, emptyErr)
	}
}
