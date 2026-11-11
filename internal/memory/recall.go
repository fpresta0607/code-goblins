package memory

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// DefaultRecallLimit is how many items recall returns unless asked.
const DefaultRecallLimit = 5

// RecallQuery asks the index a question. A filter excludes items scoped to
// another project, task or role; an item with no such scope is fleet-wide
// and passes it. Superseded and archived items show only with IsHistory.
type RecallQuery struct {
	Question  string    `json:"question"`
	Project   string    `json:"project,omitempty"`
	Repo      string    `json:"repo,omitempty"`
	Task      string    `json:"task,omitempty"`
	Role      string    `json:"role,omitempty"`
	Kinds     []string  `json:"kinds,omitempty"`
	Since     time.Time `json:"since,omitzero"`
	Until     time.Time `json:"until,omitzero"`
	IsHistory bool      `json:"is_history,omitempty"`
	Limit     int       `json:"limit"`
}

// Recalled is one ranked item, with a short excerpt in place of its text:
// the reader opens Path at Line for the whole source.
type Recalled struct {
	Item
	Excerpt      string  `json:"excerpt"`
	Score        float64 `json:"score"`
	IsSuperseded bool    `json:"is_superseded,omitempty"`
}

// RecallResult is what one question found, and how long the search took.
type RecallResult struct {
	Items    []Recalled    `json:"items"`
	Indexed  int           `json:"indexed"`
	Hidden   int           `json:"hidden"`
	Elapsed  time.Duration `json:"elapsed"`
	BuiltAt  time.Time     `json:"built_at"`
	Problems []string      `json:"problems,omitempty"`
}

const excerptLimit = 240

func (query RecallQuery) validate() error {
	if len(Terms(query.Question)) == 0 {
		return errors.New("recall needs a question with at least one searchable word")
	}
	if query.Limit < 1 || query.Limit > 50 {
		return errors.New("recall limit must be between 1 and 50")
	}
	if !query.Since.IsZero() && !query.Until.IsZero() && query.Until.Before(query.Since) {
		return errors.New("recall --until is before --since")
	}
	return nil
}

func scoped(keys []string, wanted string) bool {
	return wanted == "" || len(keys) == 0 || slices.ContainsFunc(keys, func(key string) bool { return strings.EqualFold(key, wanted) })
}

// IncidentHalfLife is the age at which an incident, a done report or a fact
// marked incident, counts half in recall. Rulings and other facts are rules:
// they never fade, and only a fact that supersedes one hides it.
const IncidentHalfLife = 30 * 24 * time.Hour

// Recall ranks the items that pass query's filters by BM25, with incidents
// faded by age, and returns the best Limit of them, newest first among equal
// scores.
func (index *Index) Recall(query RecallQuery) (RecallResult, error) {
	if err := query.validate(); err != nil {
		return RecallResult{}, err
	}
	started := time.Now()
	repo := query.Repo
	if slash := strings.LastIndex(repo, "/"); slash >= 0 {
		repo = repo[slash+1:]
	}
	hidden := map[int]bool{}
	isEligible := func(position int) bool {
		item := index.Items[position]
		if !scoped(item.Projects, query.Project) || !scoped(item.Projects, repo) || !scoped(item.Roles, query.Role) {
			return false
		}
		if query.Task != "" && item.Task != "" && !strings.EqualFold(item.Task, query.Task) {
			return false
		}
		if len(query.Kinds) > 0 && !slices.Contains(query.Kinds, item.Kind) {
			return false
		}
		if (!query.Since.IsZero() || !query.Until.IsZero()) && item.Date.IsZero() {
			return false
		}
		if !query.Since.IsZero() && item.Date.Before(query.Since) || !query.Until.IsZero() && item.Date.After(query.Until) {
			return false
		}
		if !query.IsHistory && (item.Kind == KindArchive || index.superseded[item.ID]) {
			hidden[position] = true
			return false
		}
		return true
	}
	scores := index.score(Terms(query.Question), isEligible)
	positions := make([]int, 0, len(scores))
	for position := range scores {
		item := index.Items[position]
		if (item.Kind == KindDone || item.IsIncident) && !item.Date.IsZero() && item.Date.Before(index.BuiltAt) {
			scores[position] *= math.Pow(0.5, float64(index.BuiltAt.Sub(item.Date))/float64(IncidentHalfLife))
		}
		positions = append(positions, position)
	}
	slices.SortFunc(positions, func(left, right int) int {
		if scores[left] != scores[right] {
			if scores[left] > scores[right] {
				return -1
			}
			return 1
		}
		if compared := index.Items[right].Date.Compare(index.Items[left].Date); compared != 0 {
			return compared
		}
		return strings.Compare(index.Items[left].ID, index.Items[right].ID)
	})
	result := RecallResult{Indexed: len(index.Items), Hidden: len(hidden), BuiltAt: index.BuiltAt, Problems: index.Problems}
	for _, position := range positions[:min(query.Limit, len(positions))] {
		item := index.Items[position]
		result.Items = append(result.Items, Recalled{Item: item, Excerpt: excerpt(item), Score: scores[position], IsSuperseded: index.superseded[item.ID]})
	}
	result.Elapsed = time.Since(started)
	return result, nil
}

