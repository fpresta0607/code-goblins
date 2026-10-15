package afk

import (
	"strings"
	"testing"
	"time"
)

func stepAt(minute int, what string) Entry {
	return Entry{At: night.Add(time.Duration(minute) * time.Minute), Kind: KindHis, What: what, Evidence: "his own sign-in. Backlog row cg-sign-in"}
}

func TestACardedStepIsToldByItsCardAndNotAsALineOfItsOwn(t *testing.T) {
	// Arrange
	carded, plain := stepAt(1, "Sign in to Vercel"), stepAt(2, "Cap the Actions spend")
	report := Report{Session: "afk-1", Since: night, Ended: night.Add(time.Hour), From: "the board", EndedFrom: "the board", Decisions: Decisions([]Entry{carded, plain})}
	report.Held = []Held{{Item: HisCardItem(carded), What: carded.What, At: carded.At, Waiting: true, Now: "still waiting on you"}}

	// Act
	sections := report.Sections()

	// Assert
	if got := titled(sections); strings.Join(got, ",") != "Needs you 1" || sections[0].Entries[0].What != plain.What {
		t.Errorf("headings = %q, want only the step with no card as a line of Needs you", got)
	}
}

func TestACardedStepTheCardSettledIsNotUnderSettledByTheCFO(t *testing.T) {
	// Arrange
	carded := stepAt(1, "Sign in to Vercel")
	carded.Settled = "You answered Done on its card"
	report := Report{Session: "afk-1", Since: night, Ended: night.Add(time.Hour), From: "the board", EndedFrom: "the board", Decisions: []Entry{carded}}
	report.Held = []Held{{Item: HisCardItem(carded), What: carded.What, At: carded.At, Now: "you answered it: Done"}}

	// Act
	headings := titled(report.Sections())

	// Assert
	if len(headings) != 0 {
		t.Errorf("headings = %q, want none: its card says what became of it", headings)
	}
}

func TestACardsStepIsSettledAfterAFKTurnsOffAndItsReportReadsItSettled(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	if _, _, err := TurnOn(dir, "the board", nil, night); err != nil {
		t.Fatal(err)
	}
	step, err := Log(dir, stepAt(0, "Sign in to Vercel"), night.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ended, err := TurnOff(dir, "the board", night.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := Entries(dir, ended.Session)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveReport(dir, Report{Session: ended.Session, Since: ended.Since, Ended: ended.Ended, From: "the board", EndedFrom: "the board", Decisions: Decisions(entries)}); err != nil {
		t.Fatal(err)
	}

	// Act
	first := SettleAfter(dir, step.At, "You answered Done on its card", night.Add(2*time.Hour))
	second := SettleAfter(dir, step.At, "You answered Later on its card", night.Add(3*time.Hour))
	kept, found, err := ReadReport(dir)

	// Assert
	if first != nil || second != nil || err != nil || !found {
		t.Fatalf("first = %v, second = %v, read = %v, %v, want both settles taken", first, second, found, err)
	}
	if got := kept.Decisions[0].Settled; got != "You answered Done on its card" {
		t.Errorf("settled = %q, want the first answer kept: a step is settled once", got)
	}
	if NeedsHim(kept.Decisions[0]) {
		t.Error("the step still needs him after its card settled it")
	}
}

func TestASettleAfterNamesALineOfTheLog(t *testing.T) {
	dir := t.TempDir()
	if err := SettleAfter(dir, night, "Done", night); err == nil {
		t.Error("SettleAfter of a line the log does not hold = nil, want it refused")
	}
}

func TestACommandIsLoggedOnlyOnAStepOnlyHeCanTake(t *testing.T) {
	for name, tc := range map[string]struct {
		entry Entry
		ok    bool
	}{
		"a his step with a command":  {Entry{Kind: KindHis, What: "Refresh the GitHub scope", Evidence: "his own sign-in", Command: "gh auth refresh -s user", Shell: "powershell"}, true},
		"a deploy with a command":    {Entry{Kind: KindDeploy, What: "acme", Evidence: "read", Command: "gh auth refresh", Shell: "powershell"}, false},
		"a command with no shell":    {Entry{Kind: KindHis, What: "Refresh the GitHub scope", Evidence: "his own sign-in", Command: "gh auth refresh -s user"}, false},
		"a command of another shell": {Entry{Kind: KindHis, What: "Refresh the GitHub scope", Evidence: "his own sign-in", Command: "gh auth refresh", Shell: "cmd"}, false},
		"a shell and no command":     {Entry{Kind: KindHis, What: "Refresh the GitHub scope", Evidence: "his own sign-in", Shell: "powershell"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			if _, _, err := TurnOn(dir, "the board", nil, night); err != nil {
				t.Fatal(err)
			}

			// Act
			_, err := Log(dir, tc.entry, night.Add(time.Minute))

			// Assert
			if (err == nil) != tc.ok {
				t.Errorf("Log = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestAStepsCardIsARunItemOnlyWhenTheStepIsACommand(t *testing.T) {
	step := stepAt(1, "Sign in to Vercel")
	command := step
	command.Command, command.Shell = "vercel login", "powershell"

	if got, want := HisCardItem(step), "question:afk-his-20261002T021100.000000000Z"; got != want {
		t.Errorf("a plain step's card = %q, want %q", got, want)
	}
	if got, want := HisCardItem(command), "run:afk-his-20261002T021100.000000000Z"; got != want {
		t.Errorf("a command's card = %q, want %q", got, want)
	}
}
