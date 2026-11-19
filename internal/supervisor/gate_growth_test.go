package supervisor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/janitor"
)

// gateSweep records a janitor sweep whose gate home keeps linked completed
// runs' checkouts past its retention under junctions and own under folders of
// its own, the oldest of each two weeks old.
func gateSweep(t *testing.T, stateDir string, now time.Time, linked, own int) {
	t.Helper()
	past := &janitor.GatePastRetention{Retention: 24 * time.Hour}
	if linked > 0 {
		past.Linked = janitor.GateRuns{Count: linked, Oldest: now.Add(-14 * 24 * time.Hour)}
	}
	if own > 0 {
		past.Unlinked = janitor.GateRuns{Count: own, Oldest: now.Add(-14 * 24 * time.Hour)}
	}
	record := janitor.Record{Time: now, Gate: &janitor.GateHome{Folders: []string{"G1", "G2"}, Bytes: 82 * gigabyte, PastRetention: past}}
	if err := janitor.WriteRecord(stateDir, record); err != nil {
		t.Fatal(err)
	}
}

// The gate's home keeping completed runs' checkouts past its own retention
// wakes the CEO once for each cause it had not woken for, naming every cause
// it finds, and files the junction cause, the gate's reaper skipping a
// junction, on the gate's repository under its open issue's key. A folder of
// the gate's own is woken for with what to check, and filed by nothing: the
// gate's current source removes those itself. A cause that goes and comes
// back wakes again (#2761).
func TestGateGrowthWakesOnceForEachNewCauseAndFilesTheJunctionCauseOnTheGate(t *testing.T) {
	// Arrange
	s, filings := filingBoard(t, nil)
	h := s.Store.Home
	s.Options.Dispatch = &Dispatch{Memory: func() (Memory, error) {
		return Memory{Available: 20 * gigabyte, CommitAvailable: 20 * gigabyte, Total: 32 * gigabyte}, nil
	}}
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	read := func(linked, own int) int {
		t.Helper()
		before := len(fleetWakeRecords(t, h, "disk"))
		gateSweep(t, h.State, now, linked, own)
		now = now.Add(time.Minute)
		if err := s.checkFleet(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		return len(fleetWakeRecords(t, h, "disk")) - before
	}

	// Act and Assert: the junction cause wakes once and is filed.
	if woke := read(58, 0) + read(58, 0); woke != 1 {
		t.Fatalf("the junction cause on two readings woke %d times, want one", woke)
	}
	detail := fleetWakeRecords(t, h, "disk")[0].Detail
	for _, want := range []string{"gate_growth:", "at G1 and G2 holds 82.0 GB", "past its 24h worktree.retention", "58 under repository folders that are directory junctions", "(the oldest last written 336h ago)", "never removes anything in the gate's home"} {
		if !strings.Contains(detail, want) {
			t.Errorf("gate wake %q lacks %q", detail, want)
		}
	}
	filing := nextFiling(t, filings)
	if filing.project != "no-mistakes" || filing.root.RootKey() != "worktree reaper skips a repository folder that is a directory junction" || !strings.Contains(filing.root.Evidence, "58 completed runs' checkouts kept past the 24h retention") {
		t.Fatalf("filing = %+v, want the junction cause on the gate's repository", filing)
	}

	// The gate's own folders add a cause: one more wake naming both, no filing.
	if woke := read(58, 40) + read(58, 40); woke != 1 {
		t.Fatalf("a new cause on two readings woke %d times, want one", woke)
	}
	detail = fleetWakeRecords(t, h, "disk")[1].Detail
	for _, want := range []string{"58 under repository folders that are directory junctions", "40 under repository folders of its own", "no-mistakes --version", "goblins issue file"} {
		if !strings.Contains(detail, want) {
			t.Errorf("gate wake %q lacks %q", detail, want)
		}
	}

	// The junction cause goes, then comes back: it wakes and is filed again.
	if woke := read(0, 40); woke != 0 {
		t.Fatalf("a cause going woke %d times, want none", woke)
	}
	if woke := read(58, 40); woke != 1 {
		t.Fatalf("the junction cause coming back woke %d times, want one", woke)
	}
	if again := nextFiling(t, filings); again.root.RootKey() != filing.root.RootKey() {
		t.Fatalf("filing = %+v, want the junction cause filed again", again)
	}
	if woke := read(0, 0); woke != 0 {
		t.Fatalf("no cause woke %d times, want none", woke)
	}
	select {
	case extra := <-filings:
		t.Fatalf("filed %+v as well, want the junction cause alone filed", extra)
	case <-time.After(100 * time.Millisecond): // stall-accepted: no further filing is meant to arrive, so a stall only makes the wait longer.
	}
}

func TestDurationTextDropsOnlyAZeroUnit(t *testing.T) {
	for _, testCase := range []struct {
		duration time.Duration
		want     string
	}{
		{24 * time.Hour, "24h"},
		{342 * time.Hour, "342h"},
		{30 * time.Minute, "30m"},
		{90 * time.Minute, "1h30m"},
		{12*time.Hour + 30*time.Minute, "12h30m"},
		{90 * time.Second, "1m30s"},
		{time.Hour + 10*time.Second, "1h0m10s"},
	} {
		// Act
		got := durationText(testCase.duration)

		// Assert
		if got != testCase.want {
			t.Errorf("durationText(%v) = %q, want %q", testCase.duration, got, testCase.want)
		}
	}
}
