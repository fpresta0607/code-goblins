package afk

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
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

// Held is an item that waited on the Overlord during the stretch: one held
// for him, or a line the CFO left for him, which is asked in the Command
// Center as the CFO's question when the stretch ends.
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
	// Settled is what became of a line left for him that the CFO settled in
	// the stretch: it was never asked, so nothing read later changes it.
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

// Section is one heading of the report with the decisions under it.
type Section struct {
	Title   string  `json:"title"`
	Entries []Entry `json:"entries"`
}

// Sections sorts what the CFO decided under its headings, in the order the
// report lists them, the goblins paused at a floor after them, and last what
// the CFO struck. A heading is there only when it holds something. What it
// left for him is not among them: it is with what was held for him, as it
// stands now. A struck line is under Struck by the CFO alone, so it is never
// taken for something left for him or decided. The CFO's text and the
// board's page are both written from these.
func (r Report) Sections() []Section {
	var sections []Section
	decided := func(title string, keep func(Entry) bool) {
		entries := []Entry{}
		for _, entry := range r.Decisions {
			if entry.Struck == "" && keep(entry) {
				entries = append(entries, entry)
			}
		}
		if len(entries) > 0 {
			sections = append(sections, Section{Title: title, Entries: entries})
		}
	}
	kind := func(kind string) func(Entry) bool { return func(entry Entry) bool { return entry.Kind == kind } }
	decided("Merged", func(entry Entry) bool { return entry.Kind == KindMerge && entry.Outcome == OutcomeMerged })
	decided("Merge words with no merge recorded", func(entry Entry) bool { return entry.Kind == KindMerge && entry.Outcome != OutcomeMerged })
	decided("Deployed", kind(KindDeploy))
	decided("Migrations applied", kind(KindMigration))
	decided("Installed", kind(KindInstall))
	decided("Answered for goblins", kind(KindAnswer))
	decided("Other decisions", kind(KindOther))
	if len(r.Paused) > 0 {
		sections = append(sections, Section{Title: "Paused at a floor", Entries: r.Paused})
	}
	struck := slices.Concat(slices.DeleteFunc(slices.Clone(r.Decisions), func(entry Entry) bool { return entry.Struck == "" }), r.Struck)
	if len(struck) > 0 {
		sections = append(sections, Section{Title: "Struck by the CFO", Entries: struck})
	}
	return sections
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

// Render writes the report as the text the CFO puts in its terminal: who
// turned it on and off, what was held or left for the Overlord, which he
// reads first, then what the CFO decided, what each goblin finished and what
// was spent, with what became of free disk after it.
func Render(w io.Writer, r Report) error {
	var out []string
	say := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }
	say("AFK MODE REPORT")
	say("AFK mode was on from %s to %s (%s): turned on %s, off %s.", at(r.Since), at(r.Ended), r.Lasted(), SwitchedBy(r.From, r.Asked), SwitchedBy(r.EndedFrom, r.EndedAsked))

	say("")
	say("For you (%d), each as it stands now", len(r.Held))
	for _, held := range r.Held {
		whose := "the CFO's"
		if held.Task != "" {
			whose = held.Task + "'s"
		}
		say("- %s, %s: %s", held.Item, whose, held.What)
		if recommends := Recommends(held.Task, held.Recommendation, held.Waiting); recommends != "" {
			say("  %s", recommends)
		}
		line := "  Now: " + held.Now + "."
		if held.Meanwhile != "" {
			line += " Meanwhile: " + held.Meanwhile + "."
		}
		say("%s", line)
	}

	for _, section := range r.Sections() {
		say("")
		say("%s (%d)", section.Title, len(section.Entries))
		for _, entry := range section.Entries {
			line := "- "
			if entry.Task != "" {
				line += entry.Task + ": "
			}
			line += entry.What
			// A pull request is its own link.
			if entry.Link != "" && entry.Link != entry.What {
				line += " (" + entry.Link + ")"
			}
			switch {
			case entry.Struck != "":
				line += " (struck)"
			case entry.Kind == KindMerge && entry.Outcome == "":
				line += ": no outcome was recorded"
			case entry.Outcome != "":
				line += ": " + entry.Outcome
			}
			say("%s", line)
			if entry.Struck != "" {
				say("  Struck: %s", entry.Struck)
			}
			// A strike of an earlier stretch's line carries the reason as its
			// evidence, and names the line by when it was logged.
			if entry.Kind == KindStrike {
				say("  Logged: %s", entry.Item)
				continue
			}
			say("  Evidence: %s", entry.Evidence)
		}
	}

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
