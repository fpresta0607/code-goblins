package memory

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// DefaultPackBudget is a context pack's estimated-token allowance unless
// config/context-pack-budget names another.
const DefaultPackBudget = 6000

// PackInput is what one goblin's context pack is drawn from: its task and
// brief, and the live state of its repository read fresh at this start.
type PackInput struct {
	Task    string
	Project string
	Title   string
	Brief   string
	Budget  int
	Now     time.Time
	Live    []LiveTask
	// Commits are main's commits since the task's base, newest first, or
	// CommitsUnread says why they could not be read.
	Commits       []string
	CommitsUnread string
	// Delivered is the fingerprints of entries the goblin's retained
	// conversation already holds; those are named, not repeated.
	// DeliveredUnread says why that record could not be read, so every entry
	// is shown.
	Delivered       []string
	DeliveredUnread string
}

// RoleGoblin is the role a context pack is written for; a fact scoped to
// another role stays out of it.
const RoleGoblin = "goblin"

// LiveTask is another task working in the same repository right now, with
// the last pull request its reports name.
type LiveTask struct {
	ID          string
	Title       string
	Goblin      string
	Branch      string
	PullRequest string
	Status      string
}

// Pack is a rendered context pack and the fingerprints of every entry it
// selected, which the next pack of a resumed conversation omits.
type Pack struct {
	Text      string
	Selected  []string
	Shown     int
	Unchanged int
	Tokens    int
	Budget    int
}

type packEntry struct {
	section     string
	text        string
	fingerprint string
}

const (
	sectionRulings = "Rulings in scope (verbatim, dated)"
	sectionFacts   = "Current facts (superseded ones hidden)"
	sectionLive    = "Live now in this repository"
	sectionDone    = "Recently finished here"
)

// briefQuestion is the part of a brief that says what the task is: its
// title, Task and Ticket sections, or the whole brief when it has neither.
func briefQuestion(title, brief, project string) string {
	var parts []string
	for _, heading := range []string{"## Task", "## Ticket"} {
		_, after, found := strings.Cut(brief, heading+"\n")
		if !found {
			continue
		}
		if next := strings.Index(after, "\n## "); next >= 0 {
			after = after[:next]
		}
		parts = append(parts, after)
	}
	if len(parts) == 0 {
		parts = append(parts, brief)
	}
	return title + "\n" + project + "\n" + strings.Join(parts, "\n")
}

