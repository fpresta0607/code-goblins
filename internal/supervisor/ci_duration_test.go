package supervisor

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// A cycle reads the fleet wakes again only once their file changed: a cycle
// runs on each hook event and each save, many a minute, and each read parsed
// the 493 KB record, about 3 ms of processor, which changes about once a minute
// (#1716). Durations of the same size written over the record, its write
// time kept, are what only a new read would show.
func TestACycleReadsTheFleetWakesAgainOnlyOnceTheyChanged(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	path := fleetWakesPath(h.State)
	recorded := func(seconds int64) {
		t.Helper()
		watched := fleetWakes{Durations: []CIDuration{{Repository: "owner/repo", Kind: "ci", Name: "test", Seconds: seconds, FinishedAt: time.Date(2026, 10, 2, 12, 13, 0, 0, time.UTC)}}}
		if err := writeFleetWakes(h.State, watched); err != nil {
			t.Fatal(err)
		}
	}
	shown := func() float64 {
		t.Helper()
		service.cycle(t.Context(), false)
		snapshot, err := service.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.CIUsual) != 1 {
			t.Fatalf("usual CI times = %+v, want the one recorded", snapshot.CIUsual)
		}
		return snapshot.CIUsual[0].Seconds
	}
	recorded(780)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	first := shown()
	recorded(790)
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	// Act
	unchanged := shown()
	recorded(1790)
	changed := shown()

	// Assert
	if first != 780 || unchanged != 780 || changed != 1790 {
		t.Errorf("shown %g s, then %g s over the same size and write time, then %g s once it changed; want 780, 780 and 1790", first, unchanged, changed)
	}
}

func TestCIFinishedRecordsMeasuredDurationsAndOmitsUnknownTimes(t *testing.T) {
	service, h := fleetService(t)
	now := time.Date(2026, 10, 2, 12, 20, 0, 0, time.UTC)
	pull := ghPullRequest{URL: "https://github.com/owner/repo/pull/42", HeadRefOid: strings.Repeat("a", 40), Checks: []ghCheck{
		{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: "2026-10-02T12:00:00Z", CompletedAt: "2026-10-02T12:13:00Z"},
		{Name: "deploy", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: "2026-10-02T12:00:00Z", CompletedAt: "2026-10-02T12:10:30Z"},
		{Name: "unknown", Status: "COMPLETED", Conclusion: "SUCCESS", CompletedAt: "2026-10-02T12:13:00Z"},
	}}
	watched := fleetWakes{}

	if err := reportChecks(h.State, &watched, "task", pull, false, now, nil); err != nil {
		t.Fatal(err)
	}
	if err := writeFleetWakes(h.State, watched); err != nil {
		t.Fatal(err)
	}
	service.cycle(t.Context(), false)
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// A run without a start, measured, would move the usual CI time off 780 s.
	want := []CIUsual{{Repository: "owner/repo", Kind: "ci", Seconds: 780}, {Repository: "owner/repo", Kind: "deploy", Seconds: 630}}
	if !slices.Equal(snapshot.CIUsual, want) {
		t.Fatalf("usual CI times %+v, want %+v", snapshot.CIUsual, want)
	}
}

// #1611: the snapshot carried the 200 CI runs and deploys last measured, 40 KB
// of every board's first snapshot and sent whole again whenever one finished,
// though a card paused on CI shows only how long its repository's runs
// usually take.
func TestABoardIsSentEachRepositorysUsualCITimeRatherThanItsRuns(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	measured := func(repository, kind string, seconds ...int64) []CIDuration {
		runs := make([]CIDuration, len(seconds))
		for i, each := range seconds {
			runs[i] = CIDuration{Repository: repository, Kind: kind, Name: "test", Seconds: each, FinishedAt: time.Date(2026, 10, 28, 12, i, 0, 0, time.UTC)}
		}
		return runs
	}
	watched := fleetWakes{Durations: slices.Concat(measured("o/r", "deploy", 661, 600), measured("o/r", "ci", 900, 720, 780), measured("o/other", "deploy", 120))}
	if err := writeFleetWakes(h.State, watched); err != nil {
		t.Fatal(err)
	}
	service.cycle(t.Context(), false)

	// Act
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	type usual struct {
		Repository, Kind string
		Seconds          float64
	}
	var board struct {
		Usual []usual          `json:"ci_usual"`
		Runs  *json.RawMessage `json:"ci_durations"`
	}
	if err := json.Unmarshal(data, &board); err != nil {
		t.Fatal(err)
	}
	want := []usual{{"o/other", "deploy", 120}, {"o/r", "ci", 780}, {"o/r", "deploy", 630.5}}
	if !slices.Equal(board.Usual, want) || board.Runs != nil {
		t.Errorf("the board is sent the usual times %+v and the runs %v, want %+v and no runs", board.Usual, board.Runs != nil, want)
	}
}
