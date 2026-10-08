package afk

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestADecisionsLaterLineGivesItItsOutcome(t *testing.T) {
	pr := "https://github.com/acme/api/pull/12"
	entries := []Entry{
		{Kind: KindOn, What: "the board"},
		{Kind: KindMerge, What: pr, Evidence: "gate run 41 passed"},
		{Kind: KindHeld, Item: "question:drop-legacy", What: "Apply migration 0042?"},
		{Kind: KindDeploy, What: "acme production", Evidence: "/health reads 200"},
		{Kind: KindMerge, What: pr, Outcome: "merged"},
		{Kind: KindMerge, What: "https://github.com/acme/api/pull/13", Evidence: "verified locally"},
		{Kind: KindOff, What: "the board"},
	}

	decisions := Decisions(entries)

	if len(decisions) != 3 {
		t.Fatalf("Decisions = %+v, want the two merge words and the deploy", decisions)
	}
	if decisions[0].What != pr || decisions[0].Outcome != "merged" || decisions[0].Evidence != "gate run 41 passed" {
		t.Errorf("first decision = %+v, want the merge word with its evidence and its outcome", decisions[0])
	}
	if decisions[1].Kind != KindDeploy || decisions[2].Outcome != "" {
		t.Errorf("decisions = %+v, want the deploy, then a merge word with no outcome recorded", decisions)
	}
}

func TestAnOutcomeWithNoDecisionBeforeItIsKeptAsItsOwnLine(t *testing.T) {
	decisions := Decisions([]Entry{{Kind: KindMerge, What: "https://github.com/acme/api/pull/9", Outcome: "merged"}})

	if len(decisions) != 1 || decisions[0].Outcome != "merged" {
		t.Fatalf("Decisions = %+v, want the outcome kept rather than dropped", decisions)
	}
}

func nightReport() Report {
	return Report{
		Session: "afk-1", Since: night, Ended: night.Add(10*time.Hour + 21*time.Minute),
		From: "his own terminal (powershell.exe pid 4242)", EndedFrom: "his own terminal (powershell.exe pid 5151)",
		Decisions: []Entry{
			{Kind: KindMerge, What: "https://github.com/acme/api/pull/12", Link: "https://github.com/acme/api/pull/12", Evidence: "gate run 41 passed and its test output was read; head abc1234; 7 checks green", Outcome: "merged"},
			{Kind: KindMerge, What: "https://github.com/acme/api/pull/13", Evidence: "verified locally"},
			{Kind: KindDeploy, What: "acme production", Link: "https://acme.example/health", Evidence: "/health reads 200 with commit abc1234"},
			{Kind: KindAnswer, What: "notify-pd-billing-12", Task: "pd-billing", Evidence: "asked: Which store? answered: SQLite"},
		},
		Finished: []Finish{{Task: "cg-board-polish", PR: "https://github.com/acme/board/pull/270", At: night.Add(64 * time.Minute)}},
		Held: []Held{
			{Item: "question:drop-legacy", What: "Migration 0042 drops legacy_invoices. Apply it?", At: night.Add(30 * time.Minute), Waiting: true, Now: "still waiting on you", Meanwhile: "pd-billing is working on the invoice export"},
			{Item: "review:waiting-pd-auth-7", Task: "pd-auth", What: "Waiting on you: sign in to Vercel", At: night.Add(40 * time.Minute), Now: "withdrawn: pd-auth reported again"},
		},
		Before: []Allowance{{Provider: "claude", Window: "week", PercentUsed: 40, ResetsAt: night.Add(120 * time.Hour)}},
		After:  []Allowance{{Provider: "claude", Window: "week", PercentUsed: 47, ResetsAt: night.Add(120 * time.Hour)}},
		Notes:  []string{"1 line of the log could not be read"},
	}
}