func oneLine(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8Start(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}

func (index *Index) packCandidates(input PackInput) (map[string][]packEntry, error) {
	entries := map[string][]packEntry{}
	add := func(section, text string) {
		entries[section] = append(entries[section], packEntry{section, text, Hash([]byte(section + "\n" + text))})
	}
	// The items that best match the task's title and project lead, so a
	// long brief's general words cannot crowd out what its subject is.
	asks := []struct {
		question string
		limit    int
	}{{input.Title + "\n" + input.Project, DefaultRecallLimit}, {briefQuestion(input.Title, input.Brief, input.Project), 50}}
	for _, kind := range []string{KindRuling, KindFact} {
		taken := map[string]bool{}
		for _, ask := range asks {
			if len(Terms(ask.question)) == 0 {
				continue
			}
			result, err := index.Recall(RecallQuery{Question: ask.question, Project: input.Project, Task: input.Task, Role: RoleGoblin, Kinds: []string{kind}, Limit: ask.limit})
			if err != nil {
				return nil, err
			}
			for _, item := range result.Items {
				if taken[item.ID] {
					continue
				}
				taken[item.ID] = true
				if kind == KindRuling {
					add(sectionRulings, fmt.Sprintf("- %s %s (%s:%d, %s): %s", FormatDate(item.Date), item.Title, item.Path, item.Line, item.ID, item.Text))
					continue
				}
				line := fmt.Sprintf("- %s %s (%s): %s", FormatDate(item.Date), item.ID, item.Path, item.Excerpt)
				if len(item.Supersedes) > 0 {
					line += " Supersedes " + strings.Join(item.Supersedes, ", ") + "."
				}
				add(sectionFacts, line)
			}
		}
	}
	for _, task := range input.Live {
		line := fmt.Sprintf("- %s", task.ID)
		if task.Goblin != "" {
			line += " (" + task.Goblin + ")"
		}
		if task.Title != "" {
			line += ": " + task.Title
		}
		if task.Branch != "" {
			line += "; branch " + task.Branch
		}
		if task.PullRequest != "" && !strings.Contains(task.Status, task.PullRequest) {
			line += "; pull request " + task.PullRequest
		}
		if task.Status != "" {
			line += "; last report: " + oneLine(task.Status, 200)
		}
		add(sectionLive, line)
	}
	if input.CommitsUnread != "" {
		add(sectionLive, "- Commits on main since your base could not be read: "+input.CommitsUnread)
	}
	for _, commit := range input.Commits {
		add(sectionLive, "- on main since your base: "+commit)
	}
	var done []Item
	for _, item := range index.Items {
		if item.Kind == KindDone && item.Task != input.Task && slices.ContainsFunc(item.Projects, func(project string) bool { return strings.EqualFold(project, input.Project) }) {
			done = append(done, item)
		}
	}
	slices.SortFunc(done, func(left, right Item) int { return right.Date.Compare(left.Date) })
	for _, item := range done[:min(5, len(done))] {
		line := fmt.Sprintf("- %s %s: %s", FormatDate(item.Date), item.Task, oneLine(item.Title, 300))
		if item.Text != "" {
			line += " - " + oneLine(item.Text, 300)
		}
		add(sectionDone, line)
	}
	return entries, nil
}

// BuildPack selects the entries that fit input.Budget, in shares of it:
// the live repository first, then recent reports, rulings and facts, each
// share's unused part passed to the next. Entries the conversation already
// holds are named by count, not repeated.
func (index *Index) BuildPack(input PackInput) (Pack, error) {
	candidates, err := index.packCandidates(input)
	if err != nil {
		return Pack{}, err
	}
	header := fmt.Sprintf("# Context pack: %s, %s\n\nBuilt %s from the fleet's memory files and live state; every entry names its source, so open it for the whole text.\n", input.Project, input.Task, input.Now.UTC().Format(time.RFC3339))
	footer := "\nNeed more? Run ceo memory recall \"<question>\" --project " + input.Project + "; recall and this pack are discovery, never a ruling's replacement.\n"
	// The reserve holds the one closing note a pack can add.
	const reserve = 200
	allowance := input.Budget*4 - len(header) - len(footer) - reserve
	chosen := map[string][]packEntry{}
	carry := 0
	for _, share := range []struct {
		section string
		percent int
	}{{sectionLive, 25}, {sectionDone, 10}, {sectionRulings, 45}, {sectionFacts, 20}} {
		room := allowance*share.percent/100 + carry
		heading := len("\n## " + share.section + "\n")
		used := heading
		for _, entry := range candidates[share.section] {
			if used+len(entry.text)+1 > room {
				continue
			}
			chosen[share.section] = append(chosen[share.section], entry)
			used += len(entry.text) + 1
		}
		if len(chosen[share.section]) == 0 {
			used = 0
		}
		carry = room - used
	}
	pack := Pack{Budget: input.Budget}
	var output strings.Builder
	output.WriteString(header)
	for _, section := range []string{sectionRulings, sectionFacts, sectionLive, sectionDone} {
		var shown []string
		for _, entry := range chosen[section] {
			pack.Selected = append(pack.Selected, entry.fingerprint)
			if slices.Contains(input.Delivered, entry.fingerprint) {
				pack.Unchanged++
				continue
			}
			shown = append(shown, entry.text)
		}
		pack.Shown += len(shown)
		if len(shown) > 0 {
			output.WriteString("\n## " + section + "\n" + strings.Join(shown, "\n") + "\n")
		}
	}
	switch {
	case input.DeliveredUnread != "":
		fmt.Fprintf(&output, "\nThe record of your last pack could not be read (%s), so every entry is shown.\n", oneLine(input.DeliveredUnread, 100))
	case pack.Unchanged > 0:
		fmt.Fprintf(&output, "\n%d entries you already hold from your last pack are unchanged and not repeated; %d are new or changed.\n", pack.Unchanged, pack.Shown)
	case pack.Shown == 0:
		output.WriteString("\nNo memory or live state matched this task.\n")
	}
	output.WriteString(footer)
	pack.Text = output.String()
	pack.Tokens = Tokens(len(pack.Text))
	return pack, nil
}
