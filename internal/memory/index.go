package memory

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Item kinds. A ruling is one line of overlord.md, a fact one memory file,
// an archive item one line of memory-archive.md and a done item a task's
// last done report.
const (
	KindRuling  = "ruling"
	KindFact    = "fact"
	KindArchive = "archive"
	KindDone    = "done"
)

var (
	datePattern     = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	headingLine     = regexp.MustCompile(`^#{1,6}(\s|$)`)
	doneRow         = regexp.MustCompile(`^- \[x\] (\S+) - (.*)$`)
	doneRowDate     = regexp.MustCompile(`\(done (\d{4}-\d{2}-\d{2})\)`)
	doneRowRepo     = regexp.MustCompile(`\(repo: ([^)]+)\)`)
	archivedHeading = regexp.MustCompile(`^## Archived (\d{4}-\d{2}-\d{2})`)
)

// Item is one recallable piece of a memory file. Path and Line name the
// source a reader opens for the whole text; nothing here replaces it.
type Item struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Date       time.Time `json:"date"`
	Path       string    `json:"path"`
	Line       int       `json:"line"`
	Title      string    `json:"title"`
	Text       string    `json:"-"`
	Projects   []string  `json:"projects,omitempty"`
	Task       string    `json:"task,omitempty"`
	Roles      []string  `json:"roles,omitempty"`
	Supersedes []string  `json:"supersedes,omitempty"`
	// IsIncident marks a fact that records something that happened, which
	// fades with age; a done report is always one.
	IsIncident bool `json:"incident,omitempty"`
	// Lesson is the id of the lesson a fact was accepted from, empty for a
	// fact written any other way.
	Lesson string `json:"lesson,omitempty"`
}

type posting struct {
	item  int
	count int
}

type stamp struct {
	size     int64
	modified time.Time
}

// Index is a keyword index of the memory files, rebuilt from them and never
// written back. Problems names sources it could not read in full.
type Index struct {
	Items      []Item
	Problems   []string
	BuiltAt    time.Time
	dataDir    string
	stateDir   string
	postings   map[string][]posting
	lengths    []int
	average    float64
	superseded map[string]bool
	stamps     map[string]stamp
}

// BuildIndex reads every memory source under dataDir and the task records
// under stateDir. A source that cannot be parsed is indexed as plain text
// and named in Problems; a source that cannot be read at all is an error,
// except one removed while the build ran, which the next look sees gone.
func BuildIndex(dataDir, stateDir string, now time.Time) (*Index, error) {
	index := &Index{BuiltAt: now.UTC(), dataDir: dataDir, stateDir: stateDir, postings: map[string][]posting{}, superseded: map[string]bool{}}
	sources, err := indexSources(dataDir, stateDir)
	if err != nil {
		return nil, err
	}
	if index.stamps, err = stampsOf(sources); err != nil {
		return nil, err
	}
	if err := index.addLines(filepath.Join(dataDir, "overlord.md"), KindRuling); err != nil {
		return nil, err
	}
	if err := index.addLines(filepath.Join(dataDir, "memory-archive.md"), KindArchive); err != nil {
		return nil, err
	}
	if err := index.addFacts(filepath.Join(dataDir, "memory")); err != nil {
		return nil, err
	}
	if err := index.addDone(dataDir, stateDir); err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, item := range index.Items {
		ids[item.ID] = true
	}
	for _, item := range index.Items {
		for _, previous := range item.Supersedes {
			if !ids[previous] {
				index.Problems = append(index.Problems, fmt.Sprintf("%s supersedes %s, which no source holds", item.ID, previous))
				continue
			}
			index.superseded[previous] = true
		}
	}
	total := 0
	index.lengths = make([]int, len(index.Items))
	for position, item := range index.Items {
		counts := map[string]int{}
		title := Terms(item.Title)
		words := slices.Concat(title, title, title, Terms(item.Title+" "+item.Text))
		for _, word := range words {
			counts[word]++
		}
		for word, count := range counts {
			index.postings[word] = append(index.postings[word], posting{position, count})
		}
		index.lengths[position] = len(words)
		total += len(words)
	}
	if len(index.Items) > 0 {
		index.average = float64(total) / float64(len(index.Items))
	}
	return index, nil
}

