package afk

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

const reportFile = "afk-report.json"

// OutcomeMerged is the outcome of a merge word whose pull request merged.
const OutcomeMerged = "merged"

// sameWindow is how far apart two readings of one window's reset time may
// be. quota-axi works the time out again at every reading, so it moves by a
// moment; a window that reset moves by its whole length, hours at least.
const sameWindow = 10 * time.Minute

// Report is what a stretch of AFK mode comes to when it turns off: what the
// CFO decided under the authority with the evidence of each, what each goblin
// finished, what is held for the Overlord and why, and what was spent, of the
// allowance and of the disk.
type Report struct {
	Session string    `json:"session"`
	Since   time.Time `json:"since"`
	Ended   time.Time `json:"ended"`
	// From and EndedFrom say who turned it on and off, and Asked and
	// EndedAsked hold the Overlord's words for a switch the CFO made at his
	// ask, as the switch kept them.
	From       string `json:"from"`
	Asked      string `json:"asked,omitempty"`
	EndedFrom  string `json:"ended_from"`
	EndedAsked string `json:"ended_asked,omitempty"`
	// Decisions are the stretch's decisions in the order they were made, and
	// Paused the goblins the supervisor paused at a floor, each with what the
	// pause stood on and how it went.
	Decisions []Entry `json:"decisions"`
	Paused    []Entry `json:"paused"`
	// Struck are the strikes made in the stretch of lines an earlier stretch
	// logged: its own lines struck are among Decisions, marked.
	Struck   []Entry  `json:"struck,omitempty"`
	Finished []Finish `json:"finished"`
	Held     []Held   `json:"held"`
	// Before and After are the allowance read when AFK mode turned on and
	// when it turned off.
	Before []Allowance `json:"before"`
	After  []Allowance `json:"after"`
	// DiskBefore and DiskAfter are the free disk read when AFK mode turned on
	// and when it turned off, each nil for a reading not taken.
	DiskBefore *disk.Reading `json:"disk_before,omitempty"`
	DiskAfter  *disk.Reading `json:"disk_after,omitempty"`
	// Notes say what the report could not read.
	Notes []string `json:"notes,omitempty"`
}

// Finish is a pull request a goblin reported done during the stretch.
type Finish struct {
	Task string    `json:"task"`
	PR   string    `json:"pr"`
	At   time.Time `json:"at"`
}

// Held is an item that waited on the Overlord in the Command Center during
// the stretch, such as a question asked of him before it began.
type Held struct {
	// Item is its key in the Command Center, Task its goblin, empty for the
	// CFO's own, and What the question or request itself, which says why it
	// is his.
	Item string    `json:"item"`
	Task string    `json:"task,omitempty"`
	What string    `json:"what"`
	At   time.Time `json:"at"`
	// Waiting says it still waits on him, and Now what became of it: by the
	// time AFK mode turned off as the report is kept, and as it stands now
	// when the report is read.
	Waiting bool   `json:"waiting"`
	Now     string `json:"now"`
	// Meanwhile is what was done while it waited: its goblin's latest report.
	Meanwhile string `json:"meanwhile,omitempty"`
	// Recommendation is the choice recommended for it by whoever asked it,
	// empty when nothing was.
	Recommendation string `json:"recommendation,omitempty"`
	// Settled is what became of a line the CFO left for him and settled in
	// the stretch, as a report kept before nothing was left for him held it:
	// it was never asked, so nothing read later changes it.
	Settled string `json:"settled,omitempty"`
}

// Recommends says what was recommended for a held item and by whom: the CFO
// for its own item, or task, the goblin whose question it is. It is in the
// present while the item still waits on the Overlord, in the past once it
// does not, and empty when nothing was recommended.
func Recommends(task, recommendation string, waiting bool) string {
	if recommendation == "" {
		return ""
	}
	who, verb := "The CFO", " recommended: "
	if task != "" {
		who = task
	}
	if waiting {
		verb = " recommends: "
	}
	return who + verb + strings.TrimSuffix(recommendation, ".") + "."
}

