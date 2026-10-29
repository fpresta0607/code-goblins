package afk

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func stepAt(minute int, what string) Entry {
	return Entry{At: night.Add(time.Duration(minute) * time.Minute), Kind: KindHis, What: what, Link: "https://vercel.com/login", Evidence: "his own sign-in. Backlog row cg-sign-in"}
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

// A step only he can take is logged only with what its card does it with: a
// command he runs with one click, or the page he takes it on beside Done. On
// 2026-10-18 the Overlord pressed Done on install and merge steps logged with
// neither, and nothing ran, so a step with no page, and one whose words name
// a command, are refused with the flag that makes its card act.
func TestAStepIsLoggedOnlyWithWhatItsCardDoesItWith(t *testing.T) {
	for name, tc := range map[string]struct {
		entry   Entry
		refusal string
	}{
		"a page he takes it on": {Entry{Kind: KindHis, What: "Give your GitHub account access to the project's Vercel team again", Link: "https://vercel.com/teams/acme/settings/members", Evidence: "Vercel refuses every deployment by its Git author"}, ""},
		"a command he runs":     {Entry{Kind: KindHis, What: "Restart the shared no-mistakes daemon", Evidence: "the safety check refused it", Command: "no-mistakes daemon start", Shell: "powershell"}, ""},
		"no page":               {Entry{Kind: KindHis, What: "Install Code Goblins main-3278b62b, which replaces every earlier build today", Evidence: "His command: powershell -NoProfile -File update-candidate.ps1 -Short 3278b62b"}, "--link"},
		"a fleet command":       {Entry{Kind: KindHis, What: "Retire Kenny (cfo cleanup task-7), merge Teddy's no-mistakes PR 11", Link: "https://github.com/fpresta0607/no-mistakes/pull/11", Evidence: "the safety check refused each"}, "cfo cleanup"},
		"a PowerShell call":     {Entry{Kind: KindHis, What: "Install it: powershell -NoProfile -File C:\\dev\\code-goblins\\state\\ops\\update-candidate.ps1", Link: "https://example.com/build", Evidence: "built clean"}, "--run"},
		"a script":              {Entry{Kind: KindHis, What: "Run install-3278b62b.ps1 from his terminal", Link: "https://example.com/build", Evidence: "built clean"}, "install-3278b62b.ps1"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			if _, _, err := TurnOn(dir, "the board", nil, night); err != nil {
				t.Fatal(err)
			}

			// Act
			_, err := Log(dir, tc.entry, night.Add(time.Minute))
			check := CheckStep(tc.entry)

			// Assert
			if tc.refusal == "" {
				if err != nil || check != nil {
					t.Errorf("Log = %v, CheckStep = %v, want the step logged", err, check)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.refusal) || check == nil {
				t.Errorf("Log = %v, CheckStep = %v, want it refused naming %q", err, check, tc.refusal)
			}
		})
	}
}

// A step only he can take that is a decision of his offers its own choices,
// as cfo question does, and its card offers them in place of Done and Later:
// his answer goes to the CFO, which acts on it, so it needs no page and may
// name the command the CFO runs on his yes. It is never also a command he
// runs, and it offers real choices, one of them recommended or none.
func TestAStepOnlyHeCanTakeOffersChoicesOnlyAsADecisionOfHis(t *testing.T) {
	choices := []string{"Approve the repair", "Dry-run it again first", "Leave it as it is"}
	for name, tc := range map[string]struct {
		entry   Entry
		refusal string
	}{
		"three choices and a recommendation": {Entry{Kind: KindHis, What: "Approve the production repair of Georgetown", Link: "https://github.com/acme/app/issues/2012", Evidence: "a production repair is his", Options: choices, Recommendation: choices[1]}, ""},
		"choices and no page":                {Entry{Kind: KindHis, What: "Spend 40 credits on the corpus rebuild", Evidence: "a spend is his", Options: choices[:2]}, ""},
		"choices and a command in its words": {Entry{Kind: KindHis, What: "Close PR 140 with gh pr close 140", Evidence: "a teammate's pull request", Options: []string{"Close it", "Keep it open"}}, ""},
		"one choice":                         {Entry{Kind: KindHis, What: "Approve the repair", Link: "https://example.com", Evidence: "his", Options: choices[:1]}, "2 to 8 choices"},
		"the same choice twice":              {Entry{Kind: KindHis, What: "Approve the repair", Link: "https://example.com", Evidence: "his", Options: []string{"Approve", "Approve"}}, "distinct"},
		"a recommendation not among them":    {Entry{Kind: KindHis, What: "Approve the repair", Link: "https://example.com", Evidence: "his", Options: choices, Recommendation: "Approve"}, "one of its choices"},
		"a recommendation and no choices":    {Entry{Kind: KindHis, What: "Approve the repair", Link: "https://example.com", Evidence: "his", Recommendation: "Approve"}, "2 to 8 choices"},
		"choices and a command he runs":      {Entry{Kind: KindHis, What: "Restart the daemon", Evidence: "refused", Command: "no-mistakes daemon start", Shell: "powershell", Options: choices}, "--run or --option"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			if _, _, err := TurnOn(dir, "the board", nil, night); err != nil {
				t.Fatal(err)
			}

			// Act
			logged, err := Log(dir, tc.entry, night.Add(time.Minute))

			// Assert
			if tc.refusal == "" {
				if err != nil || !slices.Equal(logged.Options, tc.entry.Options) || logged.Recommendation != tc.entry.Recommendation || CheckStep(logged) != nil {
					t.Errorf("Log = %+v, %v, want the step logged with its choices", logged, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.refusal) {
				t.Errorf("Log = %v, want it refused naming %q", err, tc.refusal)
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
