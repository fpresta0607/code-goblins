package afk

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The stretch the Overlord ended at 19:45 UTC on 2026-10-10, whose report
// showed a second merge group, "Merge words with no merge recorded 36", under
// warning triangles. All 36 were lines the CFO wrote by hand after a landing,
// each naming a merge the log already held. testdata holds the stretch's lines
// as the live home's log holds them, except that this repository is public:
// the CFO's own words are numbered placeholders, one for each distinct text,
// and every repository but this one is a northwind one.
const recordedStretch = "afk-20261009T230830.982Z"

func recordedReport(t *testing.T) Report {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "afk-stretch-2026-10-10.audit"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, auditFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	entries, unreadable, err := Entries(dir, recordedStretch)
	if err != nil || unreadable != 0 || len(entries) == 0 {
		t.Fatalf("the recorded stretch reads %d lines, %d unreadable, %v", len(entries), unreadable, err)
	}
	return Report{Session: recordedStretch, Since: entries[0].At, Ended: entries[len(entries)-1].At, From: "the CFO at his ask", EndedFrom: "his own board", Decisions: Decisions(entries)}
}

// rendered is the report's text, or the test's end.
func rendered(t *testing.T, r Report) string {
	t.Helper()
	var out bytes.Buffer
	if err := Render(&out, r); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// titled is each heading of the report with how many it holds.
func titled(sections []Section) []string {
	var titles []string
	for _, section := range sections {
		titles = append(titles, section.Title+" "+strconv.Itoa(len(section.Entries)))
	}
	return titles
}

// following is the line of text after the first line that is line, or empty.
func following(text, line string) string {
	_, after, found := strings.Cut(text, line+"\n")
	if !found {
		return ""
	}
	next, _, _ := strings.Cut(after, "\n")
	return next
}

func TestTheStretchOf10OctoberReportsOneMergedGroupAndNoWarning(t *testing.T) {
	// Arrange
	report := recordedReport(t)

	// Act
	sections, text := report.Sections(), rendered(t, report)

	// Assert
	// The 8 decisions the CFO answered for him that stretch are recapped under
	// For you, and every merge is under one Merged heading.
	if got := titled(sections); !slices.Contains(got, "Merged 80") || !slices.Contains(got, "For you 8") {
		t.Fatalf("the stretch's headings = %q, want Merged 80 and For you 8", got)
	}
	merged := sections[slices.IndexFunc(sections, func(section Section) bool { return section.Title == "Merged" })]
	for _, section := range sections {
		for _, entry := range section.Entries {
			if entry.Kind == KindMerge && section.Title != "Merged" && section.Title != "Struck by the CFO" {
				t.Errorf("a merge is under %q, not Merged: %+v", section.Title, entry)
			}
		}
	}
	for _, entry := range merged.Entries {
		if entry.Outcome != OutcomeMerged {
			t.Errorf("under Merged: %+v, want only what merged", entry)
		}
	}
	for _, phrase := range []string{"Not merged", "No merge was recorded", "no outcome was recorded", "Merge words"} {
		if strings.Contains(text, phrase) {
			t.Errorf("the report says %q of a stretch where everything merged:\n%s", phrase, text)
		}
	}
	// 72 of the 80 carry what the CFO wrote of them: the 8 others are its own
	// merge words and a train it wrote nothing for.
	if noted := strings.Count(text, "\n  The CFO: words withheld "); noted != 72 {
		t.Errorf("%d merges carry the CFO's words, want 72", noted)
	}
	// Train 600 landed twice: 584 and 589, then 597. Each landing carries the
	// line the CFO wrote for it, never the other's.
	pull := func(number int) string {
		return following(text, "- https://github.com/fpresta0607/code-goblins/pull/"+strconv.Itoa(number)+": merged")
	}
	if first, second := pull(584), pull(597); !strings.HasPrefix(first, "  The CFO: ") || first != pull(589) || !strings.HasPrefix(second, "  The CFO: ") || first == second {
		t.Errorf("train 600's landings say %q, %q and %q, want 584 and 589 alike and 597 its own", first, pull(589), second)
	}
}

// A line the CFO wrote for a landing names a merge by the pull request that
// merged, as its link or as itself, or by the merge train's own pull request
// whose train landed it, logged before it or after. It is with that merge, in
// the CFO's words, and never a merge of its own.
func TestALineTheCFOWroteForALandingIsWithTheMergeItNames(t *testing.T) {
	const train = "https://github.com/acme/api/pull/900"
	pull := func(number int) string { return "https://github.com/acme/api/pull/" + strconv.Itoa(number) }
	at := func(minute int) time.Time { return night.Add(time.Duration(minute) * time.Minute) }
	landed := func(minute, number int, head string) Entry {
		return Entry{At: at(minute), Kind: KindMerge, What: pull(number), Link: pull(number), Evidence: "merge train acme-20261010-120000: CI passed on " + train + " at " + head + ", built on main at 1234567", Outcome: OutcomeMerged}
	}
	wrote := func(minute int, what, link, evidence string) Entry {
		return Entry{At: at(minute), Kind: KindMerge, What: what, Link: link, Evidence: evidence}
	}
	for name, tc := range map[string]struct {
		lines []Entry
		want  map[int]string
	}{
		"it links the pull request that merged": {
			[]Entry{
				{At: at(1), Kind: KindMerge, What: pull(10), Link: pull(10), Evidence: "verified: 15 checks green"},
				{At: at(2), Kind: KindMerge, What: pull(10), Link: pull(10), Evidence: "gh pr merge", Outcome: OutcomeMerged},
				wrote(3, "PR 10 (the export) merged", pull(10), "CI passed at abc1234."),
			},
			map[int]string{10: "PR 10 (the export) merged. CI passed at abc1234."},
		},
		"it is the pull request that merged": {
			[]Entry{landed(1, 10, "abc1234"), wrote(2, pull(10), "", "the train's run passed")},
			map[int]string{10: "the train's run passed."},
		},
		"its first merge word failed and a second merged it": {
			[]Entry{
				{At: at(1), Kind: KindMerge, What: pull(10), Link: pull(10), Evidence: "verified: 15 checks green"},
				{At: at(2), Kind: KindMerge, What: pull(10), Link: pull(10), Evidence: "gh pr merge", Outcome: "not merged: gh exited 1: Pull request is not mergeable"},
				{At: at(9), Kind: KindMerge, What: pull(10), Link: pull(10), Evidence: "verified: head holds main's tip"},
				{At: at(10), Kind: KindMerge, What: pull(10), Link: pull(10), Evidence: "gh pr merge", Outcome: OutcomeMerged},
			},
			map[int]string{10: "verified: 15 checks green. Not merged: gh exited 1: Pull request is not mergeable."},
		},
		"it links the train that landed two": {
			[]Entry{landed(1, 10, "abc1234"), landed(1, 11, "abc1234"), wrote(2, "PRs 10 and 11 landed on merge train 900", train, "One CI run.")},
			map[int]string{10: "PRs 10 and 11 landed on merge train 900. One CI run.", 11: "PRs 10 and 11 landed on merge train 900. One CI run."},
		},
		"it was written before the train's line": {
			[]Entry{wrote(1, "PR 10 landed on merge train 900", train, "One CI run."), landed(2, 10, "abc1234")},
			map[int]string{10: "PR 10 landed on merge train 900. One CI run."},
		},
		"the train landed twice under one pull request": {
			[]Entry{
				landed(1, 10, "abc1234"), landed(1, 11, "abc1234"),
				wrote(2, "PRs 10 and 11 landed on merge train 900", train, "The second run."),
				landed(20, 12, "def5678"),
				wrote(21, "PR 12 landed on merge train 900", train, "The third run."),
			},
			map[int]string{10: "PRs 10 and 11 landed on merge train 900. The second run.", 11: "PRs 10 and 11 landed on merge train 900. The second run.", 12: "PR 12 landed on merge train 900. The third run."},
		},
		"two lines for one merge": {
			[]Entry{landed(1, 10, "abc1234"), wrote(2, "PR 10 landed", train, "One CI run."), wrote(3, "PR 10 is the export", pull(10), "Deployed nothing.")},
			map[int]string{10: "PR 10 landed. One CI run. PR 10 is the export. Deployed nothing."},
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			report := Report{Session: "afk-1", Since: night, Ended: at(30), From: "the board", EndedFrom: "the board", Decisions: Decisions(tc.lines)}

			// Act
			sections, text := report.Sections(), rendered(t, report)

			// Assert
			if got, want := titled(sections), []string{"Merged " + strconv.Itoa(len(tc.want))}; strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("headings = %q, want %q:\n%s", got, want, text)
			}
			for number, words := range tc.want {
				if got := following(text, "- "+pull(number)+": merged"); got != "  The CFO: "+words {
					t.Errorf("#%d says %q, want the CFO's words %q:\n%s", number, got, words, text)
				}
			}
		})
	}
}