// IsChanged reports whether any source the index was built from changed,
// appeared or disappeared since.
func (index *Index) IsChanged() (bool, error) {
	sources, err := indexSources(index.dataDir, index.stateDir)
	if err != nil {
		return false, err
	}
	current, err := stampsOf(sources)
	if err != nil {
		return false, err
	}
	if len(current) != len(index.stamps) {
		return true, nil
	}
	for path, value := range current {
		if index.stamps[path] != value {
			return true, nil
		}
	}
	return false, nil
}

// IsSuperseded reports whether a newer item names id as superseded.
func (index *Index) IsSuperseded(id string) bool { return index.superseded[id] }

func indexSources(dataDir, stateDir string) ([]string, error) {
	var sources []string
	for _, name := range []string{"overlord.md", "memory-archive.md", "done-archive.md"} {
		sources = append(sources, filepath.Join(dataDir, name))
	}
	facts, err := filepath.Glob(filepath.Join(dataDir, "memory", "*.md"))
	if err != nil {
		return nil, err
	}
	tasks, err := filepath.Glob(filepath.Join(stateDir, "*.status"))
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		id := strings.TrimSuffix(filepath.Base(task), ".status")
		sources = append(sources, task, state.TaskMetaPath(stateDir, id), filepath.Join(stateDir, "outcomes", id+".json"))
	}
	return append(sources, facts...), nil
}

// stampsOf records each existing source's size and modification time; a
// source that does not exist has no stamp, so its arrival is a change.
func stampsOf(sources []string) (map[string]stamp, error) {
	stamps := make(map[string]stamp, len(sources))
	for _, source := range sources {
		info, err := os.Stat(source)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		stamps[source] = stamp{info.Size(), info.ModTime()}
	}
	return stamps, nil
}

func readOptional(path string) ([]byte, error) {
	content, err := fsx.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return content, err
}

// addLines indexes one item per nonblank line under its heading, dated by
// the first date the heading names. Headings and tier comments are not items.
func (index *Index) addLines(path, kind string) error {
	content, err := readOptional(path)
	if err != nil {
		return err
	}
	heading, date := "", time.Time{}
	seen := map[string]bool{}
	for number, line := range strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n") {
		text := strings.TrimSpace(line)
		switch {
		case text == "" || strings.HasPrefix(text, "<!--"):
			continue
		case headingLine.MatchString(text):
			heading = strings.TrimSpace(strings.TrimLeft(text, "#"))
			date = time.Time{}
			if found := datePattern.FindString(heading); found != "" {
				date, _ = time.Parse(time.DateOnly, found)
			}
			continue
		}
		id := kind + "-" + Hash([]byte(heading + "\n" + text))[:12]
		if seen[id] {
			continue
		}
		seen[id] = true
		index.Items = append(index.Items, Item{ID: id, Kind: kind, Date: date, Path: path, Line: number + 1, Title: heading, Text: text})
	}
	return nil
}

type factFrontmatter struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Metadata    map[string]any `yaml:"metadata"`
}