func TestTheReportSaysWhatWasDecidedFinishedHeldAndSpent(t *testing.T) {
	var out bytes.Buffer
	if err := Render(&out, nightReport()); err != nil {
		t.Fatal(err)
	}
	report := out.String()

	for name, phrase := range map[string]string{
		"its span":                      "2026-10-02 02:10 UTC to 2026-10-02 12:31 UTC (10h21m)",
		"where it turned on and off":    "on from his own terminal (powershell.exe pid 4242), off from his own terminal (powershell.exe pid 5151)",
		"the merge with its link":       "https://github.com/acme/api/pull/12",
		"the merge's outcome":           "merged",
		"the merge's verification":      "gate run 41 passed and its test output was read; head abc1234; 7 checks green",
		"a merge word with no result":   "no outcome was recorded",
		"the deploy with its link":      "acme production (https://acme.example/health)",
		"the deploy's verification":     "/health reads 200 with commit abc1234",
		"nothing installed":             "Installed (0)",
		"no migration":                  "Migrations applied (0)",
		"the answer and its goblin":     "pd-billing",
		"what a goblin finished":        "cg-board-polish: https://github.com/acme/board/pull/270",
		"what is held":                  "Migration 0042 drops legacy_invoices. Apply it?",
		"that it still waits":           "still waiting on you",
		"what was done meanwhile":       "pd-billing is working on the invoice export",
		"a held item that closed":       "withdrawn: pd-auth reported again",
		"what was spent":                "claude week: 40% used when it turned on, 47% when it turned off (7 points)",
		"what it could not read":        "1 line of the log could not be read",
		"how many were held":            "Held for you (2)",
		"how many merged":               "Merged (1)",
		"merge words that did not land": "Merge words with no merge recorded (1)",
	} {
		if !strings.Contains(report, phrase) {
			t.Errorf("the report does not say %s (%q):\n%s", name, phrase, report)
		}
	}
	if strings.ContainsRune(report, 0x2014) {
		t.Error("the report uses an em dash")
	}
	if !strings.Contains(report, "- https://github.com/acme/api/pull/12: merged") {
		t.Errorf("the report repeats a link that is the decision's own subject:\n%s", report)
	}
}

// A held item says what was recommended for it and by whom, the CFO for its
// own or the goblin whose question it is: in the present while it still waits
// on the Overlord, in the past once it does not, and nothing for an item with
// no recommendation.
func TestAHeldItemSaysWhatWasRecommendedForIt(t *testing.T) {
	// Arrange
	report := Report{Session: "afk-1", Since: night, Ended: night.Add(time.Hour), From: "the board", EndedFrom: "the board", Held: []Held{
		{Item: "question:drop-legacy", What: "Migration 0042 drops legacy_invoices. Apply it?", Waiting: true, Now: "still waiting on you", Recommendation: "Keep it held"},
		{Item: "question:notify-pd-billing-12", Task: "pd-billing", What: "Delete fix/tax-rounding-v1?", Now: "you answered it: Delete it", Recommendation: "Keep the branch."},
		{Item: "run:restart-db", What: "Restart the dev database", Waiting: true, Now: "still waiting for you to run it"},
	}}
	var out bytes.Buffer

	// Act
	err := Render(&out, report)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"  The CFO recommends: Keep it held.\n", "  pd-billing recommended: Keep the branch.\n"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("the report does not say %q:\n%s", line, out.String())
		}
	}
	if count := strings.Count(out.String(), "recommend"); count != 2 {
		t.Errorf("the report says a recommendation %d times, want 2: the run had none:\n%s", count, out.String())
	}
}

// The goblins paused at a floor are listed after what the CFO decided, under
// their own heading, and only in a stretch that paused one.
func TestTheReportListsTheGoblinsPausedAtAFloor(t *testing.T) {
	// Arrange
	report := nightReport()
	report.Paused = []Entry{{At: night.Add(3 * time.Hour), Kind: KindPause, Task: "nw-search-index", What: "at the memory floor", Evidence: "3.1 GB of memory and 6.2 GB of commit free on two readings in a row, under the 4 GB floor", Outcome: "paused"}}
	var out bytes.Buffer

	// Act
	sections, err := report.Sections(), Render(&out, report)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if last := sections[len(sections)-1]; last.Title != "Paused at a floor" || len(last.Entries) != 1 || last.Entries[0].Task != "nw-search-index" {
		t.Errorf("the last heading = %+v, want the goblin paused at the memory floor", last)
	}
	for _, line := range []string{"Paused at a floor (1)\n", "- nw-search-index: at the memory floor: paused\n", "  Evidence: 3.1 GB of memory and 6.2 GB of commit free on two readings in a row, under the 4 GB floor\n"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("the report does not say %q:\n%s", line, out.String())
		}
	}
	for _, section := range nightReport().Sections() {
		if section.Title == "Paused at a floor" {
			t.Errorf("a stretch that paused nothing lists %+v", section)
		}
	}
}

func TestAQuietStretchReportsNothingRatherThanNothingAtAll(t *testing.T) {
	var out bytes.Buffer
	if err := Render(&out, Report{Session: "afk-1", Since: night, Ended: night.Add(time.Hour), From: "the board", EndedFrom: "the board"}); err != nil {
		t.Fatal(err)
	}

	for _, phrase := range []string{"Merged (0)", "Deployed (0)", "Goblins finished (0)", "Held for you (0)"} {
		if !strings.Contains(out.String(), phrase) {
			t.Errorf("a quiet stretch's report does not say %q:\n%s", phrase, out.String())
		}
	}
}