// What did not land is the one warning in the report: under Not merged, each
// with what the CFO did about it, then what happened, each in one sentence.
// The CFO's own line about it is what it did, a step only the Overlord can
// take about it says it needs him, and with neither the report says the CFO
// logged nothing. A line the CFO wrote that names no merge says what it says
// itself.
func TestANotMergedRowSaysWhatTheCFODidAndWhatHappened(t *testing.T) {
	pr := "https://github.com/acme/api/pull/13"
	word := Entry{At: night, Kind: KindMerge, What: pr, Link: pr, Evidence: "verified: 15 checks green"}
	ended := func(outcome string) Entry {
		return Entry{At: night.Add(time.Minute), Kind: KindMerge, What: pr, Link: pr, Evidence: "gh pr merge", Outcome: outcome}
	}
	failed := ended("not merged: gh exited 1: Pull request is not mergeable")
	sentBack := Entry{At: night.Add(2 * time.Minute), Kind: KindMerge, What: "Sent it back to nw-export to merge main in", Link: pr, Evidence: "Its head was behind main."}
	his := Entry{At: night.Add(3 * time.Minute), Kind: KindHis, What: "Merge #13, a teammate's pull request", Link: pr, Evidence: "cfo pr merge refused it"}
	for name, tc := range map[string]struct {
		lines []Entry
		says  []string
	}{
		"the CFO sent it back":      {[]Entry{word, failed, sentBack}, []string{"The CFO: Sent it back to nw-export to merge main in. Its head was behind main.", "Not merged: gh exited 1: Pull request is not mergeable."}},
		"only he can take it":       {[]Entry{word, failed, his}, []string{"The CFO: It needs you: Merge #13, a teammate's pull request.", "Not merged: gh exited 1: Pull request is not mergeable."}},
		"the CFO logged nothing":    {[]Entry{word, failed}, []string{"The CFO logged nothing about it.", "Not merged: gh exited 1: Pull request is not mergeable."}},
		"it joined a merge queue":   {[]Entry{word, ended("added to the merge queue")}, []string{"The CFO logged nothing about it.", "Added to the merge queue."}},
		"nothing followed its word": {[]Entry{word}, []string{"The CFO logged nothing about it.", "No merge was recorded."}},
		"the CFO wrote of no merge": {[]Entry{{At: night, Kind: KindMerge, What: "PR 13 landed on merge train 900", Link: pr, Evidence: "One CI run."}}, []string{"No merge was recorded."}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			report := Report{Session: "afk-1", Since: night, Ended: night.Add(time.Hour), From: "the board", EndedFrom: "the board", Decisions: Decisions(tc.lines)}

			// Act
			sections, text := report.Sections(), rendered(t, report)

			// Assert
			if got := titled(sections); len(got) == 0 || got[0] != "Not merged 1" {
				t.Fatalf("headings = %q, want Not merged 1 first:\n%s", got, text)
			}
			_, under, _ := strings.Cut(text, "Not merged (1)\n")
			lines := strings.Split(under, "\n")
			for i, says := range tc.says {
				if i+1 >= len(lines) || lines[i+1] != "  "+says {
					t.Errorf("line %d under it is not %q:\n%s", i+1, "  "+says, text)
				}
			}
		})
	}
}