// replyProblems is how many index problems a shortened result names.
const replyProblems = 10

// Shortened is the result cut to what RenderRecall shows, so the largest
// recall still fits one supervisor pipe reply: titles and problems at
// excerpt length, and at most replyProblems problems and a count of the rest.
func (result RecallResult) Shortened() RecallResult {
	items := make([]Recalled, len(result.Items))
	for position, item := range result.Items {
		item.Title = oneLine(item.Title, excerptLimit)
		items[position] = item
	}
	result.Items = items
	var problems []string
	for _, problem := range result.Problems[:min(replyProblems, len(result.Problems))] {
		problems = append(problems, oneLine(problem, excerptLimit))
	}
	if extra := len(result.Problems) - replyProblems; extra > 0 {
		problems = append(problems, fmt.Sprintf("and %d more; goblins memory recall --data <data folder> builds the index here and names them all", extra))
	}
	result.Problems = problems
	return result
}

// excerpt is a fact's description, or the opening of any other item's text,
// or of its title when it has none.
func excerpt(item Item) string {
	text := item.Text
	if item.Kind == KindFact && item.Title != "" || text == "" {
		text = item.Title
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= excerptLimit {
		return text
	}
	cut := excerptLimit
	for cut > 0 && !utf8Start(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}

func utf8Start(value byte) bool { return value&0xC0 != 0x80 }

// FormatDate is an item's date as recall shows it.
func FormatDate(date time.Time) string {
	if date.IsZero() {
		return "undated"
	}
	return date.Format(time.DateOnly)
}

// RenderRecall is the text a reader sees: one ranked line per item with its
// id, kind, date and source, then its excerpt.
func RenderRecall(query RecallQuery, result RecallResult, origin string) string {
	var output strings.Builder
	fmt.Fprintf(&output, "recall %q: %d of %d items, %.2f ms, %s\n", query.Question, len(result.Items), result.Indexed, float64(result.Elapsed.Microseconds())/1000, origin)
	for rank, item := range result.Items {
		fmt.Fprintf(&output, "%d. %s  %s  %s  %s:%d", rank+1, item.ID, item.Kind, FormatDate(item.Date), item.Path, item.Line)
		if item.IsSuperseded {
			output.WriteString("  superseded")
		}
		fmt.Fprintf(&output, "\n   %s\n", item.Excerpt)
	}
	if len(result.Items) == 0 {
		output.WriteString("No item matches; try other words, fewer filters or --history.\n")
	}
	if result.Hidden > 0 {
		fmt.Fprintf(&output, "%d superseded or archived matches hidden; add --history to see them.\n", result.Hidden)
	}
	for _, problem := range result.Problems {
		fmt.Fprintf(&output, "INDEX PROBLEM: %s\n", problem)
	}
	output.WriteString("Open a source at its line to read it in full; an excerpt is never the authority.\n")
	return output.String()
}
