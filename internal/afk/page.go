package afk

import (
	_ "embed"
	"encoding/json"
	"html/template"
	"io"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/disk"
)

// pageSource is the page the Overlord comes back to, drawn with the board's
// own cards, icons and goblin faces.
//
//go:embed page.html
var pageSource string

// icons are the board's line icons the page draws, from frontend/src/Icon.tsx.
var icons = map[string]string{
	"book":           "M4 5.5A1.5 1.5 0 0 1 5.5 4H11v16H5.5A1.5 1.5 0 0 1 4 18.5ZM20 5.5A1.5 1.5 0 0 0 18.5 4H13v16h5.5a1.5 1.5 0 0 0 1.5-1.5Z",
	"check":          "M5 12.5 9.5 17 19 7.5",
	"claude":         "M12 3v18M3 12h18M5.6 5.6l12.8 12.8M18.4 5.6 5.6 18.4M8.6 3.7l6.8 16.6M15.4 3.7 8.6 20.3M3.7 8.6l16.6 6.8M3.7 15.4l16.6-6.8",
	"clock":          "M12 3.5a8.5 8.5 0 1 0 0 17 8.5 8.5 0 0 0 0-17ZM12 7.5V12l3 2",
	"close":          "M6 6l12 12M18 6 6 18",
	"codex":          "M14.02 4.47A4.62 4.62 0 0 1 19.53 9.98 4.62 4.62 0 0 1 17.52 17.52 4.62 4.62 0 0 1 9.98 19.53 4.62 4.62 0 0 1 4.47 14.02 4.62 4.62 0 0 1 6.48 6.48 4.62 4.62 0 0 1 14.02 4.47ZM8.7 9.7l1.5 2.3-1.5 2.3M13 14.3h2.8",
	"comment":        "M5 4.5h14A1.5 1.5 0 0 1 20.5 6v9a1.5 1.5 0 0 1-1.5 1.5h-7l-4.5 3.5v-3.5H5A1.5 1.5 0 0 1 3.5 15V6A1.5 1.5 0 0 1 5 4.5Z",
	"command-center": "M6.5 3.5h11a2.5 2.5 0 0 1 2.5 2.5v7.5a2.5 2.5 0 0 1-2.5 2.5H11l-4.5 4v-4a2.5 2.5 0 0 1-2.5-2.5V6a2.5 2.5 0 0 1 2.5-2.5ZM8.5 12.5l-.75-5 2.75 2L12 6.5l1.5 3 2.75-2-.75 5Z",
	"download":       "M12 4v11M7 10.5l5 5 5-5M5 19.5h14",
	"drive":          "M3.5 13.5h17M6.3 5.5 3.5 13.5V18A1.5 1.5 0 0 0 5 19.5h14a1.5 1.5 0 0 0 1.5-1.5v-4.5l-2.8-8a1.5 1.5 0 0 0-1.4-1H7.7a1.5 1.5 0 0 0-1.4 1ZM7 16.5h.01M10.5 16.5h.01",
	"edit":           "m4 16-1 5 5-1L20 8l-4-4ZM14 6l4 4",
	"external":       "M14 4h6v6M20 4l-8 8M10 5H6a2 2 0 0 0-2 2v11a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2v-4",
	"folder":         "M3.5 7a2 2 0 0 1 2-2H9l2 2h7.5a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2Z",
	"images":         "M7.5 3.5h12a1 1 0 0 1 1 1v12a1 1 0 0 1-1 1h-12a1 1 0 0 1-1-1v-12a1 1 0 0 1 1-1ZM3.5 7.5v12a1 1 0 0 0 1 1h12M6.5 14l4-4 3 3 2-2 5 5M15 7.5h.01",
	"key":            "M14.5 4a5.5 5.5 0 1 0 0 11 5.5 5.5 0 0 0 0-11ZM10.6 13 3.5 20.1M5.5 18.1l2 2M8 15.6l2 2",
	"lock":           "M7.5 10.5V8a4.5 4.5 0 0 1 9 0v2.5M6 10.5h12a1.5 1.5 0 0 1 1.5 1.5v7a1.5 1.5 0 0 1-1.5 1.5H6A1.5 1.5 0 0 1 4.5 19v-7A1.5 1.5 0 0 1 6 10.5ZM12 14.5v2.5",
	"merge":          "M6 4a2 2 0 1 0 0 4 2 2 0 0 0 0-4ZM6 16a2 2 0 1 0 0 4 2 2 0 0 0 0-4ZM18 12a2 2 0 1 0 0 4 2 2 0 0 0 0-4ZM6 8v8M6 8a6 6 0 0 0 6 6h4",
	"moon":           "M19.5 14.5A7.5 7.5 0 0 1 9.5 4.5a7.5 7.5 0 1 0 10 10Z",
	"next":           "M5 12h14M13 6l6 6-6 6",
	"pause-circle":   "M12 3.5a8.5 8.5 0 1 0 0 17 8.5 8.5 0 0 0 0-17ZM9.5 9v6M14.5 9v6",
	"pi":             "M5 7h14M9.5 7v11M14.5 7v8.5a2.5 2.5 0 0 0 2.5 2.5",
	"play":           "M8 5.5v13l11-6.5Z",
	"question":       "M12 3.5a8.5 8.5 0 1 0 0 17 8.5 8.5 0 0 0 0-17ZM9.5 9.5a2.5 2.5 0 1 1 3.5 2.3c-.6.3-1 .8-1 1.5v.7M12 16.8v.2",
	"refresh":        "M19.5 12a7.5 7.5 0 1 1-2.2-5.3M19.5 4v4.5H15",
	"send":           "M20.5 3.5 10.5 13.5M20.5 3.5l-6.5 17-3.5-7-7-3.5Z",
	"sparkle":        "M12 3.5 13.9 10.1 20.5 12 13.9 13.9 12 20.5 10.1 13.9 3.5 12 10.1 10.1Z",
	"warning":        "M10.3 4.5 2.8 17.6A2 2 0 0 0 4.5 20.5h15a2 2 0 0 0 1.7-2.9L13.7 4.5a2 2 0 0 0-3.4 0ZM12 9.5v4.5M12 17.2v.3",
}

