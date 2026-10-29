package supervisor

import (
	"encoding/json"
	"os"
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
	shown := func() int64 {
		t.Helper()
		service.cycle(t.Context(), false)
		snapshot, err := service.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.CIDurations) != 1 {
			t.Fatalf("CI durations = %+v, want the one recorded", snapshot.CIDurations)
		}
		return snapshot.CIDurations[0].Seconds
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
		t.Errorf("shown %d s, then %d s over the same size and write time, then %d s once it changed; want 780, 780 and 1790", first, unchanged, changed)
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
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"repository":"owner/repo"`, `"duration_seconds":780`, `"duration_seconds":630`, `"kind":"deploy"`, `"kind":"ci"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("snapshot missing %s: %s", want, data)
		}
	}
	if strings.Contains(string(data), `"name":"unknown"`) {
		t.Fatal("missing start was presented as a measured duration")
	}
}