// Decisions folds a stretch's log lines into its decisions, in the order they
// were made: a later line that carries an outcome closes the decision logged
// before it for the same kind, subject and goblin, a strike marks the
// decision it names struck, with its reason, and a settle marks the line left
// for him that it names settled, with what became of it.
func Decisions(entries []Entry) []Entry {
	return marked(folded(entries, DecisionKinds), entries)
}

// marked is decisions with each one a strike among entries names marked
// struck, and each one a settle names marked settled.
func marked(decisions, entries []Entry) []Entry {
	for _, line := range entries {
		if line.Kind != KindStrike && line.Kind != KindSettle {
			continue
		}
		for i, decision := range decisions {
			switch {
			case decision.At.UTC().Format(time.RFC3339Nano) != line.Item:
			case line.Kind == KindStrike:
				decisions[i].Struck = line.Evidence
			default:
				decisions[i].Settled = line.Evidence
			}
		}
	}
	return decisions
}

// StruckEarlier is the strikes among a stretch's log lines of lines an
// earlier stretch logged, each with the reason as Struck, so the stretch's
// report shows them struck.
func StruckEarlier(entries []Entry) []Entry {
	logged := map[string]bool{}
	for _, entry := range entries {
		if slices.Contains(DecisionKinds, entry.Kind) {
			logged[entry.At.UTC().Format(time.RFC3339Nano)] = true
		}
	}
	earlier := []Entry{}
	for _, strike := range entries {
		if strike.Kind == KindStrike && !logged[strike.Item] {
			strike.Struck = strike.Evidence
			earlier = append(earlier, strike)
		}
	}
	return earlier
}

// Pauses folds a stretch's log lines into the goblins the supervisor paused
// at a floor, each closed by the line that says how its pause went.
func Pauses(entries []Entry) []Entry {
	return folded(entries, []string{KindPause})
}

// folded is the lines of kinds, each with its outcome folded in.
func folded(entries []Entry, kinds []string) []Entry {
	var lines []Entry
	for _, entry := range entries {
		if !slices.Contains(kinds, entry.Kind) {
			continue
		}
		if entry.Outcome != "" {
			open := -1
			for i, prior := range lines {
				if prior.Kind == entry.Kind && prior.What == entry.What && prior.Task == entry.Task && prior.Outcome == "" {
					open = i
				}
			}
			if open >= 0 {
				lines[open].Outcome = entry.Outcome
				continue
			}
		}
		lines = append(lines, entry)
	}
	return lines
}

// trainLanding is how the line a merge train logs for a pull request it
// landed begins, as train.Evidence writes it: the train's own pull request
// follows its id.
var trainLanding = regexp.MustCompile(`^merge train \S+: CI passed on (\S+) at `)

