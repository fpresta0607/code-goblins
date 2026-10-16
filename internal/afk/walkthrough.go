package afk

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/disk"
)

// Ongoing is a goblin's work that was still under way when AFK mode turned
// off, or a task queued to start next: its title, the CFO's one line for it,
// and the goblin's latest note on it.
type Ongoing struct {
	Task   string    `json:"task"`
	Title  string    `json:"title,omitempty"`
	Goblin string    `json:"goblin,omitempty"`
	Note   string    `json:"note,omitempty"`
	At     time.Time `json:"at,omitzero"`
}

// Return is what the page made when AFK mode turns off shows: the report of
// the stretch, with what the board held of each decision's question when the
// page was made, where he changes its answer, the pictures the goblins made
// of their work, and the title of each merged pull request no goblin reported
// done in the stretch.
type Return struct {
	Report    Report
	Questions map[string]Question
	Pictures  []Picture
	Titles    map[string]string
}

// Question is a decision's question as his History holds it and its answer.
type Question struct {
	ID     string
	Text   string
	Answer string
}

// Picture is one picture a goblin made of its work in the stretch, named by
// its file beside the page, with the words it was shown with.
type Picture struct {
	Task    string
	Caption string
	File    string
}

// Project is one repository's part of the walkthrough.
type Project struct {
	Name    string
	Changes []Change
}

// Change is one item of the walkthrough: one task's work, or one merge,
// deploy or other decision no task's work holds, in plain words: its title
// and each note its goblin gave on it.
type Change struct {
	Task     string
	Goblin   string
	Title    string
	Notes    []string
	Pulls    []Pull
	Deploys  []string
	Pictures []Picture
	// NotLanded says a merge did not land, and Evidence is what each line
	// it was told from stood on.
	NotLanded string
	Evidence  []string
	At        time.Time
}

// Pull is a pull request beside a change, small: its number and its link.
type Pull struct {
	Number string
	URL    string
}

// Decision is one decision the CFO made for the Overlord: the short question,
// the answer taken, why in a sentence, and the question in his History it
// changes.
type Decision struct {
	// Task is the goblin's task whose question it was, empty for the
	// Overlord's own.
	Task     string
	Question string
	Answer   string
	Why      string
	Evidence string
	At       time.Time
	// Change is the question his change goes to, nil when the board held no
	// question for it when the page was made.
	Change *Question
}

// pullRequest reads a pull request's repository and number from its URL.
var pullRequest = regexp.MustCompile(`^https://github\.com/[^/\s]+/([^/\s]+)/pull/(\d+)$`)

// repositoryOf is the repository a pull request's URL names, empty for any
// other link.
func repositoryOf(url string) string {
	if m := pullRequest.FindStringSubmatch(url); m != nil {
		return m[1]
	}
	return ""
}

// pullOf is a pull request's URL as the page puts it beside a change.
func pullOf(url string) Pull {
	if m := pullRequest.FindStringSubmatch(url); m != nil {
		return Pull{Number: "#" + m[2], URL: url}
	}
	return Pull{Number: url, URL: url}
}

// elsewhere names the part of the walkthrough for what names no repository.
const elsewhere = "Across the fleet"