// page is the return page's template.
var page = template.Must(template.New("page").Funcs(template.FuncMap{
	"icon": func(name string) template.HTML {
		return template.HTML(`<svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="` + icons[name] + `"/></svg>`)
	},
	"who": func(goblin string) string { return cmpOr(goblin, "A goblin") },
	"lines": func(lines []string) string {
		data, _ := json.Marshal(slices.DeleteFunc(slices.Clone(lines), func(line string) bool { return strings.TrimSpace(line) == "" }))
		return string(data)
	},
	"pictures": func(pictures []Picture) string {
		type shown struct {
			File    string `json:"file"`
			Caption string `json:"caption"`
		}
		var all []shown
		for _, picture := range pictures {
			all = append(all, shown{picture.File, picture.Caption})
		}
		data, _ := json.Marshal(all)
		return string(data)
	},
}).Parse(pageSource))

// cmpOr is a, or b when a is empty.
func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// pageView is what the page shows, in its order.
type pageView struct {
	Away     string
	Needs    []needRow
	Merged   int
	Decided  int
	Faces    []string
	Projects []projectRow
	His      []decisionRow
	Goblins  []decisionRow
	UnderWay []ongoingRow
	Next     []ongoingRow
	Spent    []spentRow
	Record   []recordRow
}

// needRow is one thing that needs him, drawn as the Command Center's waiting
// row: who asks, what, and for an item in his Command Center its key and the
// icon its Answer wears there. A step with no item opens its own card.
type needRow struct {
	Title, Who, Persona, Item, Answer string
	Body                              []string
}

// answerIcons are the icons the Command Center's Answer wears on a waiting
// row, by the kind its item key starts with, from CommandCenter.tsx.
var answerIcons = map[string]string{"question": "question", "review": "comment", "run": "play", "credential": "key", "browser-signin": "lock"}

// releaseUpdate is the key of the board's own release update run, which
// releases.go names update-<tag>-<16 hex nonce>. The CFO names its own runs,
// update-node-1 among them, so the prefix alone is no release update.
var releaseUpdate = regexp.MustCompile(`^run:update-.+-[0-9a-f]{16}$`)

type projectRow struct {
	Name    string
	Changes []changeRow
}

// shownPulls is how many pull request numbers a change's row shows, so they
// stay on one line on a phone: the rest are counted, and listed on its card.
const shownPulls = 2

type changeRow struct {
	Change
	Persona string
	Body    []string
	Shown   []Pull
	More    int
}

type decisionRow struct {
	Decision
	Who, Persona string
	Body         []string
}

type ongoingRow struct {
	Ongoing
	Persona string
	Body    []string
}