// Noted is decisions with each line the CFO wrote of a merge folded into the
// merge it names, so a merge is told once and a warning is never a merge that
// landed. A merge line that did not land names a merge when it links, or is,
// the pull request that merged, or the merge train's own pull request whose
// train landed it: a line the CFO wrote for the landing, or a merge word that
// failed before another landed it. Its words go to the merges it names that
// landed before it and carry no earlier line of the CFO's, so a train that
// landed twice under one pull request gives each line its own landing, or
// else to every merge it names. A line the CFO wrote that links a pull request
// that did not merge goes the same way to that merge, as what the CFO did about
// it, and a decision the CFO answered in the Overlord's place that links it
// gives it its words and the CFO's answer, and a step only he can take that
// links it says it needs him, each staying a line of its own. A struck line,
// and one that names no merge, stays as it was.
func Noted(decisions []Entry) []Entry {
	names := func(line, merge Entry) bool {
		train := trainLanding.FindStringSubmatch(merge.Evidence)
		for _, subject := range []string{line.Link, line.What} {
			if subject != "" && (subject == merge.What || train != nil && subject == train[1]) {
				return true
			}
		}
		return false
	}
	landed := func(merge Entry) bool {
		return merge.Kind == KindMerge && merge.Outcome == OutcomeMerged && merge.Struck == ""
	}
	open := func(merge Entry) bool {
		return merge.Kind == KindMerge && merge.Outcome != OutcomeMerged && merge.Struck == ""
	}
	// A line the CFO wrote of a landing, which GitHub says merged, is no
	// merge of its own where the log holds the merge it names.
	handLanded := func(line Entry) bool { return landed(line) && isHandWritten(line) }
	noted := slices.Clone(decisions)
	folded := make([]bool, len(decisions))
	fold := func(i int, into func(Entry) bool) {
		line := decisions[i]
		var named, before []int
		for j, merge := range noted {
			if j == i || folded[j] || !into(merge) {
				continue
			}
			named = append(named, j)
			if merge.At.Before(line.At) && merge.Noted == "" {
				before = append(before, j)
			}
		}
		if len(before) > 0 {
			named = before
		}
		for _, j := range named {
			noted[j].Noted = strings.TrimSpace(noted[j].Noted + " " + said(line, noted[j]))
		}
		folded[i] = len(named) > 0
	}
	for i, line := range decisions {
		if open(line) || handLanded(line) {
			fold(i, func(merge Entry) bool { return landed(merge) && names(line, merge) })
		}
	}
	for i, line := range decisions {
		if open(line) && !folded[i] && line.Outcome == "" && line.Link != "" && line.Link != line.What {
			fold(i, func(merge Entry) bool { return open(merge) && merge.What == line.Link })
		}
	}
	for _, line := range decisions {
		for j, merge := range noted {
			switch {
			case line.Kind != KindLeft && line.Kind != KindHis || line.Struck != "" || line.Link == "" || folded[j] || !open(merge) || merge.What != line.Link:
			case NeedsHim(line):
				noted[j].Noted = strings.TrimSpace(noted[j].Noted + " It needs you: " + sentence(line.What))
			case AnsweredForHim(line):
				noted[j].Noted = strings.TrimSpace(noted[j].Noted + " " + said(line, merge) + " It answered: " + strings.TrimSuffix(line.Answer, ".") + ".")
			}
		}
	}
	kept := []Entry{}
	for i, decision := range noted {
		if !folded[i] {
			kept = append(kept, decision)
		}
	}
	return kept
}

// pullRequestURL is the URL of a pull request on GitHub, and nothing more: a
// link to its files or a comment is no pull request.
var pullRequestURL = regexp.MustCompile(`^https://github\.com/[^/\s]+/[^/\s]+/pull/\d+$`)

// PullRequestURL is the pull request a merge line names: its link when that is
// one, else its words when they are, else empty.
func PullRequestURL(link, what string) string {
	for _, subject := range []string{link, what} {
		if pullRequestURL.MatchString(subject) {
			return subject
		}
	}
	return ""
}

// isHandWritten reports whether a merge line is one the CFO wrote of a
// pull request in its own words, rather than the merge's own record, which is
// the pull request and links it.
func isHandWritten(line Entry) bool {
	return line.Kind == KindMerge && line.Struck == "" && line.Link != "" && line.Link != line.What
}

// UnprovenMerges are the pull requests the merge lines of decisions name and
// say nothing of how they ended, each once, for GitHub to be asked whether it
// merged. A line that records an outcome, a struck line and one that names no
// pull request is not asked about.
func UnprovenMerges(decisions []Entry) []string {
	var urls []string
	for _, decision := range decisions {
		if decision.Kind != KindMerge || decision.Outcome != "" || decision.Struck != "" {
			continue
		}
		if url := PullRequestURL(decision.Link, decision.What); url != "" && !slices.Contains(urls, url) {
			urls = append(urls, url)
		}
	}
	return urls
}

