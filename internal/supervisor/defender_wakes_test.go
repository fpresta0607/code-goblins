package supervisor

import (
	"context"
	"errors"
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