type spentRow struct {
	Mark, Name, Value, Change, Says string
	Used, Reset, Bar                bool
	Before, From, Width             float64
}

type recordRow struct {
	Title, Kind, Mark string
	Struck            bool
	Body              []string
}

// RenderPage writes the page the Overlord comes back to: how long he was
// away and whether anything needs him, what changed by project, each decision
// made for him with Change, which opens its answer in History, what
// is still under way and next, what was spent, and the record of the
// stretch: who switched it, what was struck, settled or paused, and what
// could not be read. Each row opens a card with what it stood on.
func RenderPage(w io.Writer, r Return) error {
	return page.Execute(w, r.view())
}

func (r Return) view() pageView {
	report := r.Report
	titles := map[string]string{}
	for _, finish := range report.Finished {
		titles[finish.Task] = finish.Title
	}
	for _, one := range slices.Concat(report.UnderWay, report.Next) {
		titles[one.Task] = one.Title
	}
	names := map[string]string{}
	for _, finish := range report.Finished {
		names[finish.Task] = cmpOr(finish.Goblin, names[finish.Task])
	}
	for _, one := range report.UnderWay {
		names[one.Task] = cmpOr(one.Goblin, names[one.Task])
	}
	persona := func(task string) string { return Persona(task, titles[task]) }
	v := pageView{Away: report.Away()}
	for _, section := range report.Sections() {
		switch {
		case section.Group == GroupNeeds:
			for _, entry := range section.Entries {
				v.Needs = append(v.Needs, needRow{Title: entry.What, Who: "The CFO", Persona: "cfo", Body: []string{entry.Link, entry.Evidence}})
			}
		case section.Title == "Merged":
			v.Merged = len(section.Entries)
		}
	}
	for _, held := range report.Held {
		if !held.Waiting {
			continue
		}
		// As the Command Center names who asks: Code Goblins for its own
		// release update, a run of no task, the goblin, or the CFO for an item
		// of no task.
		kind, _, _ := strings.Cut(held.Item, ":")
		who, face, answer := "The CFO", "cfo", answerIcons[kind]
		switch {
		case held.Task == "" && releaseUpdate.MatchString(held.Item):
			who, face, answer = "Code Goblins", "releases", "download"
		case held.Task != "":
			who, face = cmpOr(names[held.Task], held.Task), persona(held.Task)
		}
		v.Needs = append(v.Needs, needRow{Item: held.Item, Title: held.What, Who: who, Persona: face, Answer: answer})
	}
	for _, project := range r.Walkthrough() {
		row := projectRow{Name: project.Name}
		for _, change := range project.Changes {
			body := append(slices.Clone(change.Notes), change.NotLanded)
			for _, pull := range change.Pulls {
				body = append(body, pull.URL)
			}
			for _, evidence := range change.Evidence {
				body = append(body, "Evidence: "+evidence)
			}
			face := "cfo"
			if change.Task != "" {
				face = persona(change.Task)
			}
			shown := change.Pulls[:min(len(change.Pulls), shownPulls)]
			row.Changes = append(row.Changes, changeRow{Change: change, Persona: face, Body: body, Shown: shown, More: len(change.Pulls) - len(shown)})
		}
		v.Projects = append(v.Projects, row)
	}
	his, goblins := r.Decided()
	for _, decision := range his {
		v.His = append(v.His, decisionRow{Decision: decision, Who: "The CFO", Persona: "cfo", Body: []string{decision.Why, "Evidence: " + decision.Evidence}})
	}
	// A goblin's question names its task: its face and its name are that
	// task's goblin's.
	for _, decision := range goblins {
		v.Goblins = append(v.Goblins, decisionRow{Decision: decision, Who: cmpOr(names[decision.Task], "A goblin"), Persona: persona(decision.Task), Body: []string{decision.Why, "Asked: " + decision.Evidence}})
	}
	v.Decided = len(v.His) + len(v.Goblins)
	for _, one := range report.UnderWay {
		v.UnderWay = append(v.UnderWay, ongoingRow{Ongoing: one, Persona: persona(one.Task), Body: []string{one.Note}})
		if len(v.Faces) < 3 {
			v.Faces = append(v.Faces, persona(one.Task))
		}
	}
	for _, one := range report.Next {
		v.Next = append(v.Next, ongoingRow{Ongoing: one, Persona: persona(one.Task), Body: []string{one.Note}})
	}
	v.Spent = spentRows(report)
	v.Record = recordRows(report)
	return v
}

