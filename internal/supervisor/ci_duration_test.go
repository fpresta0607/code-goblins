package supervisor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCIFinishedRecordsMeasuredDurationsAndOmitsUnknownTimes(t *testing.T) {
	service, h := fleetService(t)
	now := time.Date(2026, 10, 2, 12, 20, 0, 0, time.UTC)
	pull := ghPullRequest{URL: "https://github.com/owner/repo/pull/42", HeadRefOid: strings.Repeat("a", 40), Checks: []ghCheck{
		{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: "2026-10-02T12:00:00Z", CompletedAt: "2026-10-02T12:13:00Z"},
		{Name: "deploy", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: "2026-10-02T12:00:00Z", CompletedAt: "2026-10-02T12:10:30Z"},
		{Name: "unknown", Status: "COMPLETED", Conclusion: "SUCCESS", CompletedAt: "2026-10-02T12:13:00Z"},
	}}
	watched := fleetWakes{}

	if err := reportChecks(h.State, &watched, "task", pull, now); err != nil {
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
