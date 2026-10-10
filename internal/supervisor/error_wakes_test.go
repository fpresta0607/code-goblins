package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
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
		s.wakeForNewErrors(reading.err, now.Add(reading.after))

		// Assert
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

// A wake the queue does not take, as while the state folder fails, keeps its
// lines for the next publish, and its own failure never reaches the board,
// where it would pass the storage grace by (CI on PR 464, 2026-10-08).
func TestAnErrorWakeTheQueueRefusesIsToldAtTheNextPublish(t *testing.T) {
	// Arrange
	store, _ := failingStore(t)
	s := &Service{Store: store}
	now := time.Date(2026, 10, 8, 1, 0, 0, 0, time.UTC)

	// Act
	s.publish(errors.New("run pipe closed"))
	refused := s.lastError
	if err := os.MkdirAll(store.Home.State, 0o700); err != nil {
		t.Fatal(err)
	}
	s.wakeForNewErrors(nil, now)

	// Assert
	if refused != "run pipe closed" {
		t.Errorf("the board shows %q, want only the published error", refused)
	}
	if wakes := supervisorErrorWakes(t, s); len(wakes) != 1 || !strings.Contains(wakes[0], "run pipe closed") {
		t.Fatalf("wakes = %q, want the refused line told once the queue takes it", wakes)
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

// On 2026-10-10, with every processor busy, the supervisor woke the CFO twice
// in minutes for reads that ran out of time once: "progress for <task>:
// context deadline exceeded" for three goblins at a time, and "gh could not
// read <pull request>: context deadline exceeded". Such a read is made again
// on the next pass, so one that ran out of time once is nothing to tell.
func TestAReadThatRanOutOfTimeOnceDoesNotWakeTheCFO(t *testing.T) {
	// Arrange
	s, _ := fleetService(t)
	now := time.Date(2026, 10, 10, 5, 0, 0, 0, time.UTC)
	slow := errors.Join(
		fmt.Errorf("progress for cg-a: %w", context.DeadlineExceeded),
		fmt.Errorf("progress for cg-b: %w", context.DeadlineExceeded),
		fmt.Errorf("gh could not read https://github.com/o/r/pull/7: %w", context.DeadlineExceeded),
	)

	// Act: one pass ran out of time, and the passes after it read.
	s.wakeForNewErrors(slow, now)
	s.wakeForNewErrors(nil, now.Add(time.Minute))
	s.wakeForNewErrors(nil, now.Add(10*time.Minute))

	// Assert
	if raised := supervisorErrorWakes(t, s); len(raised) != 0 {
		t.Errorf("reads that ran out of time once woke the CFO: %q", raised)
	}
}

// A read that keeps running out of time is the CFO's to know: he is told
// once, when it has done so for five minutes. An error that is no such read
// wakes at once as it always did, and without the slow reads beside it.
func TestAReadThatKeepsRunningOutOfTimeWakesTheCFOOnce(t *testing.T) {
	// Arrange
	s, _ := fleetService(t)
	now := time.Date(2026, 10, 10, 5, 0, 0, 0, time.UTC)
	slow := fmt.Errorf("progress for cg-a: %w", context.DeadlineExceeded)
	broken := errors.New("run requests: the pipe could not be created")

	// Act
	s.wakeForNewErrors(errors.Join(slow, broken), now)
	atOnce := supervisorErrorWakes(t, s)
	for minute := 1; minute <= 12; minute++ {
		s.wakeForNewErrors(slow, now.Add(time.Duration(minute)*time.Minute))
	}
	raised := supervisorErrorWakes(t, s)

	// Assert
	if len(atOnce) != 1 || !strings.Contains(atOnce[0], "the pipe could not be created") || strings.Contains(atOnce[0], "cg-a") {
		t.Errorf("the first pass woke with %q, want the broken pipe alone", atOnce)
	}
	if len(raised) != 2 || !strings.Contains(raised[1], "progress for cg-a: context deadline exceeded") {
		t.Errorf("twelve minutes of one read running out of time woke with %q, want one more wake naming it", raised)
	}
}

// Two reads that ran out of time more than five minutes apart are two single
// ones, not one that kept failing.
func TestReadsThatRanOutOfTimeFarApartDoNotWakeTheCFO(t *testing.T) {
	// Arrange
	s, _ := fleetService(t)
	now := time.Date(2026, 10, 10, 5, 0, 0, 0, time.UTC)
	slow := fmt.Errorf("progress for cg-a: %w", context.DeadlineExceeded)

	// Act
	for _, after := range []time.Duration{0, 7 * time.Minute, 15 * time.Minute, 24 * time.Minute} {
		s.wakeForNewErrors(slow, now.Add(after))
	}

	// Assert
	if raised := supervisorErrorWakes(t, s); len(raised) != 0 {
		t.Errorf("single reads that ran out of time minutes apart woke the CFO: %q", raised)
	}
}
