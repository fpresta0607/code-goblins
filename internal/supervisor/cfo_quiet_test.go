package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/wake"
)

func writeQuietQueue(t *testing.T, store *Store, records []wake.Record) {
	t.Helper()
	var data []byte
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, line...), '\n')
	}
	if err := os.WriteFile(filepath.Join(store.Home.State, ".wake-queue"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func quietSnapshot(t *testing.T, store *Store) (time.Time, int, float64) {
	t.Helper()
	if err := store.keepCFOQuiet(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store}
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Quiet struct {
			Since     time.Time `json:"since"`
			Count     int       `json:"count"`
			OldestAge float64   `json:"oldest_age"`
		} `json:"cfo_quiet"`
	}
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatal(err)
	}
	return view.Quiet.Since, view.Quiet.Count, view.Quiet.OldestAge
}

func TestSnapshotNoticesOnlyUnansweredQuestionsThatHaveWaitedTenMinutesOnTheCFO(t *testing.T) {
	now := time.Now().UTC()
	for name, test := range map[string]struct {
		records []wake.Record
		count   int
	}{
		"not yet ten minutes": {[]wake.Record{{Seq: 1, Kind: "notify", Key: "task-1", Detail: "blocked: Which store?", Time: now.Add(-9 * time.Minute)}}, 0},
		"blocked and failed, including the newer question": {[]wake.Record{
			{Seq: 1, Kind: "notify", Key: "task-1", Detail: "blocked: Which store?", Time: now.Add(-11 * time.Minute)},
			{Seq: 2, Kind: "notify", Key: "task-2", Detail: "failed: Which fix?", Time: now.Add(-time.Minute)},
		}, 2},
		"news and a wait on him are not questions": {[]wake.Record{
			{Seq: 1, Kind: "notify", Key: "task-1", Detail: "done: PR https://github.com/o/r/pull/1", Time: now.Add(-time.Hour)},
			{Seq: 2, Kind: "notify", Key: "task-2", Detail: "waiting on overlord: Sign in", Time: now.Add(-time.Hour)},
		}, 0},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			writeQuietQueue(t, store, test.records)
			// Act
			since, count, age := quietSnapshot(t, store)
			// Assert
			if count != test.count || since.IsZero() != (test.count == 0) {
				t.Fatalf("notice since=%s count=%d, want count=%d", since, count, test.count)
			}
			if count > 0 && age < 11*60 {
				t.Fatalf("oldest age=%f, want at least eleven minutes", age)
			}
		})
	}
}

func TestAQuietCFONoticeKeepsOneIdentityThroughAnswersAndRestartUntilTheStretchEnds(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	oldest := wake.Record{Seq: 1, Kind: "notify", Key: "task-1", Detail: "blocked: Which store?", Time: time.Now().UTC().Add(-12 * time.Minute)}
	next := wake.Record{Seq: 2, Kind: "notify", Key: "task-2", Detail: "failed: Which fix?", Time: time.Now().UTC().Add(-11 * time.Minute)}
	writeQuietQueue(t, store, []wake.Record{oldest, next})
	first, count, _ := quietSnapshot(t, store)
	if first.IsZero() || count != 2 {
		t.Fatalf("notice=%s count=%d, want one notice for two questions", first, count)
	}
	// Act: answer the oldest without draining its wake, then restart.
	if err := wake.MarkAnswered(home.State, oldest.Seq, wake.AnsweredByCFO, "SQLite"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	again, count, _ := quietSnapshot(t, reopened)
	// Assert
	if !again.Equal(first) || count != 1 {
		t.Fatalf("restarted notice=%s count=%d, want %s count=1", again, count, first)
	}
	if err := wake.MarkAnswered(home.State, next.Seq, wake.AnsweredByCFO, "Fix it"); err != nil {
		t.Fatal(err)
	}
	ended, count, _ := quietSnapshot(t, reopened)
	if !ended.IsZero() || count != 0 {
		t.Fatalf("answered questions kept notice=%s count=%d", ended, count)
	}
	writeQuietQueue(t, reopened, []wake.Record{{Seq: 3, Kind: "notify", Key: "task-3", Detail: "blocked: Which port?", Time: oldest.Time}})
	second, count, _ := quietSnapshot(t, reopened)
	if second.IsZero() || second.Equal(first) || count != 1 {
		t.Fatalf("next stretch=%s count=%d, want a fresh notice", second, count)
	}
}