// ConfirmMerges is decisions with the outcome merged on each merge line that
// records none and names a pull request merged says GitHub merged, so a merge
// the CFO logged by hand reads as the merge it is. A pull request merged does
// not answer for, or answers false for, leaves its lines as they were.
func ConfirmMerges(decisions []Entry, merged map[string]bool) []Entry {
	confirmed := slices.Clone(decisions)
	for i, decision := range confirmed {
		if decision.Kind == KindMerge && decision.Outcome == "" && decision.Struck == "" && merged[PullRequestURL(decision.Link, decision.What)] {
			confirmed[i].Outcome = OutcomeMerged
		}
	}
	return confirmed
}

// asPullRequests is decisions with each merge the CFO logged by hand, which
// GitHub says merged and the log holds no merge line of its own for, as the
// pull request it merged: the CFO's words ride on it as a train's line for it
// would carry them, and a second line for the same pull request rides on the
// first.
func asPullRequests(decisions []Entry) []Entry {
	kept := []Entry{}
	first := map[string]int{}
	for _, entry := range decisions {
		if entry.Outcome != OutcomeMerged || !isHandWritten(entry) || PullRequestURL(entry.Link, "") == "" {
			kept = append(kept, entry)
			continue
		}
		if i, ok := first[entry.Link]; ok {
			kept[i].Noted = strings.TrimSpace(kept[i].Noted + " " + said(entry, kept[i]) + " " + entry.Noted)
			continue
		}
		entry.Noted, entry.What = strings.TrimSpace(sentence(entry.What)+" "+entry.Noted), entry.Link
		first[entry.Link] = len(kept)
		kept = append(kept, entry)
	}
	return kept
}

// said is a line of the CFO's as it rides on the merge it names: its words,
// without the pull request the merge names already, and how it ended when it
// was a merge word that did not land.
func said(line, merge Entry) string {
	var parts []string
	for _, part := range []string{line.What, line.Evidence} {
		if part = strings.TrimSuffix(strings.TrimSpace(part), "."); part != "" && part != merge.What {
			parts = append(parts, sentence(part))
		}
	}
	if line.Outcome != "" && line.Outcome != OutcomeMerged {
		parts = append(parts, NotLanded(line.Outcome))
	}
	return strings.Join(parts, " ")
}

// sentence is text as a sentence of the report: one full stop at its end,
// unless it asks a question.
func sentence(text string) string {
	text = strings.TrimSuffix(strings.TrimSpace(text), ".")
	if strings.HasSuffix(text, "?") {
		return text
	}
	return text + "."
}

// did says what the CFO did about a merge that did not land: the lines it
// wrote about it, or that it logged nothing. A line the CFO wrote that names
// no merge needs no word of what it did, since it is the CFO's own.
func did(merge Entry) string {
	switch {
	case merge.Noted != "":
		return "The CFO: " + merge.Noted
	case merge.Link == "" || merge.Link == merge.What:
		return "The CFO logged nothing about it."
	}
	return ""
}

// NotLanded says in one sentence what became of a merge that did not land:
// how it ended, as the log holds it, or that no merge was recorded.
func NotLanded(outcome string) string {
	outcome = strings.TrimSuffix(strings.TrimSpace(outcome), ".")
	if outcome == "" {
		return "No merge was recorded."
	}
	return strings.ToUpper(outcome[:1]) + outcome[1:] + "."
}

// The parts of the recap a heading is in, in the order it reads them: what
// was done, what the CFO decided for the Overlord and the goblins, and last
// what needs him.
const (
	GroupDone    = "done"
	GroupDecided = "decided"
	GroupNeeds   = "needs"
)

// Section is one heading of the report, in its part of the recap, with the
// lines under it.
type Section struct {
	Title   string  `json:"title"`
	Group   string  `json:"group"`
	Entries []Entry `json:"entries"`
}