// spentRows are what the stretch spent, as the board's AFK bars draw it: gray
// what was used before it, green what it used, and what is left empty.
func spentRows(report Report) []spentRow {
	marks := map[string]string{"claude": "claude", "codex": "codex", "pi": "pi"}
	var rows []spentRow
	for _, used := range Spent(report.Before, report.After) {
		row := spentRow{Mark: cmpOr(marks[used.Provider], "sparkle"), Name: strings.ToUpper(used.Provider[:1]) + used.Provider[1:] + " " + used.Window, Says: used.Plain()}
		switch {
		case used.Credits:
			row.Value, row.Change, row.Used = number(used.Spent)+" "+used.Unit, "spent", true
		case used.Off == nil:
			row.Value, row.Bar, row.Before = number(*used.On)+"%", true, *used.On
		case used.On == nil || used.Reset:
			row.Value, row.Bar, row.Used, row.Width = number(*used.Off)+"%", true, true, *used.Off
			row.Reset, row.Change = used.Reset, "reset"
		default:
			row.Value, row.Bar, row.Used = number(*used.Off)+"%", true, *used.Off > *used.On
			row.Before, row.From, row.Width = math.Min(*used.On, *used.Off), math.Min(*used.On, *used.Off), math.Abs(*used.Off-*used.On)
			if moved := *used.Off - *used.On; moved >= 0 {
				row.Change = "+" + number(moved)
			} else {
				row.Change = number(moved)
			}
		}
		rows = append(rows, row)
	}
	if use := DiskUsed(report.DiskBefore, report.DiskAfter); use != nil {
		row := spentRow{Mark: "drive", Name: cmpOr(strings.TrimSpace("Disk "+use.Drive), "Disk"), Value: number(disk.GB(use.Off)) + " GB free", Says: use.Plain(), Bar: use.Total > 0}
		if use.Total > 0 {
			row.Before = 100 * float64(use.Total-use.Off) / float64(use.Total)
		}
		if moved := math.Round((disk.GB(use.Off)-disk.GB(use.On))*10) / 10; moved != 0 {
			row.Change = number(moved) + " GB"
			if moved > 0 {
				row.Change = "+" + row.Change
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// ownProgram is the program and process the supervisor names where he made a
// switch, which he does not read.
var ownProgram = regexp.MustCompile(` \(.*\)$`)

// switchedForHim says who made a switch in the words the board's own switch
// uses for him: from his board or terminal, or by the CFO at his ask with
// his words.
func switchedForHim(from, asked string) string {
	if asked != "" {
		return "by the CFO at your ask: “" + asked + "”"
	}
	return "from " + ownProgram.ReplaceAllString(strings.Replace(from, "his own ", "your ", 1), "")
}

// recordRows are the rest of what the report holds: who switched AFK mode on
// and off, each item that waited on him and no longer does, each goblin
// paused at a floor, each line the CFO settled or struck, and what could not
// be read.
func recordRows(report Report) []recordRow {
	rows := []recordRow{{Title: "Turned on " + switchedForHim(report.From, report.Asked), Kind: at(report.Since), Mark: "clock"}, {Title: "Turned off " + switchedForHim(report.EndedFrom, report.EndedAsked), Kind: at(report.Ended), Mark: "clock"}}
	for _, held := range report.Held {
		if !held.Waiting {
			rows = append(rows, recordRow{Title: held.What, Kind: "Settled", Mark: "check", Body: []string{held.Now, held.Meanwhile, Recommends(held.Task, held.Recommendation, false)}})
		}
	}
	for _, section := range report.Sections() {
		kind, mark := "", ""
		switch section.Title {
		case "Paused at a floor":
			kind, mark = "Paused", "pause-circle"
		case "Settled by the CFO":
			kind, mark = "Settled by the CFO", "check"
		case "Struck by the CFO":
			kind, mark = "Struck by the CFO", "close"
		default:
			continue
		}
		for _, entry := range section.Entries {
			title := entry.What
			if entry.Task != "" {
				title = entry.Task + ": " + title
			}
			rows = append(rows, recordRow{Title: title, Kind: kind, Mark: mark, Struck: entry.Struck != "", Body: []string{entry.Outcome, entry.Settled, entry.Struck, "Evidence: " + entry.Evidence}})
		}
	}
	for _, note := range report.Notes {
		rows = append(rows, recordRow{Title: note, Kind: "Not read", Mark: "warning"})
	}
	return rows
}