// Walkthrough is what changed in the stretch, by project: each task's work as
// one change, told by the CFO's title for it and the goblin's own note, with
// every pull request it finished small beside it, then each merge, deploy,
// migration, install or other decision no task's work holds. A deploy, a
// migration and an install are named on the change whose pull request they
// link, or as changes of their own. Projects come in the order their first
// change was made, and changes within each the same way.
func (r Return) Walkthrough() []Project {
	var projects []Project
	add := func(repository string, change Change) {
		if repository == "" {
			repository = elsewhere
		}
		i := slices.IndexFunc(projects, func(p Project) bool { return p.Name == repository })
		if i < 0 {
			projects = append(projects, Project{Name: repository})
			i = len(projects) - 1
		}
		projects[i].Changes = append(projects[i].Changes, change)
	}
	type place struct{ project, change int }
	byTask := map[string]place{}
	byPull := map[string]place{}
	locate := func(repository string) int {
		if repository == "" {
			repository = elsewhere
		}
		return slices.IndexFunc(projects, func(p Project) bool { return p.Name == repository })
	}
	for _, finish := range r.Report.Finished {
		noted := func(change *Change) {
			if finish.Note != "" && !slices.Contains(change.Notes, finish.Note) {
				change.Notes = append(change.Notes, finish.Note)
			}
		}
		if at, ok := byPull[finish.PR]; ok {
			noted(&projects[at.project].Changes[at.change])
			continue
		}
		repository := repositoryOf(finish.PR)
		key := finish.Task + "\n" + repository
		if at, ok := byTask[key]; ok {
			change := &projects[at.project].Changes[at.change]
			change.Pulls = append(change.Pulls, pullOf(finish.PR))
			noted(change)
			byPull[finish.PR] = at
			continue
		}
		title := finish.Title
		if title == "" {
			title = finish.Task
		}
		change := Change{Task: finish.Task, Goblin: finish.Goblin, Title: title, Pulls: []Pull{pullOf(finish.PR)}, At: finish.At}
		noted(&change)
		add(repository, change)
		at := place{locate(repository), len(projects[locate(repository)].Changes) - 1}
		byTask[key], byPull[finish.PR] = at, at
	}
	for _, picture := range r.Pictures {
		for key, at := range byTask {
			if task, _, _ := strings.Cut(key, "\n"); task == picture.Task {
				change := &projects[at.project].Changes[at.change]
				change.Pictures = append(change.Pictures, picture)
				break
			}
		}
	}
	for _, section := range r.Report.Sections() {
		if section.Group != GroupDone || section.Title == "Struck by the CFO" || section.Title == "Settled by the CFO" || section.Title == "Paused at a floor" {
			continue
		}
		for _, entry := range section.Entries {
			evidence := entry.Evidence
			if entry.Noted != "" {
				evidence = strings.TrimSpace(entry.Noted + " " + evidence)
			}
			url := PullRequestURL(entry.Link, entry.What)
			if at, ok := byPull[url]; ok && url != "" {
				change := &projects[at.project].Changes[at.change]
				switch {
				case entry.Kind == KindMerge && entry.Outcome != OutcomeMerged:
					change.NotLanded = NotLanded(entry.Outcome)
				case entry.Kind == KindDeploy || entry.Kind == KindMigration || entry.Kind == KindInstall:
					change.Deploys = append(change.Deploys, entry.What)
				}
				change.Evidence = append(change.Evidence, evidence)
				continue
			}
			change := Change{Title: entry.What, Evidence: []string{evidence}, At: entry.At}
			switch {
			case entry.Kind == KindMerge && url != "":
				change.Title, change.Pulls = r.Titles[url], []Pull{pullOf(url)}
				if change.Title == "" {
					change.Title = "Pull request " + pullOf(url).Number + " merged"
				}
				if entry.Outcome != OutcomeMerged {
					change.NotLanded = NotLanded(entry.Outcome)
				}
			case entry.Kind == KindDeploy || entry.Kind == KindMigration || entry.Kind == KindInstall:
				change.Deploys = []string{entry.What}
			}
			add(repositoryOf(url), change)
			if url != "" {
				byPull[url] = place{locate(repositoryOf(url)), len(projects[locate(repositoryOf(url))].Changes) - 1}
			}
		}
	}
	return projects
}