// Sections sorts the stretch's lines under their headings, in the order the
// recap reads them, and a heading is there only when it holds something.
// Done leads with a merge that did not land, then what changed for the
// Overlord: what was deployed, migrated and installed, then what merged, and
// what else the CFO did, the goblins the supervisor paused at a floor, each
// step only he could take that the CFO saw to later, and last what the CFO
// struck, under Struck by the CFO alone so it is never taken for anything
// else. A line the CFO wrote for a landing is with the merge it names. Then
// Decided: each decision the CFO answered in his place and each goblin's
// question it answered, each naming the question in his History where he
// changes its answer. Last Needs you: each step only he can take that is
// still open. The CFO's text and the board's page are both written from these.
func (r Report) Sections() []Section {
	var sections []Section
	// A decision already represented by a held question takes that question's
	// current state, rather than appearing again with its saved state.
	decisions := slices.DeleteFunc(slices.Clone(r.Decisions), func(entry Entry) bool {
		return entry.Kind == KindLeft && slices.ContainsFunc(r.Held, func(held Held) bool {
			return held.Item == "question:"+LeftQuestionID(entry)
		})
	})
	decisions = asPullRequests(Noted(decisions))
	for i, entry := range decisions {
		switch {
		case AnsweredForHim(entry):
			decisions[i].Item = "question:" + LeftQuestionID(entry)
		case entry.Kind == KindAnswer:
			decisions[i].Item = "question:" + entry.What
		}
	}
	add := func(group, title string, entries []Entry) {
		if len(entries) > 0 {
			sections = append(sections, Section{Title: title, Group: group, Entries: entries})
		}
	}
	lines := func(group, title string, keep func(Entry) bool) {
		entries := []Entry{}
		for _, entry := range decisions {
			if entry.Struck == "" && keep(entry) {
				entries = append(entries, entry)
			}
		}
		add(group, title, entries)
	}
	kind := func(kind string) func(Entry) bool { return func(entry Entry) bool { return entry.Kind == kind } }
	lines(GroupDone, "Not merged", func(entry Entry) bool { return entry.Kind == KindMerge && entry.Outcome != OutcomeMerged })
	lines(GroupDone, "Deployed", kind(KindDeploy))
	lines(GroupDone, "Migrations applied", kind(KindMigration))
	lines(GroupDone, "Installed", kind(KindInstall))
	lines(GroupDone, "Merged", func(entry Entry) bool { return entry.Kind == KindMerge && entry.Outcome == OutcomeMerged })
	lines(GroupDone, "Other", kind(KindOther))
	add(GroupDone, "Paused at a floor", r.Paused)
	lines(GroupDone, "Settled by the CFO", func(entry Entry) bool { return entry.Settled != "" })
	add(GroupDone, "Struck by the CFO", slices.Concat(slices.DeleteFunc(slices.Clone(r.Decisions), func(entry Entry) bool { return entry.Struck == "" }), r.Struck))
	lines(GroupDecided, "For you", AnsweredForHim)
	lines(GroupDecided, "For the goblins", kind(KindAnswer))
	lines(GroupNeeds, "Needs you", NeedsHim)
	return sections
}

// AnsweredForHim reports whether a line is a decision the CFO answered in the
// Overlord's place: once AFK mode turns off it is in his History with the
// CFO's answer, never asked of him. One the CFO settled before nothing was
// left for him was never put there, and is under Settled by the CFO.
func AnsweredForHim(left Entry) bool {
	return left.Kind == KindLeft && left.Answer != "" && left.Settled == ""
}

// NeedsHim reports whether a line is a step only the Overlord can take that is
// still open: one the CFO logged as his, or a line it left him unanswered
// before nothing was left for him, which it neither struck nor saw to later.
func NeedsHim(line Entry) bool {
	return (line.Kind == KindHis || line.Kind == KindLeft && line.Answer == "") && line.Struck == "" && line.Settled == ""
}

