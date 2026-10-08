// Package goblinname gives every goblin a fun first name and title, such as
// Jerry the Code Designer, which the board, the merge train and the CFO call
// it by. The task id stays the handle for cfo commands.
package goblinname

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Pair is a goblin's first name and title.
type Pair struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// String is the pair as the board and the CFO read it: "Jerry - Code
// Designer".
func (p Pair) String() string {
	return p.Name + " - " + p.Title
}

// RecentWindow is how many of the latest pairs a new one keeps clear of, so
// the Overlord meets a new goblin each spawn rather than one he just saw.
const RecentWindow = 20

// maxTitle is the longest title, in characters, so a title stays short.
const maxTitle = 20

// recentFile keeps the latest pairs, oldest first, in the state directory.
const recentFile = ".goblin-names.json"

// queuedFile keeps the pair of each queued task by its id, so a task keeps
// the goblin it was queued with into its spawn.
const queuedFile = ".goblin-names-queued.json"

// lockName serialises every pick, so two goblins starting at once can never
// both take a name.
const lockName = ".goblin-names.lock"

// lockBudget is how long a pick waits for another, which only reads the
// fleet's records and writes one file.
const lockBudget = 10 * time.Second

// picking serialises picks within one process, which the named lock lets
// re-enter.
var picking sync.Mutex

// Called is how the CFO's own views name a goblin: "Jerry (cg-x)", or the id
// alone for a goblin with no name.
func Called(name, id string) string {
	if name == "" {
		return id
	}
	return name + " (" + id + ")"
}

// Work is what a goblin's title is fitted to: its task's id and title, and
// the Task section of its brief, which says what the work is without the
// constraints that name other goblins' work.
type Work struct {
	ID    string
	Title string
	Task  string
}

// WorkOf is the work of task id, titled title, that brief describes.
func WorkOf(id, title, brief string) Work {
	var task []string
	inTask := false
	for line := range strings.Lines(brief) {
		heading, isHeading := strings.CutPrefix(line, "## ")
		if isHeading {
			inTask = strings.TrimSpace(heading) == "Task"
			continue
		}
		if inTask {
			task = append(task, line)
		}
	}
	return Work{ID: id, Title: title, Task: strings.TrimSpace(strings.Join(task, ""))}
}

// Assign gives a goblin starting now a pair: the one its task was queued
// with, or else a name no live goblin or queued task holds and, while any is
// free, none of the last RecentWindow spawns used, with a title naming the
// subject of work, or a generic one when work names none. It records the
// pair among the recent ones.
func Assign(stateDir string, work Work) (pair Pair, err error) {
	err = withLock(stateDir, func(names *held) error {
		var found bool
		if pair, found = names.queued[work.ID]; !found {
			var pickErr error
			if pair, pickErr = pick(work, names.all(), names.recent); pickErr != nil {
				return pickErr
			}
		}
		names.recent = append(names.recent, pair)
		return nil
	})
	return pair, err
}

// Reserve gives each queued task without a pair one, by the rules Assign
// follows, and forgets the pair of any task no longer among queued, so the
// card of a queued task names its goblin before it starts. It returns the
// pair of each queued task it could name.
func Reserve(stateDir string, queued []Work) (pairs map[string]Pair, err error) {
	err = withLock(stateDir, func(names *held) error {
		kept := map[string]Pair{}
		for _, work := range queued {
			if pair, found := names.queued[work.ID]; found {
				kept[work.ID] = pair
			}
		}
		names.queued = kept
		defer func() { pairs = maps.Clone(names.queued) }()
		for _, work := range queued {
			if _, found := names.queued[work.ID]; found {
				continue
			}
			pair, err := pick(work, names.all(), names.recent)
			if err != nil {
				return err
			}
			names.queued[work.ID] = pair
		}
		return nil
	})
	return pairs, err
}

// QueuedPath is the file that keeps the queued tasks' pairs.
func QueuedPath(stateDir string) string {
	return filepath.Join(stateDir, queuedFile)
}

// ReadQueued reads the queued tasks' pairs by task id.
func ReadQueued(stateDir string) (map[string]Pair, error) {
	queued := map[string]Pair{}
	if err := readJSON(QueuedPath(stateDir), &queued); err != nil {
		return nil, err
	}
	return queued, nil
}

