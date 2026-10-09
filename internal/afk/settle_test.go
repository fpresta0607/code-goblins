package afk

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// A line left for the Overlord that the CFO saw to later in the stretch, as
// three of the six left on the night of 2026-10-08 were, is settled with what
// became of it. The log keeps the line and the settle after it, and the line
// folds into a decision marked settled.
func TestTheCFOSettlesALineLeftForHimWithWhatBecameOfIt(t *testing.T) {
	// Arrange
	dir, on := turnedOn(t)
	left, err := Log(dir, leftForHim(), night.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	settle, err := Settle(dir, left.At, "backlog row pd-auth is done: the fleet's own token signs in", night.Add(2*time.Hour))

	// Assert
	if err != nil {
		t.Fatalf("Settle = %v, want the line settled", err)
	}
	if settle.Kind != KindSettle || settle.Item != left.At.Format(time.RFC3339Nano) || settle.What != left.What || settle.Evidence != "backlog row pd-auth is done: the fleet's own token signs in" {
		t.Errorf("settle = %+v, want it to name the line by when it was logged and say what became of it", settle)
	}
	entries, _, _ := Entries(dir, on.Session)
	if len(entries) != 3 || entries[1].What != left.What || entries[1].Settled != "" {
		t.Fatalf("log = %+v, want the switch, the line as it was written and the settle", entries)
	}
	decisions := Decisions(entries)
	if len(decisions) != 1 || decisions[0].Settled != "backlog row pd-auth is done: the fleet's own token signs in" || decisions[0].Struck != "" {
		t.Errorf("decisions = %+v, want the line kept and marked settled with what became of it", decisions)
	}
}

// A settle names a line left for him in the stretch that is on, says what
// became of it, and is made once: everything else is refused and writes
// nothing to the log.
func TestSettleRefusesALineItCannotName(t *testing.T) {
	cases := []struct {
		name string
		// line picks the line to settle among the stretch's lines, and how
		// what became of it.
		line func(left, merge Entry) time.Time
		how  string
		// before is done to the log before the settle.
		before func(t *testing.T, dir string, left Entry)
		want   string
	}{
		{name: "a line the log does not hold", line: func(Entry, Entry) time.Time { return night.Add(9 * time.Hour) }, how: "done", want: "no line left for him at"},
		{name: "a decision that was not left for him", line: func(_, merge Entry) time.Time { return merge.At }, how: "done", want: "no line left for him at"},
		{name: "no word of what became of it", line: func(left, _ Entry) time.Time { return left.At }, how: "  ", want: "what became of the line"},
		{name: "a struck line", line: func(left, _ Entry) time.Time { return left.At }, how: "done", before: func(t *testing.T, dir string, left Entry) {
			if _, err := Strike(dir, left.At, "written by mistake", night.Add(90*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}, want: "struck"},
		{name: "a line settled already", line: func(left, _ Entry) time.Time { return left.At }, how: "done", before: func(t *testing.T, dir string, left Entry) {
			if _, err := Settle(dir, left.At, "the run finished on its own", night.Add(90*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}, want: "settled already"},
		{name: "a stretch that has ended", line: func(left, _ Entry) time.Time { return left.At }, how: "done", before: func(t *testing.T, dir string, _ Entry) {
			if _, err := TurnOff(dir, "his own terminal", night.Add(90*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}, want: "Command Center"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			dir, _ := turnedOn(t)
			left, err := Log(dir, leftForHim(), night.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			merge, err := Log(dir, Entry{Kind: KindMerge, What: "https://github.com/o/r/pull/7", Evidence: "green on main"}, night.Add(70*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if c.before != nil {
				c.before(t, dir, left)
			}
			logged, _, _ := Entries(dir, "")

			// Act
			_, err = Settle(dir, c.line(left, merge), c.how, night.Add(2*time.Hour))

			// Assert
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Settle = %v, want it refused, saying %q", err, c.want)
			}
			if after, _, _ := Entries(dir, ""); len(after) != len(logged) {
				t.Errorf("log = %+v, want nothing written for a refused settle", after)
			}
		})
	}
}

// A stretch that has ended is not on: the refusal says so as ErrNotOn does.
func TestSettleAfterAFKModeTurnedOffIsNotOn(t *testing.T) {
	// Arrange
	dir, _ := turnedOn(t)
	left, err := Log(dir, leftForHim(), night.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TurnOff(dir, "his own terminal", night.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err = Settle(dir, left.At, "done", night.Add(3*time.Hour))

	// Assert
	if !errors.Is(err, ErrNotOn) {
		t.Errorf("Settle = %v, want ErrNotOn", err)
	}
}