// Lasted says how long the stretch lasted, to the minute.
func (r Report) Lasted() string {
	return span(r.Ended.Sub(r.Since))
}

// number writes a reading without the digits a float adds: 40, or 47.5.
func number(value float64) string {
	return strconv.FormatFloat(math.Round(value*10)/10, 'f', -1, 64)
}

// Used is one allowance under the report's Spent: a window's percent used
// when AFK mode turned on and when it turned off, nil for a reading not
// taken, with Reset when the window reset in between, or for a credit balance
// what was spent of it in Unit.
type Used struct {
	Provider string   `json:"provider"`
	Window   string   `json:"window"`
	On       *float64 `json:"on,omitempty"`
	Off      *float64 `json:"off,omitempty"`
	Reset    bool     `json:"reset,omitempty"`
	Credits  bool     `json:"credits,omitempty"`
	Spent    float64  `json:"spent,omitempty"`
	Unit     string   `json:"unit,omitempty"`
}

// Spent sets the allowance read when AFK mode turned on beside the one read
// when it turned off, one row for each provider's window or credit balance
// that was used. A window at 0% wherever it was read is left out, and so is a
// credit balance that was not read at both ends or did not fall: neither says
// anything was used. A reading not taken leaves its end out of the row.
func Spent(before, after []Allowance) []Used {
	same := func(a, b Allowance) bool { return a.Provider == b.Provider && a.Window == b.Window }
	used := func(percent *float64) bool { return percent != nil && number(*percent) != "0" }
	var rows []Used
	for _, reading := range slices.Concat(before, after) {
		if slices.ContainsFunc(rows, func(row Used) bool { return row.Provider == reading.Provider && row.Window == reading.Window }) {
			continue
		}
		row := Used{Provider: reading.Provider, Window: reading.Window}
		i := slices.IndexFunc(before, func(on Allowance) bool { return same(on, reading) })
		j := slices.IndexFunc(after, func(off Allowance) bool { return same(off, reading) })
		if reading.Credits {
			if i < 0 || j < 0 || before[i].Unlimited || after[j].Unlimited {
				continue
			}
			row.Credits, row.Spent, row.Unit = true, before[i].Remaining-after[j].Remaining, reading.Unit
			if row.Spent > 0 && number(row.Spent) != "0" {
				rows = append(rows, row)
			}
			continue
		}
		if i >= 0 {
			on := before[i].PercentUsed
			row.On = &on
		}
		if j >= 0 {
			off := after[j].PercentUsed
			row.Off = &off
		}
		row.Reset = i >= 0 && j >= 0 && before[i].ResetsAt.Sub(after[j].ResetsAt).Abs() >= sameWindow
		if used(row.On) || used(row.Off) {
			rows = append(rows, row)
		}
	}
	return rows
}

// DiskUse is free disk under the report's Spent: the bytes free on Drive when
// AFK mode turned on and when it turned off, of the drive's Total.
type DiskUse struct {
	Drive string `json:"drive"`
	Total uint64 `json:"total"`
	On    uint64 `json:"on"`
	Off   uint64 `json:"off"`
}

// DiskUsed sets the free disk read when AFK mode turned on beside the one read
// when it turned off. It is nil unless both were read, and of one drive: a
// reading not taken leaves nothing to compare, and the disk meter reads
// whichever of the home's drive and its Dev Drive has less free, so two
// readings of two drives say nothing of either.
func DiskUsed(before, after *disk.Reading) *DiskUse {
	if before == nil || after == nil || before.Drive != after.Drive {
		return nil
	}
	return &DiskUse{Drive: after.Drive, Total: after.Total, On: before.Free, Off: after.Free}
}