// The report kept for a stretch is folded when it is read, so a strike the CFO
// makes after the stretch ended still takes its line off the merge it names.
func TestAKeptReportFoldsTheCFOsLinesAsTheyStandWhenItIsRead(t *testing.T) {
	// Arrange
	dir, pr := t.TempDir(), "https://github.com/acme/api/pull/10"
	if _, _, err := TurnOn(dir, "the board", nil, night); err != nil {
		t.Fatal(err)
	}
	merged, err := Log(dir, Entry{Kind: KindMerge, What: pr, Link: pr, Evidence: "merge train acme-1: CI passed on https://github.com/acme/api/pull/900 at abc1234, built on main at 1234567", Outcome: OutcomeMerged}, night.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	mistaken, err := Log(dir, Entry{Kind: KindMerge, What: "PR 11 landed", Link: pr, Evidence: "the wrong pull request"}, night.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Log(dir, Entry{Kind: KindMerge, What: "PR 10 landed", Link: pr, Evidence: "One CI run."}, night.Add(3*time.Minute)); err != nil {
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
	if _, err := Strike(dir, mistaken.At, "it names PR 11, which did not ride", night.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Act
	kept, found, err := ReadReport(dir)

	// Assert
	if err != nil || !found {
		t.Fatal(err, found)
	}
	text := rendered(t, kept)
	if got := titled(kept.Sections()); strings.Join(got, ",") != "Merged 1,Struck by the CFO 1" {
		t.Errorf("headings = %q, want the merge and the struck line:\n%s", got, text)
	}
	if got := following(text, "- "+merged.What+": merged"); got != "  The CFO: PR 10 landed. One CI run." {
		t.Errorf("the merge says %q, want only the line the CFO stands by:\n%s", got, text)
	}
}

// A decision the CFO answered itself about a merge that did not land is what
// the CFO did about it: the merge's row says it with the CFO's answer, nothing
// is left for him, and the decision is recapped under For you.
func TestANotMergedRowSaysWhatTheCFOAnsweredAboutIt(t *testing.T) {
	// Arrange
	pr := "https://github.com/acme/api/pull/13"
	word := Entry{At: night, Kind: KindMerge, What: pr, Link: pr, Evidence: "verified: 15 checks green"}
	failed := Entry{At: night.Add(time.Minute), Kind: KindMerge, What: pr, Link: pr, Evidence: "gh pr merge", Outcome: "not merged: gh exited 1: Pull request is not mergeable"}
	decided := Entry{At: night.Add(3 * time.Minute), Kind: KindLeft, What: "Merge #13 past its failing check?", Link: pr, Evidence: "the check fails on main too", Diagnosis: "a flaky check", Tried: "two reruns", Options: []string{"Hold it", "Merge it"}, Answer: "Hold it"}
	report := Report{Session: "afk-1", Since: night, Ended: night.Add(time.Hour), From: "the board", EndedFrom: "the board", Decisions: Decisions([]Entry{word, failed, decided})}

	// Act
	sections, text := report.Sections(), rendered(t, report)

	// Assert
	if got := titled(sections); strings.Join(got, ",") != "Not merged 1,For you 1" {
		t.Fatalf("headings = %q, want the merge not merged and the decision recapped:\n%s", got, text)
	}
	if got, want := following(text, "- "+pr), "  The CFO: Merge #13 past its failing check? the check fails on main too. It answered: Hold it."; got != want {
		t.Errorf("the merge's row says %q, want %q:\n%s", got, want, text)
	}
}
