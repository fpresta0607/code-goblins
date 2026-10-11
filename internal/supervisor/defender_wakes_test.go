package supervisor

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/defender"
)

// defenderRecords is a Defender whose records the test sets, and the times
// the supervisor read from.
type defenderRecords struct {
	report defender.Report
	err    error
	asked  []time.Time
}

func (records *defenderRecords) read(_ context.Context, since time.Time) (defender.Report, error) {
	records.asked = append(records.asked, since)
	return records.report, records.err
}

// A detection or an upload Defender records under the fleet's folders reaches
// the CFO as one wake, naming the file and the test and task that made it,
// and never the Overlord as a notice of his own to read alone. What Defender
// recorded before the supervisor first looked raises nothing, and nothing is
// told twice, however often it is read.
func TestEachNewDefenderRecordWakesTheCFOOnce(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	records := &defenderRecords{}
	s.Options.Defender = records.read
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	// The detected file is in a test's temporary folder on another drive,
	// so what its path says of who made it does not depend on where this
	// test runs. The uploaded one is in the home.
	made := `X:\CodeGoblins\scratch\cg-auth-probe-hang\TestOneLineInstallKeepsANoMistakesAtThePin67573441\003\cfo.exe.download`
	script := filepath.Join(h.Root, "candidates", "cand-v0.5.14", "install.ps1")
	check := func(after time.Duration) int {
		t.Helper()
		before := len(fleetWakeRecords(t, h, "check"))
		now = now.Add(after)
		if err := s.checkFleet(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		return len(fleetWakeRecords(t, h, "check")) - before
	}

	// Act and Assert
	records.report = defender.Report{Detections: []defender.Detection{{ID: "{OLD}", Time: now.Add(-24 * time.Hour), Threat: "Trojan:Win32/Bearfoos.B!ml", Files: []string{made}}}}
	if woke := check(0); woke != 0 || len(records.asked) != 0 {
		t.Fatalf("the first look woke %d times after %d reads, want it only to mark where the watch begins", woke, len(records.asked))
	}
	if woke := check(time.Minute); woke != 0 || len(records.asked) != 0 {
		t.Fatalf("a minute later Defender was read %d times and woke %d, want no read before %s has passed", len(records.asked), woke, defenderEvery)
	}

	records.report = defender.Report{
		Detections: []defender.Detection{
			{ID: "{NEW}", Time: now.Add(5 * time.Minute), Threat: "Trojan:Win32/Bearfoos.B!ml", Process: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, Files: []string{made}},
			{ID: "{HIS}", Time: now.Add(6 * time.Minute), Threat: "Trojan:Win32/Other", Files: []string{`D:\Photos\viewer.exe`}},
		},
		Uploads: []defender.Upload{{Time: now.Add(7 * time.Minute), File: script, SHA256: "bce8a63a02f20d9c1c2c10aace013d40476fcf786146836708a662da3695da61"}},
	}
	if woke := check(defenderEvery); woke != 2 {
		t.Fatalf("a new detection and a new upload woke %d times, want one wake each and none for a file outside the fleet's folders", woke)
	}
	if first := records.asked[0]; !first.Equal(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("Defender was read from %s, want from when the watch began", first)
	}
	wakes := fleetWakeRecords(t, h, "check")
	detected, sent := wakes[len(wakes)-2].Detail, wakes[len(wakes)-1].Detail
	for _, want := range []string{"defender_detection:", "Trojan:Win32/Bearfoos.B!ml", made, "test TestOneLineInstallKeepsANoMistakesAtThePin of task cg-auth-probe-hang", "cfo defender --since"} {
		if !strings.Contains(detected, want) {
			t.Errorf("the detection's wake lacks %q:\n%s", want, detected)
		}
	}
	for _, want := range []string{"defender_upload:", script, "bce8a63a02f2"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the upload's wake lacks %q:\n%s", want, sent)
		}
	}
	for _, detail := range []string{detected, sent} {
		if strings.ContainsAny(detail, ";\u2014") || strings.Contains(detail, "Photos") {
			t.Errorf("the wake holds a semicolon, an em dash or a file that is not the fleet's:\n%s", detail)
		}
	}

	if woke := check(defenderEvery); woke != 0 {
		t.Fatalf("the same records read again woke %d times, want none", woke)
	}
}

// A Defender that cannot be read is said on the board once it has failed a
// few readings in a row, and raises no wake of its own.
func TestADefenderThatCannotBeReadIsReportedNotSilent(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	records := &defenderRecords{err: errors.New("read Microsoft Defender's records: access denied")}
	s.Options.Defender = records.read
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if err := s.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}

	// Act
	var reported error
	for reading := 1; reading <= failingPasses; reading++ {
		reported = s.checkFleet(context.Background(), now.Add(time.Duration(reading)*defenderEvery))
	}

	// Assert
	if reported == nil || !strings.Contains(reported.Error(), "access denied") {
		t.Errorf("after %d failed readings the check returned %v, want what Defender said", failingPasses, reported)
	}
	if woke := len(fleetWakeRecords(t, h, "check")); woke != 0 {
		t.Errorf("a Defender that cannot be read raised %d wakes, want none", woke)
	}
}