// Decided is each decision the CFO made for the Overlord in the stretch, his
// own first and then the goblins' questions, each in the order it was made,
// with the question in his History it changes.
func (r Return) Decided() (his, goblins []Decision) {
	for _, section := range r.Report.Sections() {
		if section.Group != GroupDecided {
			continue
		}
		for _, entry := range section.Entries {
			decision := Decision{Question: entry.What, Answer: strings.TrimSuffix(entry.Answer, "."), Why: entry.Diagnosis, Evidence: entry.Evidence, At: entry.At}
			if entry.Kind == KindAnswer {
				asked, answered := askedAndAnswered(entry.Evidence)
				decision.Task, decision.Question, decision.Evidence = entry.Task, firstSentence(asked), asked
				decision.Answer, decision.Why = splitAnswer(answered)
			}
			if question, ok := r.Questions[entry.Item]; ok {
				decision.Change = &question
				if question.Answer != "" {
					decision.Answer = question.Answer
				}
			}
			if entry.Kind == KindAnswer {
				goblins = append(goblins, decision)
			} else {
				his = append(his, decision)
			}
		}
	}
	return his, goblins
}

// askedAndAnswered splits the evidence cfo answer logs for a goblin's question
// into the question it asked and the answer it was given.
func askedAndAnswered(evidence string) (asked, answered string) {
	rest := strings.TrimPrefix(evidence, "asked: ")
	if i := strings.LastIndex(rest, " answered: "); i >= 0 {
		return strings.TrimSpace(rest[:i]), strings.TrimSpace(rest[i+len(" answered: "):])
	}
	return strings.TrimSpace(rest), ""
}

// splitAnswer reads an answer the CFO gave a goblin as the answer taken and
// why: the choice before the CFO's note, which follows "CFO under AFK:" or
// "CFO:", and the note's first sentence.
func splitAnswer(answered string) (answer, why string) {
	for _, marker := range []string{" CFO under AFK: ", " CFO: "} {
		if before, after, ok := strings.Cut(answered, marker); ok {
			return strings.TrimSuffix(strings.TrimSpace(before), "."), firstSentence(after)
		}
	}
	return strings.TrimSuffix(firstSentence(answered), "."), ""
}

// firstSentence is text up to the end of its first sentence, or its first
// detail line: a goblin's question leads with one short sentence.
func firstSentence(text string) string {
	text = strings.TrimSpace(text)
	if before, _, ok := strings.Cut(text, " - "); ok {
		text = before
	}
	for i, r := range text {
		if (r == '?' || r == '.') && (i+1 == len(text) || text[i+1] == ' ') {
			return text[:i+1]
		}
	}
	return text
}

// Away says how long the Overlord was away, in plain words.
func (r Report) Away() string {
	d := r.Ended.Sub(r.Since).Round(time.Minute)
	if d < time.Minute {
		return "under a minute"
	}
	hours, minutes := int(d/time.Hour), int(d/time.Minute)%60
	plural := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit
		}
		return strconv.Itoa(n) + " " + unit + "s"
	}
	switch {
	case hours == 0:
		return plural(minutes, "minute")
	case minutes == 0:
		return plural(hours, "hour")
	}
	return plural(hours, "hour") + " " + plural(minutes, "minute")
}

// Plain is a row of Spent in the Overlord's words, such as "Claude week 35 to
// 51 percent".
func (row Used) Plain() string {
	name := strings.ToUpper(row.Provider[:1]) + row.Provider[1:] + " " + row.Window
	switch {
	case row.Credits:
		return name + " " + number(row.Spent) + " " + row.Unit + " spent"
	case row.On == nil:
		return name + " " + number(*row.Off) + " percent when you came back"
	case row.Off == nil:
		return name + " " + number(*row.On) + " percent when you left"
	case row.Reset:
		return name + " " + number(*row.On) + " percent, reset, then " + number(*row.Off) + " percent"
	}
	return name + " " + number(*row.On) + " to " + number(*row.Off) + " percent"
}

// Plain is free disk in the Overlord's words.
func (use DiskUse) Plain() string {
	name := "Disk"
	if use.Drive != "" {
		name += " " + use.Drive
	}
	return fmt.Sprintf("%s %s GB free, then %s GB", name, number(disk.GB(use.On)), number(disk.GB(use.Off)))
}
