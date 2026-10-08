// Package goblinname gives every goblin a fun first name and title, such as
// Jerry the Code Designer, which the board, the merge train and the CFO call
// it by. The task id stays the handle for cfo commands.
package goblinname

import (
	"encoding/json"
	"errors"
	"fmt"
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

// recentFile keeps the latest pairs, oldest first, in the state directory.
const recentFile = ".goblin-names.json"

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

// Hint is what a goblin's title is fitted to: its task's title, its id and
// the Task section of its brief, which says what the work is without the
// constraints that name other goblins' work.
func Hint(title, id, brief string) string {
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
	return strings.Join(slices.DeleteFunc([]string{title, id, strings.TrimSpace(strings.Join(task, ""))}, func(part string) bool { return part == "" }), " ")
}

// Assign gives a goblin starting now a pair: a name no live goblin holds
// and, while any is free, none of the last RecentWindow spawns used, with a
// title fitting the work hint names, or a generic one, kept clear of the
// titles live goblins hold and recent spawns used. It records the pair among
// the recent ones.
func Assign(stateDir, hint string) (pair Pair, err error) {
	err = withLock(stateDir, func(live, recent []Pair) ([]Pair, error) {
		var pickErr error
		pair, pickErr = pick(hint, live, recent)
		if pickErr != nil {
			return nil, pickErr
		}
		return []Pair{pair}, nil
	})
	return pair, err
}

// Backfill gives every live goblin whose record has no name a pair, so a
// goblin started before names existed is named like the rest. A record
// another command is writing is left for the next pass.
func Backfill(stateDir string) error {
	metas, err := unnamed(stateDir)
	if err != nil || len(metas) == 0 {
		return err
	}
	return withLock(stateDir, func(live, recent []Pair) ([]Pair, error) {
		var added []Pair
		var errs []error
		for _, meta := range metas {
			// A brief no longer there leaves the title to fit the task's
			// title and id alone.
			brief, _ := fsx.ReadFile(meta.Brief)
			pair, err := pick(Hint(meta.Title, meta.ID, string(brief)), slices.Concat(live, added), slices.Concat(recent, added))
			if err != nil {
				return added, errors.Join(append(errs, err)...)
			}
			named, err := name(stateDir, meta.ID, pair)
			if err != nil {
				errs = append(errs, err)
			} else if named {
				added = append(added, pair)
			}
		}
		return added, errors.Join(errs...)
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

// withLock hands choose the pairs live goblins hold and the recent ones
// under the pick lock, and keeps the pairs it gave among the last
// RecentWindow.
func withLock(stateDir string, choose func(live, recent []Pair) ([]Pair, error)) error {
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
	recent, err := readRecent(stateDir)
	if err != nil {
		return err
	}
	added, err := choose(live, recent)
	if len(added) == 0 {
		return err
	}
	recent = append(recent, added...)
	return errors.Join(err, writeRecent(stateDir, recent[max(0, len(recent)-RecentWindow):]))
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
	data, err := fsx.ReadFile(filepath.Join(stateDir, recentFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var recent []Pair
	if err := json.Unmarshal(data, &recent); err != nil {
		return nil, fmt.Errorf("goblin names: %s cannot be read: %w", recentFile, err)
	}
	return recent, nil
}

func writeRecent(stateDir string, recent []Pair) error {
	data, err := json.Marshal(recent)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, recentFile), data)
}

// pick chooses a pair for work hint names, clear of the live and recent
// pairs.
func pick(hint string, live, recent []Pair) (Pair, error) {
	held := used(live, func(p Pair) string { return p.Name })
	seen := used(slices.Concat(live, recent), func(p Pair) string { return p.Name })
	names := firstOf(fresh(firstNames, seen), fresh(firstNames, held))
	if len(names) == 0 {
		return Pair{}, errors.New("goblin names: every name is held by a live goblin")
	}
	titlesSeen := used(slices.Concat(live, recent), func(p Pair) string { return p.Title })
	titles := firstOf(fresh(themed(hint), titlesSeen), fresh(genericTitles, titlesSeen), genericTitles)
	return Pair{Name: names[rand.IntN(len(names))], Title: titles[rand.IntN(len(titles))]}, nil
}

// themed is the titles of the theme hint names most often, nil when it
// names none.
func themed(hint string) []string {
	words := strings.FieldsFunc(strings.ToLower(hint), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var best []string
	bestHits := 0
	for _, theme := range themes {
		hits := 0
		for _, word := range words {
			if slices.Contains(theme.words, word) {
				hits++
			}
		}
		if hits > bestHits {
			best, bestHits = theme.titles, hits
		}
	}
	return best
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