// says is free disk as the CFO's text words it, after the rows of Spent: what
// was free at either end and what AFK mode used or freed of it, to the tenth
// of a gigabyte, under which free disk did not move.
func (use DiskUse) says() string {
	name := "disk"
	if use.Drive != "" {
		name += " (" + use.Drive + ")"
	}
	on, off := disk.GB(use.On), disk.GB(use.Off)
	change := "no change"
	switch moved := math.Round((off-on)*10) / 10; {
	case moved > 0:
		change = number(moved) + " GB freed"
	case moved < 0:
		change = number(-moved) + " GB used"
	}
	return name + ": " + number(on) + " GB free when it turned on, " + number(off) + " GB when it turned off (" + change + ")"
}

// says is a row of Spent as the CFO's text words it.
func (row Used) says() string {
	name := row.Provider + " " + row.Window + ": "
	switch {
	case row.Credits:
		return name + number(row.Spent) + " " + row.Unit + " spent"
	case row.On == nil:
		return name + number(*row.Off) + "% used when it turned off"
	case row.Off == nil:
		return name + number(*row.On) + "% used when it turned on"
	case row.Reset:
		return name + number(*row.On) + "% used when it turned on, " + number(*row.Off) + "% when it turned off, after the window reset"
	}
	points := number(*row.Off - *row.On)
	unit := " points)"
	if points == "1" {
		unit = " point)"
	}
	return name + number(*row.On) + "% used when it turned on, " + number(*row.Off) + "% when it turned off (" + points + unit
}

// span writes how long a stretch lasted, to the minute.
func span(d time.Duration) string {
	if d < time.Minute {
		return "under a minute"
	}
	d = d.Round(time.Minute)
	if hours := int(d / time.Hour); hours > 0 {
		return fmt.Sprintf("%dh%02dm", hours, int(d/time.Minute)%60)
	}
	return fmt.Sprintf("%dm", int(d/time.Minute))
}