// addFacts indexes one item per fact file, named by its file. Frontmatter
// that does not parse leaves the file indexed by its text and a problem.
func (index *Index) addFacts(directory string) error {
	paths, err := filepath.Glob(filepath.Join(directory, "*.md"))
	if err != nil {
		return err
	}
	slices.Sort(paths)
	for _, path := range paths {
		if strings.EqualFold(filepath.Base(path), "MEMORY.md") {
			continue
		}
		content, err := fsx.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		info, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		item := Item{ID: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Kind: KindFact, Date: info.ModTime().UTC(), Path: path, Line: 1}
		body := strings.ReplaceAll(string(content), "\r\n", "\n")
		var front factFrontmatter
		if rest, isFenced := strings.CutPrefix(body, "---\n"); isFenced {
			// The newlines around rest let an empty header and a fence that
			// ends the file close it too.
			header, after, isClosed := strings.Cut("\n"+rest+"\n", "\n---\n")
			if !isClosed {
				index.Problems = append(index.Problems, path+": frontmatter is not closed")
			} else if err := yaml.Unmarshal([]byte(header), &front); err != nil {
				index.Problems = append(index.Problems, fmt.Sprintf("%s: frontmatter: %v", path, err))
			} else {
				body = after
			}
		}
		item.Title, item.Text = front.Description, front.Name+"\n"+body
		if modified, isDated := dateOf(front.Metadata["modified"]); isDated {
			item.Date = modified
		}
		item.Projects = append(stringsOf(front.Metadata["projects"]), stringsOf(front.Metadata["project"])...)
		item.Roles = stringsOf(front.Metadata["roles"])
		item.Supersedes = stringsOf(front.Metadata["supersedes"])
		item.IsIncident = front.Metadata["incident"] == true
		if lessons := stringsOf(front.Metadata["lesson"]); len(lessons) > 0 {
			item.Lesson = lessons[0]
		}
		if tasks := stringsOf(front.Metadata["task"]); len(tasks) > 0 {
			item.Task = tasks[0]
		}
		index.Items = append(index.Items, item)
	}
	return nil
}

func dateOf(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC(), true
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.DateOnly} {
			if parsed, err := time.Parse(layout, typed); err == nil {
				return parsed.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

func stringsOf(value any) []string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			return []string{strings.TrimSpace(typed)}
		}
	case []any:
		var values []string
		for _, element := range typed {
			values = append(values, stringsOf(element)...)
		}
		return values
	}
	return nil
}

// addDone indexes each task's latest done report: the done archive's rows
// and the live status logs, whose newer report wins for the same task.
func (index *Index) addDone(dataDir, stateDir string) error {
	latest := map[string]Item{}
	keep := func(item Item) {
		if previous, exists := latest[item.Task]; !exists || !item.Date.Before(previous.Date) {
			latest[item.Task] = item
		}
	}
	archive := filepath.Join(dataDir, "done-archive.md")
	content, err := readOptional(archive)
	if err != nil {
		return err
	}
	var current *Item
	section := time.Time{}
	flush := func() {
		if current != nil {
			keep(*current)
			current = nil
		}
	}
	for number, line := range strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n") {
		if match := archivedHeading.FindStringSubmatch(line); match != nil {
			flush()
			section, _ = time.Parse(time.DateOnly, match[1])
			continue
		}
		if match := doneRow.FindStringSubmatch(line); match != nil {
			flush()
			item := Item{ID: KindDone + "-" + match[1], Kind: KindDone, Date: section, Path: archive, Line: number + 1, Task: match[1], Title: match[2]}
			if date := doneRowDate.FindStringSubmatch(line); date != nil {
				item.Date, _ = time.Parse(time.DateOnly, date[1])
			}
			if repo := doneRowRepo.FindStringSubmatch(line); repo != nil {
				item.Projects = []string{strings.TrimSpace(repo[1])}
			}
			current = &item
			continue
		}
		if current != nil && strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "" {
			current.Text = strings.TrimSpace(current.Text + "\n" + strings.TrimSpace(line))
			continue
		}
		flush()
	}
	flush()
	logs, err := filepath.Glob(filepath.Join(stateDir, "*.status"))
	if err != nil {
		return err
	}
	for _, path := range logs {
		item, isDone, err := lastDone(stateDir, path)
		if err != nil {
			return err
		}
		if isDone {
			keep(item)
		}
	}
	tasks := make([]string, 0, len(latest))
	for task := range latest {
		tasks = append(tasks, task)
	}
	slices.Sort(tasks)
	for _, task := range tasks {
		index.Items = append(index.Items, latest[task])
	}
	return nil
}

