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
	"time"

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
// finished, what is held for the Overlord and why, and what was spent.
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
	// Decisions are the stretch's decisions in the order they were made.
	Decisions []Entry  `json:"decisions"`
	Finished  []Finish `json:"finished"`
	Held      []Held   `json:"held"`
	// Before and After are the allowance read when AFK mode turned on and
	// when it turned off.
	Before []Allowance `json:"before"`
	After  []Allowance `json:"after"`
	// Notes say what the report could not read.
	Notes []string `json:"notes,omitempty"`
}

// Finish is a pull request a goblin reported done during the stretch.
type Finish struct {
	Task string    `json:"task"`
	PR   string    `json:"pr"`
	At   time.Time `json:"at"`
}

// Held is an item that waited on the Overlord during the stretch.
type Held struct {
	// Item is its key in the Command Center, Task its goblin, empty for the
	// CFO's own, and What the question or request itself, which says why it
	// is his.
	Item string    `json:"item"`
	Task string    `json:"task,omitempty"`
	What string    `json:"what"`
	At   time.Time `json:"at"`
	// Waiting says it still waits on him, and Now what became of it by the
	// time AFK mode turned off.
	Waiting bool   `json:"waiting"`
	Now     string `json:"now"`
	// Meanwhile is what was done while it waited: its goblin's latest report.
	Meanwhile string `json:"meanwhile,omitempty"`
}

// Decisions folds a stretch's log lines into its decisions, in the order they
// were made: a later line that carries an outcome closes the decision logged
// before it for the same kind and subject.
func Decisions(entries []Entry) []Entry {
	var decisions []Entry
	for _, entry := range entries {
		if !slices.Contains(DecisionKinds, entry.Kind) {
			continue
		}
		if entry.Outcome != "" {
			open := -1
			for i, prior := range decisions {
				if prior.Kind == entry.Kind && prior.What == entry.What && prior.Outcome == "" {
					open = i
				}
			}
			if open >= 0 {
				decisions[open].Outcome = entry.Outcome
				continue
			}
		}
		decisions = append(decisions, entry)
	}
	return decisions
}

// Section is one heading of the report with the decisions under it.
type Section struct {
	Title   string  `json:"title"`
	Entries []Entry `json:"entries"`
}

// Sections sorts the report's decisions under its headings, in the order the
// report lists them. A heading the report always shows is there with nothing
// under it; the others are there only when they hold something. The CFO's
// text and the board's page are both written from these.
func (r Report) Sections() []Section {
	var sections []Section
	decided := func(title string, keep func(Entry) bool, always bool) {
		entries := []Entry{}
		for _, entry := range r.Decisions {
			if keep(entry) {
				entries = append(entries, entry)
			}
		}
		if len(entries) > 0 || always {
			sections = append(sections, Section{Title: title, Entries: entries})
		}
	}
	kind := func(kind string) func(Entry) bool { return func(entry Entry) bool { return entry.Kind == kind } }
	decided("Merged", func(entry Entry) bool { return entry.Kind == KindMerge && entry.Outcome == OutcomeMerged }, true)
	decided("Merge words with no merge recorded", func(entry Entry) bool { return entry.Kind == KindMerge && entry.Outcome != OutcomeMerged }, false)
	decided("Deployed", kind(KindDeploy), true)
	decided("Migrations applied", kind(KindMigration), true)
	decided("Installed", kind(KindInstall), true)
	decided("Answered for goblins", kind(KindAnswer), true)
	decided("Other decisions", kind(KindOther), false)
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

// Spent sets the allowance read when AFK mode turned on beside the one read
// when it turned off, one line for each provider's window or credit balance.
// A window that reset in between says so instead of a difference that would
// mean nothing.
func Spent(before, after []Allowance) []string {
	same := func(a, b Allowance) bool { return a.Provider == b.Provider && a.Window == b.Window }
	reading := func(a Allowance) string {
		if a.Credits {
			return number(a.Remaining) + " " + a.Unit + " left"
		}
		return number(a.PercentUsed) + "% used"
	}
	var lines []string
	for _, on := range before {
		name := on.Provider + " " + on.Window + ": "
		i := slices.IndexFunc(after, func(off Allowance) bool { return same(on, off) })
		if i < 0 {
			lines = append(lines, name+reading(on)+" when it turned on; not read when it turned off")
			continue
		}
		off := after[i]
		switch {
		case on.Credits && on.Unlimited && off.Unlimited:
			lines = append(lines, name+"unlimited")
		case on.Credits:
			line := name + reading(on) + " when it turned on, " + number(off.Remaining) + " when it turned off"
			if spent := on.Remaining - off.Remaining; spent >= 0 {
				line += " (" + number(spent) + " spent)"
			} else {
				line += "; the balance rose in between"
			}
			lines = append(lines, line)
		case on.ResetsAt.Sub(off.ResetsAt).Abs() < sameWindow:
			points := number(off.PercentUsed - on.PercentUsed)
			unit := " points)"
			if points == "1" {
				unit = " point)"
			}
			lines = append(lines, name+reading(on)+" when it turned on, "+number(off.PercentUsed)+"% when it turned off ("+points+unit)
		default:
			lines = append(lines, name+reading(on)+" when it turned on, "+number(off.PercentUsed)+"% when it turned off; the window reset in between")
		}
	}
	for _, off := range after {
		if !slices.ContainsFunc(before, func(on Allowance) bool { return same(on, off) }) {
			lines = append(lines, off.Provider+" "+off.Window+": not read when it turned on; "+reading(off)+" when it turned off")
		}
	}
	return lines
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
// turned it on and off, what is held for the Overlord, which he reads first,
// then what the CFO decided, what each goblin finished and what was spent.
func Render(w io.Writer, r Report) error {
	var out []string
	say := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }
	say("AFK MODE REPORT")
	say("AFK mode was on from %s to %s (%s): turned on %s, off %s.", at(r.Since), at(r.Ended), r.Lasted(), SwitchedBy(r.From, r.Asked), SwitchedBy(r.EndedFrom, r.EndedAsked))

	say("")
	say("Held for you (%d)", len(r.Held))
	for _, held := range r.Held {
		whose := "the CFO's"
		if held.Task != "" {
			whose = held.Task + "'s"
		}
		say("- %s, %s: %s", held.Item, whose, held.What)
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
			if entry.Kind == KindMerge && entry.Outcome == "" {
				line += ": no outcome was recorded"
			} else if entry.Outcome != "" {
				line += ": " + entry.Outcome
			}
			say("%s", line)
			say("  Evidence: %s", entry.Evidence)
		}
	}

	say("")
	say("Goblins finished (%d)", len(r.Finished))
	for _, finish := range r.Finished {
		say("- %s: %s (%s)", finish.Task, finish.PR, finish.At.UTC().Format("15:04 UTC"))
	}

	say("")
	say("Spent")
	spent := Spent(r.Before, r.After)
	if len(spent) == 0 {
		say("- no allowance was read")
	}
	for _, line := range spent {
		say("- %s", line)
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
// there is one.
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
	return r, true, nil
}
