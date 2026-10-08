package supervisor

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// supervisorErrorWakes returns the queued wakes for the supervisor's own
// errors.
func supervisorErrorWakes(t *testing.T, s *Service) []string {
	t.Helper()
	var details []string
	for _, record := range fleetWakeRecords(t, s.Store.Home, "check") {
		if record.Key == "supervisor" {
			details = append(details, record.Detail)
		}
	}
	return details
}

// On 2026-10-07 a failure the sweep met every ten minutes sat on the board as
// a huge amber box that told the Overlord nothing he could act on. What the
// supervisor meets goes to the CFO instead, once while it lasts: a line wakes
// when it is new, a line that stays or comes back with each cycle does not
// wake again, new lines wait out the gap after the last wake rather than
// being dropped, and a line not met for an hour is new again.
func TestASupervisorErrorWakesTheCFOOnceWhenItIsNew(t *testing.T) {
	// Arrange
	s, _ := fleetService(t)
	now := time.Date(2026, 10, 7, 23, 0, 0, 0, time.UTC)
	gone := errors.New("lavish-axi end: ENOENT: no such file or directory, realpath 'priority.html'")
	other := errors.New("run requests: the pipe could not be created")
	third := errors.New("the Update item for Code Goblins v0.5.4 could not be made")

	for _, reading := range []struct {
		after time.Duration
		err   error
		want  []string
	}{
		{0, gone, []string{"priority.html"}},
		{time.Minute, nil, nil},
		{10 * time.Minute, gone, nil},
		{11 * time.Minute, errors.Join(gone, other), []string{"the pipe could not be created"}},
		{12 * time.Minute, third, nil},
		{17 * time.Minute, nil, []string{"v0.5.4"}},
		{18 * time.Minute, errors.Join(gone, other, third), nil},
		{3 * time.Hour, gone, []string{"priority.html"}},
	} {
		before := len(supervisorErrorWakes(t, s))

		// Act
		err := s.wakeForNewErrors(reading.err, now.Add(reading.after))

		// Assert
		if err != nil {
			t.Fatal(err)
		}
		raised := supervisorErrorWakes(t, s)[before:]
		if len(raised) != min(1, len(reading.want)) {
			t.Fatalf("at %s: wakes %q, want %q", reading.after, raised, reading.want)
		}
		for _, want := range reading.want {
			if !strings.HasPrefix(raised[0], "supervisor_error: ") || !strings.Contains(raised[0], want) {
				t.Fatalf("at %s: wake %q, want it to name %q", reading.after, raised[0], want)
			}
		}
		if reading.after == 11*time.Minute && strings.Contains(raised[0], "priority.html") {
			t.Fatalf("a line already woken for woke again: %q", raised[0])
		}
	}
}

func TestPublishWakesTheCFOForANewErrorButNotForUnreadableCI(t *testing.T) {
	// Arrange
	s, _ := fleetService(t)
	s.ciUnreadable = errors.New("ci wakes: list the open pull requests of C:\\dev\\app: gh exited 1")

	// Act
	s.publish(errors.New("the native inbox could not be read"))
	s.publish(errors.New("the native inbox could not be read"))
	s.publish(nil)

	// Assert
	wakes := supervisorErrorWakes(t, s)
	if len(wakes) != 1 || !strings.Contains(wakes[0], "the native inbox could not be read") || strings.Contains(wakes[0], "ci wakes") {
		t.Fatalf("wakes = %q, want one for the supervisor's own error, none for unreadable CI, which wakes as ci_unreadable", wakes)
	}
}