// Backfill gives every live goblin whose record has no name a pair, the one
// its task was queued with where it has one, so a goblin started before
// names existed is named like the rest. A record another command is writing
// is left for the next pass.
func Backfill(stateDir string) error {
	metas, err := unnamed(stateDir)
	if err != nil || len(metas) == 0 {
		return err
	}
	return withLock(stateDir, func(names *held) error {
		var errs []error
		for _, meta := range metas {
			pair, found := names.queued[meta.ID]
			if !found {
				// A brief no longer there leaves the title to fit the
				// task's title and id alone.
				brief, _ := fsx.ReadFile(meta.Brief)
				var err error
				if pair, err = pick(WorkOf(meta.ID, meta.Title, string(brief)), names.all(), names.recent); err != nil {
					return errors.Join(append(errs, err)...)
				}
			}
			named, err := name(stateDir, meta.ID, pair)
			if err != nil {
				errs = append(errs, err)
			} else if named {
				names.live = append(names.live, pair)
				names.recent = append(names.recent, pair)
			}
		}
		return errors.Join(errs...)
	})
}

// unnamed reads the records of the live goblins that hold no name.
func unnamed(stateDir string) ([]state.TaskMeta, error) {
	scan, err := state.ScanIDs(stateDir)
	if err != nil {
		return nil, err
	}
	var metas []state.TaskMeta
	for _, id := range scan.MetaIDs {
		if meta, err := state.ReadTaskMeta(stateDir, id); err == nil && meta.GoblinName == "" {
			metas = append(metas, meta)
		}
	}
	return metas, nil
}

// name writes pair into id's record, under the record's own lock, unless the
// record is gone, already named, or held by another writer.
func name(stateDir, id string, pair Pair) (named bool, err error) {
	metadataLock := state.MetadataLockName(id)
	if _, err := lock.AcquireExclusiveNamed(stateDir, metadataLock); err != nil {
		if errors.Is(err, lock.ErrHeld) {
			return false, nil
		}
		return false, err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(stateDir, metadataLock)) }()
	path := state.TaskMetaPath(stateDir, id)
	record, err := state.ReadMeta(path)
	if errors.Is(err, os.ErrNotExist) || err == nil && record["goblin_name"] != "" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	record["goblin_name"], record["goblin_title"] = pair.Name, pair.Title
	return true, state.WriteMeta(path, record)
}

// held is what a pick keeps clear of: the pairs live goblins hold, those
// queued tasks hold by task id, and the last RecentWindow spawns.
type held struct {
	live   []Pair
	queued map[string]Pair
	recent []Pair
}

// all is every pair a live goblin or a queued task holds.
func (names *held) all() []Pair {
	return slices.Concat(names.live, slices.Collect(maps.Values(names.queued)))
}

// withLock hands change the pairs held now under the pick lock, and keeps
// what change makes of the queued pairs and the recent ones, the last
// RecentWindow of those.
func withLock(stateDir string, change func(*held) error) error {
	picking.Lock()
	defer picking.Unlock()
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	if _, err := lock.AcquireNamedOwnerWithin(stateDir, lockName, os.Getpid(), "goblin-names", lockBudget); err != nil {
		return fmt.Errorf("goblin names: %w", err)
	}
	defer lock.ReleaseNamed(stateDir, lockName)
	live, err := livePairs(stateDir)
	if err != nil {
		return err
	}
	queued, err := ReadQueued(stateDir)
	if err != nil {
		return err
	}
	recent, err := readRecent(stateDir)
	if err != nil {
		return err
	}
	names := held{live: live, queued: maps.Clone(queued), recent: slices.Clone(recent)}
	errs := []error{change(&names)}
	if !maps.Equal(names.queued, queued) {
		errs = append(errs, writeJSON(QueuedPath(stateDir), names.queued))
	}
	if !slices.Equal(names.recent, recent) {
		errs = append(errs, writeJSON(filepath.Join(stateDir, recentFile), names.recent[max(0, len(names.recent)-RecentWindow):]))
	}
	return errors.Join(errs...)
}

// livePairs reads the pair of every live goblin that has one.
func livePairs(stateDir string) ([]Pair, error) {
	scan, err := state.ScanIDs(stateDir)
	if err != nil {
		return nil, err
	}
	var live []Pair
	for _, id := range scan.MetaIDs {
		if meta, err := state.ReadTaskMeta(stateDir, id); err == nil && meta.GoblinName != "" {
			live = append(live, Pair{Name: meta.GoblinName, Title: meta.GoblinTitle})
		}
	}
	return live, nil
}

func readRecent(stateDir string) ([]Pair, error) {
	var recent []Pair
	err := readJSON(filepath.Join(stateDir, recentFile), &recent)
	return recent, err
}