// Spent says what was used. An allowance at 0% wherever it was read, and a
// reading that could not be taken, are left out rather than written as
// nothing or as not read, and a stretch that used nothing has no Spent at all.
// The stretch is the one the Overlord ended at 01:08 UTC on 2026-10-08, whose
// reading when it turned off came back empty.
func TestSpentLeavesOutWhatWasNotUsedAndWhatWasNotRead(t *testing.T) {
	// Arrange
	reset := night.Add(5 * 24 * time.Hour)
	stretch := Report{Session: "afk-1", Since: night, Ended: night.Add(80 * time.Minute), From: "the board", EndedFrom: "the board", Before: []Allowance{
		{Provider: "claude", Window: "session", PercentUsed: 42, ResetsAt: night.Add(time.Hour)},
		{Provider: "claude", Window: "week", PercentUsed: 29, ResetsAt: reset},
		{Provider: "claude", Window: "Fable week", ResetsAt: reset},
		{Provider: "codex", Window: "week", PercentUsed: 1, ResetsAt: reset},
		{Provider: "codex", Window: "credits", Credits: true, Remaining: 25849.6, Unit: "credits"},
	}}
	unused := Report{Session: "afk-2", Since: night, Ended: night.Add(time.Hour), From: "the board", EndedFrom: "the board",
		Before: []Allowance{{Provider: "claude", Window: "Fable week", ResetsAt: reset}},
		After:  []Allowance{{Provider: "claude", Window: "Fable week", ResetsAt: reset}, {Provider: "codex", Window: "credits", Credits: true, Remaining: 12, Unit: "credits"}},
	}
	var out, quiet bytes.Buffer

	// Act
	err := errors.Join(Render(&out, stretch), Render(&quiet, unused))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Spent\n- claude session: 42% used when it turned on\n- claude week: 29% used when it turned on\n- codex week: 1% used when it turned on\n") {
		t.Errorf("Spent does not list the three allowances used, each as it was read:\n%s", out.String())
	}
	for _, left := range []string{"not read", "Fable week", "credits"} {
		if strings.Contains(out.String(), left) {
			t.Errorf("the report says %q, which was not used or not read:\n%s", left, out.String())
		}
	}
	if strings.Contains(quiet.String(), "Spent") || strings.Contains(quiet.String(), "allowance") {
		t.Errorf("a stretch that used nothing has a Spent section:\n%s", quiet.String())
	}
}

func TestSpendIsReadFromTheTwoAllowanceReadings(t *testing.T) {
	reset := night.Add(3 * time.Hour)
	percent := func(value float64) *float64 { return &value }
	for name, tc := range map[string]struct {
		before, after []Allowance
		want          []Used
		says          string
	}{
		"a window that kept running": {
			[]Allowance{{Provider: "claude", Window: "week", PercentUsed: 40, ResetsAt: reset}},
			[]Allowance{{Provider: "claude", Window: "week", PercentUsed: 47.5, ResetsAt: reset.Add(300 * time.Millisecond)}},
			[]Used{{Provider: "claude", Window: "week", On: percent(40), Off: percent(47.5)}},
			"claude week: 40% used when it turned on, 47.5% when it turned off (7.5 points)",
		},
		// quota-axi works a window's reset time out again at every reading, so
		// the same window's reads a second apart, and across a minute's edge.
		"a window whose reset time moved by a moment": {
			[]Allowance{{Provider: "claude", Window: "session", PercentUsed: 54, ResetsAt: reset.Add(100 * time.Millisecond)}},
			[]Allowance{{Provider: "claude", Window: "session", PercentUsed: 55, ResetsAt: reset.Add(-900 * time.Millisecond)}},
			[]Used{{Provider: "claude", Window: "session", On: percent(54), Off: percent(55)}},
			"claude session: 54% used when it turned on, 55% when it turned off (1 point)",
		},
		"a window that reset in between": {
			[]Allowance{{Provider: "claude", Window: "session", PercentUsed: 80, ResetsAt: reset}},
			[]Allowance{{Provider: "claude", Window: "session", PercentUsed: 12, ResetsAt: reset.Add(5 * time.Hour)}},
			[]Used{{Provider: "claude", Window: "session", On: percent(80), Off: percent(12), Reset: true}},
			"claude session: 80% used when it turned on, 12% when it turned off, after the window reset",
		},
		"a window used from nothing": {
			[]Allowance{{Provider: "claude", Window: "Fable week", ResetsAt: reset}},
			[]Allowance{{Provider: "claude", Window: "Fable week", PercentUsed: 3, ResetsAt: reset}},
			[]Used{{Provider: "claude", Window: "Fable week", On: percent(0), Off: percent(3)}},
			"claude Fable week: 0% used when it turned on, 3% when it turned off (3 points)",
		},
		"credits": {
			[]Allowance{{Provider: "codex", Window: "credits", Credits: true, Remaining: 120, Unit: "credits"}},
			[]Allowance{{Provider: "codex", Window: "credits", Credits: true, Remaining: 95, Unit: "credits"}},
			[]Used{{Provider: "codex", Window: "credits", Credits: true, Spent: 25, Unit: "credits"}},
			"codex credits: 25 credits spent",
		},
		"read only when it turned on": {
			[]Allowance{{Provider: "claude", Window: "week", PercentUsed: 40, ResetsAt: reset}},
			nil,
			[]Used{{Provider: "claude", Window: "week", On: percent(40)}},
			"claude week: 40% used when it turned on",
		},
		"read only when it turned off": {
			nil,
			[]Allowance{{Provider: "claude", Window: "week", PercentUsed: 47, ResetsAt: reset}},
			[]Used{{Provider: "claude", Window: "week", Off: percent(47)}},
			"claude week: 47% used when it turned off",
		},
		"a window at 0% at both ends": {
			[]Allowance{{Provider: "claude", Window: "Fable week", ResetsAt: reset}},
			[]Allowance{{Provider: "claude", Window: "Fable week", PercentUsed: 0.04, ResetsAt: reset}},
			nil, "",
		},
		"unlimited credits": {
			[]Allowance{{Provider: "codex", Window: "credits", Credits: true, Unlimited: true}},
			[]Allowance{{Provider: "codex", Window: "credits", Credits: true, Unlimited: true}},
			nil, "",
		},
		"credits that rose": {
			[]Allowance{{Provider: "codex", Window: "credits", Credits: true, Remaining: 95, Unit: "credits"}},
			[]Allowance{{Provider: "codex", Window: "credits", Credits: true, Remaining: 120, Unit: "credits"}},
			nil, "",
		},
		"credits read at one end": {
			[]Allowance{{Provider: "codex", Window: "credits", Credits: true, Remaining: 95, Unit: "credits"}},
			nil,
			nil, "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			rows := Spent(tc.before, tc.after)

			if !reflect.DeepEqual(rows, tc.want) {
				t.Fatalf("Spent = %+v, want %+v", rows, tc.want)
			}
			if len(rows) == 1 && rows[0].says() != tc.says {
				t.Errorf("the row says %q, want %q", rows[0].says(), tc.says)
			}
		})
	}
}