// lastDone reads a status log's last done report, titled and scoped by the
// task's record or, once the task is gone, its outcome.
func lastDone(stateDir, path string) (Item, bool, error) {
	content, err := fsx.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, err
	}
	lines := bytes.Split(content, []byte("\n"))
	for number := len(lines) - 1; number >= 0; number-- {
		line := strings.TrimSpace(string(lines[number]))
		at, detail := state.SplitStatus(line)
		report, isDone := strings.CutPrefix(detail, "done:")
		if !isDone || at.IsZero() {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(path), ".status")
		item := Item{ID: KindDone + "-" + id, Kind: KindDone, Date: at.UTC(), Path: path, Line: number + 1, Task: id, Title: id, Text: strings.TrimSpace(report)}
		project, title := "", ""
		if meta, err := state.ReadTaskMeta(stateDir, id); err == nil {
			project, title = meta.Project, meta.Title
		} else if outcome, err := state.ReadOutcome(filepath.Join(stateDir, "outcomes"), id); err == nil {
			project, title = outcome.Project, outcome.Title
		}
		if project != "" {
			item.Projects = []string{filepath.Base(project)}
		}
		if title != "" {
			item.Title = title
		}
		return item, true, nil
	}
	return Item{}, false, nil
}

var stopWords = map[string]bool{}

func init() {
	for _, word := range strings.Fields("a an and are as at be been but by can could did do does for from had has have how i if in into is it its may me might must my no not of on or our should so than that the their them then there these they this those to was we were what when where which while who why will with would you your") {
		stopWords[word] = true
	}
}

// Terms is the lowercase words of text that carry meaning for ranking, each
// cut to its stem. A lone letter, such as a possessive's s, carries none.
func Terms(text string) []string {
	var terms []string
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(character rune) bool { return !unicode.IsLetter(character) && !unicode.IsNumber(character) }) {
		if utf8.RuneCountInString(word) > 1 && !stopWords[word] {
			terms = append(terms, stem(word))
		}
	}
	return terms
}

// stem drops the plural and the -ed and -ing endings of an English word, so
// "deployed", "deploys" and "deploying" all rank as "deploy". A stem keeps at
// least three letters, a doubled final consonant left by an ending is
// undoubled, so "running" ranks as "run", and a final e goes, so "merge" and
// "merged" both rank as "merg".
func stem(word string) string {
	word = stemEnding(word)
	if len(word) > 3 && strings.HasSuffix(word, "e") {
		return word[:len(word)-1]
	}
	return word
}

func stemEnding(word string) string {
	switch {
	case len(word) > 4 && strings.HasSuffix(word, "ies"):
		return word[:len(word)-3] + "y"
	case len(word) > 4 && (strings.HasSuffix(word, "ches") || strings.HasSuffix(word, "shes") || strings.HasSuffix(word, "sses") || strings.HasSuffix(word, "xes")):
		word = word[:len(word)-2]
	case len(word) > 3 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") && !strings.HasSuffix(word, "us") && !strings.HasSuffix(word, "is"):
		word = word[:len(word)-1]
	}
	for _, ending := range []string{"ing", "ied", "ed"} {
		base, found := strings.CutSuffix(word, ending)
		if !found || len(base) < 3 {
			continue
		}
		if ending == "ied" {
			return base + "y"
		}
		last := base[len(base)-1]
		if last >= 'a' && last <= 'z' && last == base[len(base)-2] && !strings.ContainsRune("aeioulsz", rune(last)) {
			base = base[:len(base)-1]
		}
		return base
	}
	return word
}

// score is BM25 with k1 1.2 and b 0.3 over the item's title, counted four
// times, and its text. The low b and the heavy title are measured, not
// guessed: a ruling is one line and a fact a whole file, so length says
// little about relevance, and a fact's description is its summary.
func (index *Index) score(terms []string, isEligible func(int) bool) map[int]float64 {
	const k1, b = 1.2, 0.3
	scores := map[int]float64{}
	seen := map[string]bool{}
	total := float64(len(index.Items))
	for _, term := range terms {
		if seen[term] {
			continue
		}
		seen[term] = true
		postings := index.postings[term]
		if len(postings) == 0 {
			continue
		}
		frequency := float64(len(postings))
		weight := math.Log(1 + (total-frequency+0.5)/(frequency+0.5))
		for _, entry := range postings {
			if !isEligible(entry.item) {
				continue
			}
			count := float64(entry.count)
			length := float64(index.lengths[entry.item])
			scores[entry.item] += weight * count * (k1 + 1) / (count + k1*(1-b+b*length/index.average))
		}
	}
	return scores
}