// Render writes the report as the text the CFO puts in its terminal, in the
// recap's order: who turned it on and off, or that nothing needs him, then
// Done, with what each goblin finished, what was spent, with what became of
// free disk after it, and each item held for the Overlord that no longer
// waits, then Decided for you, and last Needs you, each step only he can take
// and each item that still waits on him in the Command Center, as it stands
// now. A part with nothing under it is left out, but for Done.
func Render(w io.Writer, r Report) error {
	var out []string
	say := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }
	say("AFK MODE REPORT")
	say("AFK mode was on from %s to %s (%s): turned on %s, off %s.", at(r.Since), at(r.Ended), r.Lasted(), SwitchedBy(r.From, r.Asked), SwitchedBy(r.EndedFrom, r.EndedAsked))
	sections := r.Sections()
	in := func(group string) []Section {
		return slices.DeleteFunc(slices.Clone(sections), func(section Section) bool { return section.Group != group })
	}
	waiting := slices.DeleteFunc(slices.Clone(r.Held), func(one Held) bool { return !one.Waiting })
	settled := slices.DeleteFunc(slices.Clone(r.Held), func(one Held) bool { return one.Waiting })
	needs := in(GroupNeeds)
	if len(needs) == 0 && len(waiting) == 0 {
		say("Nothing needs you.")
	}
	part := func(title string, sections []Section) {
		say("")
		say("%s", strings.ToUpper(title))
		for _, section := range sections {
			say("")
			say("%s (%d)", section.Title, len(section.Entries))
			for _, entry := range section.Entries {
				out = append(out, entryLines(entry)...)
			}
		}
	}
	held := func(title string, held []Held) {
		if len(held) == 0 {
			return
		}
		say("")
		say("%s (%d), each as it stands now", title, len(held))
		for _, one := range held {
			whose := "the CFO's"
			if one.Task != "" {
				whose = one.Task + "'s"
			}
			say("- %s, %s: %s", one.Item, whose, one.What)
			if recommends := Recommends(one.Task, one.Recommendation, one.Waiting); recommends != "" {
				say("  %s", recommends)
			}
			line := "  Now: " + one.Now + "."
			if one.Meanwhile != "" {
				line += " Meanwhile: " + one.Meanwhile + "."
			}
			say("%s", line)
		}
	}

	part("Done", in(GroupDone))
	say("")
	say("Goblins finished (%d)", len(r.Finished))
	for _, finish := range r.Finished {
		say("- %s: %s (%s)", finish.Task, finish.PR, finish.At.UTC().Format("15:04 UTC"))
	}
	if spent, use := Spent(r.Before, r.After), DiskUsed(r.DiskBefore, r.DiskAfter); len(spent) > 0 || use != nil {
		say("")
		say("Spent")
		for _, row := range spent {
			say("- %s", row.says())
		}
		if use != nil {
			say("- %s", use.says())
		}
	}
	held("Settled", settled)
	if decided := in(GroupDecided); len(decided) > 0 {
		part("Decided for you", decided)
	}
	if len(needs) > 0 || len(waiting) > 0 {
		part("Needs you", needs)
		held("Waiting on you in the Command Center", waiting)
	}

	if len(r.Notes) > 0 {
		say("")
		say("Not read")
		for _, note := range r.Notes {
			say("- %s", note)
		}
	}
	for _, line := range out {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

// entryLines is one line of the report as the CFO's text writes it: whose and
// what, its link, how it ended, then what it stood on on lines of its own. A
// decision answered in the Overlord's place is recapped with its answer and
// where he changes it, since its question in History carries what it stood on.
func entryLines(entry Entry) []string {
	line := "- "
	if entry.Task != "" {
		line += entry.Task + ": "
	}
	line += entry.What
	// A pull request is its own link.
	if entry.Link != "" && entry.Link != entry.What {
		line += " (" + entry.Link + ")"
	}
	notLanded := entry.Kind == KindMerge && entry.Outcome != OutcomeMerged && entry.Struck == ""
	switch {
	case entry.Struck != "":
		line += " (struck)"
	case entry.Outcome != "" && !notLanded:
		line += ": " + entry.Outcome
	}
	lines := []string{line}
	if notLanded {
		if did := did(entry); did != "" {
			lines = append(lines, "  "+did)
		}
		lines = append(lines, "  "+NotLanded(entry.Outcome))
	}
	if entry.Struck != "" {
		lines = append(lines, "  Struck: "+entry.Struck)
	}
	if entry.Settled != "" {
		lines = append(lines, "  Settled: "+entry.Settled)
	}
	if entry.Noted != "" && !notLanded {
		lines = append(lines, "  The CFO: "+entry.Noted)
	}
	if AnsweredForHim(entry) && entry.Struck == "" {
		return append(lines, "  The CFO answered: "+strings.TrimSuffix(entry.Answer, ".")+". You can change it in History.")
	}
	// A strike of an earlier stretch's line carries the reason as its
	// evidence, and names the line by when it was logged.
	if entry.Kind == KindStrike {
		return append(lines, "  Logged: "+entry.Item)
	}
	return append(lines, "  Evidence: "+entry.Evidence)
}

// SaveReport keeps the report of the stretch that just ended.
func SaveReport(stateDir string, r Report) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, reportFile), append(data, '\n'))
}

// ReadReport returns the report of the last stretch that ended, and whether
// there is one, with each strike the CFO made since it was kept, which
// belongs to that stretch: the report shows those lines struck too.
func ReadReport(stateDir string) (Report, bool, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, reportFile))
	if errors.Is(err, os.ErrNotExist) {
		return Report{}, false, nil
	}
	if err != nil {
		return Report{}, false, err
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return Report{}, false, fmt.Errorf("the AFK report (state/%s) cannot be read: %w", reportFile, err)
	}
	entries, _, err := Entries(stateDir, r.Session)
	if err != nil {
		return Report{}, false, fmt.Errorf("the strikes of the AFK report's stretch cannot be read: %w", err)
	}
	r.Decisions, r.Struck = marked(r.Decisions, entries), StruckEarlier(entries)
	return r, true, nil
}
