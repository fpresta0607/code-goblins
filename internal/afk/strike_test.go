package afk

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// A line the CFO wrote by mistake, such as the "nothing: CFO note" with
// evidence "test" of 2026-10-09 00:08:18Z, is struck through with the CFO's
// reason. The log keeps the line and the strike after it, and loses nothing.
func TestTheCFOStrikesALineWrittenByMistakeAndTheLogKeepsBoth(t *testing.T) {
	// Arrange
	dir, on := turnedOn(t)
	bogus, err := Log(dir, Entry{Kind: KindOther, What: "nothing: CFO note", Evidence: "test"}, night.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	strike, err := Strike(dir, bogus.At, "written by mistake while testing the log command", night.Add(2*time.Hour))

	// Assert
	if err != nil {
		t.Fatalf("Strike = %v, want the line struck", err)
	}
	entries, _, _ := Entries(dir, on.Session)
	if len(entries) != 3 || entries[1].What != "nothing: CFO note" || entries[1].Evidence != "test" {
		t.Fatalf("log = %+v, want the switch, the line as it was written and the strike", entries)
	}
	if strike.Kind != KindStrike || strike.Item != bogus.At.Format(time.RFC3339Nano) || strike.What != bogus.What || strike.Evidence != "written by mistake while testing the log command" {
		t.Errorf("strike = %+v, want it to name the line by when it was logged and carry the reason", strike)
	}
	decisions := Decisions(entries)
	if len(decisions) != 1 || decisions[0].Struck != "written by mistake while testing the log command" {
		t.Errorf("decisions = %+v, want the line kept and marked struck with the reason", decisions)
	}
}

// A struck line is shown struck in the report: under a heading of its own,
// never as something left for him or decided, and with the reason.
func TestAStruckLineIsShownStruckInTheReportAndLeavesNothingForHim(t *testing.T) {
	// Arrange
	dir, on := turnedOn(t)
	left, err := Log(dir, leftForHim(), night.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Strike(dir, left.At, "pd-auth signs in with the fleet's own token, so nothing is his", night.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	entries, _, _ := Entries(dir, on.Session)
	report := Report{Session: on.Session, Since: night, Ended: night.Add(3 * time.Hour), Decisions: Decisions(entries)}

	// Act
	sections := report.Sections()
	var text bytes.Buffer
	if err := Render(&text, report); err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(sections) != 1 {
		t.Errorf("headings = %+v, want only Struck by the CFO: the only line was struck", sections)
	}
	last := sections[len(sections)-1]
	if last.Title != "Struck by the CFO" || len(last.Entries) != 1 || last.Entries[0].What != left.What {
		t.Errorf("last heading = %+v, want the struck line under Struck by the CFO", last)
	}
	for _, want := range []string{"Struck by the CFO (1)", "- Sign in to Vercel for pd-auth? (struck)", "  Struck: pd-auth signs in with the fleet's own token, so nothing is his"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("the report text lacks %q:\n%s", want, text.String())
		}
	}
}

// AFK mode can be on again before the CFO strikes a line of a stretch that
// already ended, as the bogus line of 2026-10-09 was. The strike belongs to
// the stretch on now, so the report the Overlord reads next shows it.
func TestAStrikeOfAnEarlierStretchsLineIsShownInTheNextReport(t *testing.T) {
	// Arrange
	dir, first := turnedOn(t)
	bogus, err := Log(dir, Entry{Kind: KindLeft, What: "nothing: CFO note", Evidence: "test", Diagnosis: "none", Tried: "none", Options: []string{"a one", "a two"}}, night.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TurnOff(dir, "his own board", night.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	second, _, err := TurnOn(dir, "his own board", nil, night.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	strike, err := Strike(dir, bogus.At, "a test line the CFO wrote by mistake", night.Add(4*time.Hour))

	// Assert
	if err != nil || strike.Session != second.Session {
		t.Fatalf("Strike = %+v, %v, want it in the stretch on now", strike, err)
	}
	entries, _, _ := Entries(dir, second.Session)
	report := Report{Session: second.Session, Decisions: Decisions(entries), Struck: StruckEarlier(entries)}
	last := report.Sections()[len(report.Sections())-1]
	if last.Title != "Struck by the CFO" || len(last.Entries) != 1 || last.Entries[0].What != "nothing: CFO note" || last.Entries[0].Struck != "a test line the CFO wrote by mistake" {
		t.Errorf("last heading = %+v, want the earlier stretch's line struck with the reason", last)
	}
	if earlier, _, _ := Entries(dir, first.Session); len(Decisions(earlier)) != 1 {
		t.Errorf("the first stretch's log = %+v, want its line kept as it was", earlier)
	}
}

// After a stretch ended its report is kept, and a strike made then belongs to
// it: the kept report shows it struck when it is read.
func TestAStrikeAfterTheStretchEndedShowsInItsKeptReport(t *testing.T) {
	// Arrange
	dir, on := turnedOn(t)
	bogus, err := Log(dir, Entry{Kind: KindOther, What: "nothing: CFO note", Evidence: "test"}, night.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TurnOff(dir, "his own board", night.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	entries, _, _ := Entries(dir, on.Session)
	if err := SaveReport(dir, Report{Session: on.Session, Decisions: Decisions(entries)}); err != nil {
		t.Fatal(err)
	}

	// Act
	if _, err := Strike(dir, bogus.At, "a test line the CFO wrote by mistake", night.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	report, found, err := ReadReport(dir)

	// Assert
	if err != nil || !found || len(report.Decisions) != 1 || report.Decisions[0].Struck != "a test line the CFO wrote by mistake" {
		t.Errorf("ReadReport = %+v, %v, %v, want the kept report's line struck", report, found, err)
	}
}

func TestAStrikeNeedsAReasonAndALineOfTheCFOsNotStruckBefore(t *testing.T) {
	// Arrange
	dir, on := turnedOn(t)
	bogus, err := Log(dir, Entry{Kind: KindOther, What: "nothing: CFO note", Evidence: "test"}, night.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Strike(dir, bogus.At, "written by mistake", night.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		at     time.Time
		reason string
	}{
		"no reason":             {bogus.At, "  "},
		"no line logged then":   {night.Add(90 * time.Minute), "written by mistake"},
		"the switch itself":     {on.Since, "it was never on"},
		"a line struck already": {bogus.At, "written by mistake again"},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			_, err := Strike(dir, tc.at, tc.reason, night.Add(3*time.Hour))

			// Assert
			if err == nil {
				t.Fatalf("Strike(%s, %q) was accepted", tc.at, tc.reason)
			}
			if entries, _, _ := Entries(dir, on.Session); len(entries) != 3 {
				t.Errorf("log = %+v, want the switch, the line and the one strike", entries)
			}
		})
	}
}