// readJSON reads path into value, leaving value as it is when there is no
// file.
func readJSON(path string, value any) error {
	data, err := fsx.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("goblin names: %s cannot be read: %w", filepath.Base(path), err)
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, data)
}

// pick chooses a pair for work, clear of the held and recent pairs.
func pick(work Work, held, recent []Pair) (Pair, error) {
	heldNames := used(held, func(p Pair) string { return p.Name })
	seen := used(slices.Concat(held, recent), func(p Pair) string { return p.Name })
	names := firstOf(fresh(firstNames, seen), fresh(firstNames, heldNames))
	if len(names) == 0 {
		return Pair{}, errors.New("goblin names: every name is held by a live goblin or a queued task")
	}
	titlesSeen := used(slices.Concat(held, recent), func(p Pair) string { return p.Title })
	var title string
	if subject := subjectOf(work); subject != nil {
		// The subject's best title not in use, else its best.
		titles := subject.titles()
		title = firstOf(fresh(titles, titlesSeen), titles)[0]
	} else {
		titles := firstOf(fresh(genericTitles, titlesSeen), genericTitles)
		title = titles[rand.IntN(len(titles))]
	}
	return Pair{Name: names[rand.IntN(len(names))], Title: title}, nil
}

// subjectOf is the subject work names most often, nil when it names none.
// Its id and title say what the work is, so the id's words count twice and
// the Task section is read only when neither names a subject.
func subjectOf(work Work) *subject {
	counts := make([]int, len(subjects))
	count(words(work.ID), 2, counts)
	count(words(work.Title), 1, counts)
	if slices.Max(counts) == 0 {
		count(words(work.Task), 1, counts)
	}
	best := 0
	for i, n := range counts {
		if n > counts[best] {
			best = i
		}
	}
	if counts[best] == 0 {
		return nil
	}
	return &subjects[best]
}

// count adds weight to the count of each subject text names, each time it
// names it, reading text word by word and taking the longest name that
// starts at each, so a family tree counts once.
func count(text []string, weight int, counts []int) {
	for at := 0; at < len(text); {
		best, length := -1, 0
		for i, subject := range subjects {
			for _, name := range subject.names {
				parts := strings.Fields(name)
				if len(parts) > length && at+len(parts) <= len(text) && slices.EqualFunc(text[at:at+len(parts)], parts, forms) {
					best, length = i, len(parts)
				}
			}
		}
		if best < 0 {
			at++
			continue
		}
		counts[best] += weight
		at += length
	}
}

// words are text's words in lower case, an apostrophe dropped so "What's"
// reads as "whats", and a hyphen parting two so "no-mistakes" reads as "no
// mistakes".
func words(text string) []string {
	text = strings.NewReplacer("'", "", "\u2019", "").Replace(strings.ToLower(text))
	return strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// forms reports whether word is name or name with an s, es, d, ed or ing
// ending, or ies for a final y, so "branches", "stalled", "queued" and
// "resuming" name a branch, a stall, a queue and a resume.
func forms(word, name string) bool {
	if word == name {
		return true
	}
	for _, ending := range []string{"s", "es", "d", "ed", "ing"} {
		if stem, found := strings.CutSuffix(word, ending); found && (stem == name || ending == "ing" && stem+"e" == name) {
			return true
		}
	}
	stem, found := strings.CutSuffix(word, "ies")
	return found && stem+"y" == name
}

// titles are a subject's titles, best first: its word with its own roles,
// then the roles that share its first letter, then any role, each no longer
// than maxTitle.
func (s subject) titles() []string {
	var titles []string
	for _, role := range slices.Concat(s.roles, alliterative[unicode.ToLower([]rune(s.word)[0])], roles) {
		if title := s.word + " " + role; len(title) <= maxTitle && !slices.Contains(titles, title) {
			titles = append(titles, title)
		}
	}
	return titles
}

func used(pairs []Pair, part func(Pair) string) map[string]bool {
	set := map[string]bool{}
	for _, pair := range pairs {
		set[strings.ToLower(part(pair))] = true
	}
	return set
}

func fresh(candidates []string, seen map[string]bool) []string {
	return slices.DeleteFunc(slices.Clone(candidates), func(candidate string) bool { return seen[strings.ToLower(candidate)] })
}

func firstOf(lists ...[]string) []string {
	for _, list := range lists {
		if len(list) > 0 {
			return list
		}
	}
	return nil
}