func TestTheReportIsKeptForTheStretchItEnds(t *testing.T) {
	dir := t.TempDir()
	if _, found, err := ReadReport(dir); err != nil || found {
		t.Fatalf("ReadReport in a home with no report = %v, %v, want none", found, err)
	}

	if err := SaveReport(dir, nightReport()); err != nil {
		t.Fatal(err)
	}

	report, found, err := ReadReport(dir)
	if err != nil || !found || report.Session != "afk-1" || len(report.Decisions) != 4 || len(report.Held) != 2 || !report.Ended.Equal(nightReport().Ended) {
		t.Errorf("ReadReport = %+v, %v, %v, want the report that was saved", report, found, err)
	}
}

// The report's decisions sit under the same headings in the CFO's text and on
// the board's page. A heading the report always shows is there with nothing
// under it, so a night with no deploy says so; the others are there only when
// they hold something.
func TestTheReportsDecisionsSitUnderItsHeadings(t *testing.T) {
	// Act
	night, quiet := nightReport().Sections(), Report{}.Sections()

	// Assert
	titled := func(sections []Section) []string {
		var titles []string
		for _, section := range sections {
			titles = append(titles, section.Title+" "+strconv.Itoa(len(section.Entries)))
		}
		return titles
	}
	if got, want := titled(night), []string{"Merged 1", "Merge words with no merge recorded 1", "Deployed 1", "Migrations applied 0", "Installed 0", "Answered for goblins 1"}; !slices.Equal(got, want) {
		t.Errorf("the night's headings = %q, want %q", got, want)
	}
	if merged := night[0].Entries[0]; merged.What != "https://github.com/acme/api/pull/12" || merged.Outcome != OutcomeMerged {
		t.Errorf("under Merged = %+v, want the merge word whose merge was recorded", merged)
	}
	if unmerged := night[1].Entries[0]; unmerged.What != "https://github.com/acme/api/pull/13" || unmerged.Outcome != "" {
		t.Errorf("under Merge words with no merge recorded = %+v, want the one with no outcome", unmerged)
	}
	if got, want := titled(quiet), []string{"Merged 0", "Deployed 0", "Migrations applied 0", "Installed 0", "Answered for goblins 0"}; !slices.Equal(got, want) {
		t.Errorf("a quiet stretch's headings = %q, want %q", got, want)
	}
	for _, section := range quiet {
		if section.Entries == nil {
			t.Errorf("%s has no list of entries, want an empty one: the board's page reads it as a list", section.Title)
		}
	}
	if lasted := nightReport().Lasted(); lasted != "10h21m" {
		t.Errorf("Lasted = %q, want 10h21m", lasted)
	}
}
