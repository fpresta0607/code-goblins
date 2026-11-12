// Package goblinname gives every goblin a fun first name and title, such as
// Jerry the Code Designer, which the board, the merge train and the CEO call
// it by. The task id stays the handle for goblins commands.
//
// A name comes from a pool of 452 and keeps clear of every live goblin and
// queued task and of the last RecentWindow, 300, spawns: more than a week of
// History on a busy fleet, so History never shows one name on two recent
// tasks.
//
// A title is the one the brief gives on a "goblin title:" line, or else a
// subject of the task, in the task's own words, with a role, such as
// Estimate Elf. The subject is the word the task's title leans on most,
// passing over one another live or queued goblin's title already names
// while another fits.
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
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Pair is a goblin's first name and title.
type Pair struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// String is the pair as the board and the CEO read it: "Jerry - Code
// Designer".
func (p Pair) String() string {
	return p.Name + " - " + p.Title
}

// RecentWindow is how many of the latest pairs a new one keeps clear of, so
// the Overlord meets a new goblin each spawn rather than one he just saw.
const RecentWindow = 300

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

// Called is how the CEO's own views name a goblin: "Jerry (cg-x)", or the id
// alone for a goblin with no name.
func Called(name, id string) string {
	if name == "" {
		return id
	}
	return name + " (" + id + ")"
}

// Work is what a goblin's title is fitted to: its task's id and title, the
// Task section of its brief, which says what the work is without the
// constraints that name other goblins' work, and the title its brief gives,
// if any.
type Work struct {
	ID         string
	Title      string
	Task       string
	GivenTitle string
}

// WorkOf is the work of task id, titled title, that brief describes. A
// goblin title line GivenTitle refuses gives no title, and goblins brief ready
// refuses the brief.
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
	givenTitle, _ := GivenTitle(brief)
	return Work{ID: id, Title: title, Task: strings.TrimSpace(strings.Join(task, "")), GivenTitle: givenTitle}
}

// givenTitleKey names the line in a brief's Delivery section that gives its
// goblin's title.
const givenTitleKey = "goblin title"

// GivenTitle is the title brief gives its goblin on a "goblin title:" line
// in its Delivery section, empty when it gives none. A title of more than
// three words or maxTitle characters, or a second line, is refused.
func GivenTitle(brief string) (string, error) {
	section, title, isStated := "", "", false
	for line := range strings.Lines(brief) {
		if heading, isHeading := strings.CutPrefix(line, "## "); isHeading {
			section = strings.TrimSpace(heading)
			continue
		}
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if section != "Delivery" || !found || !strings.EqualFold(strings.TrimSpace(key), givenTitleKey) {
			continue
		}
		if isStated {
			return "", errors.New("the brief's Delivery section has more than one " + givenTitleKey + " line")
		}
		isStated = true
		title = strings.Join(strings.Fields(value), " ")
		if words := len(strings.Fields(title)); words == 0 || words > 3 || utf8.RuneCountInString(title) > maxTitle || strings.ContainsAny(title, ";\u2014") {
			return "", fmt.Errorf("the brief's %s line says %q: give one to three words of at most %d characters, with no semicolon or em dash, or no %s line to fit the title to the task", givenTitleKey, strings.TrimSpace(value), maxTitle, givenTitleKey)
		}
	}
	return title, nil
}

// given is pair with the title work's brief gives, when it gives one, so a
// title the brief's author names replaces one fitted before the brief named
// it, and the goblin keeps its name.
func given(pair Pair, work Work) Pair {
	if work.GivenTitle != "" {
		pair.Title = work.GivenTitle
	}
	return pair
}

// Assign gives a goblin starting now a pair: the one its task was queued
// with, or else a name no live goblin or queued task holds and, while any is
// free, none of the last RecentWindow spawns used, with the title its brief
// gives or one naming a subject of work, or a generic one when work names
// none. It records the pair among the recent ones.
func Assign(stateDir string, work Work) (pair Pair, err error) {
	err = withLock(stateDir, func(names *held) error {
		var found bool
		if pair, found = names.queued[work.ID]; found {
			pair = given(pair, work)
		} else {
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
				kept[work.ID] = given(pair, work)
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
			// A brief no longer there leaves the title to fit the task's
			// title and id alone.
			brief, _ := fsx.ReadFile(meta.Brief)
			work := WorkOf(meta.ID, meta.Title, string(brief))
			pair, found := names.queued[meta.ID]
			if found {
				pair = given(pair, work)
			} else {
				var err error
				if pair, err = pick(work, names.all(), names.recent); err != nil {
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
	return Pair{Name: names[rand.IntN(len(names))], Title: titleFor(work, held, recent)}, nil
}

// titleFor is the title work's brief gives, or else a title naming work's
// best subject no held title names, or its best when held titles name them
// all, with the first role no held or recent title has. Work that names no
// subject gets a generic title.
func titleFor(work Work, held, recent []Pair) string {
	if work.GivenTitle != "" {
		return work.GivenTitle
	}
	titlesSeen := used(slices.Concat(held, recent), func(p Pair) string { return p.Title })
	subjects := subjectsOf(work)
	if len(subjects) == 0 {
		titles := firstOf(fresh(genericTitles, titlesSeen), genericTitles)
		return titles[rand.IntN(len(titles))]
	}
	heldSubjects := map[string]bool{}
	for _, pair := range held {
		if fields := strings.Fields(pair.Title); len(fields) > 1 {
			heldSubjects[keyOf(strings.Join(fields[:len(fields)-1], " "))] = true
		}
	}
	chosen := subjects[0]
	for _, subject := range subjects {
		if !heldSubjects[keyOf(subject.word)] {
			chosen = subject
			break
		}
	}
	titles := chosen.titles()
	return firstOf(fresh(titles, titlesSeen), titles)[0]
}

// Weights of a word by where work says it. The title says what the work is,
// and the last word of a phrase in it is the thing the phrase is about, so
// "cost estimate" is about an estimate. A label before a title's colon, such
// as "Board:", names the area, as the id does, and words in brackets are an
// aside. The Task section is read only when neither the title nor the id
// names anything.
const (
	titleWeight = 2
	headWeight  = 1
	areaWeight  = 1
	// labelWords is the most words a label before a title's colon holds.
	labelWords = 3
	// longestWord is the longest word of the task's own a title takes, so
	// every subject leaves room for a choice of roles.
	longestWord = 12
)

// subjectsOf are the subjects work names, best first: those it weighs most,
// and of those the one it names first.
func subjectsOf(work Work) []subject {
	named := namedSubjects{project: projectOf(work.ID)}
	title := work.Title
	if label, rest, found := strings.Cut(title, ":"); found && len(strings.Fields(label)) <= labelWords {
		named.read(label, areaWeight, false)
		title = rest
	}
	named.read(title, titleWeight, true)
	named.read(work.ID, areaWeight, false)
	if len(named.subjects) == 0 {
		named.read(work.Task, titleWeight, true)
	}
	order := make([]int, len(named.subjects))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return named.weights[b] - named.weights[a] })
	subjects := make([]subject, len(order))
	for i, at := range order {
		subjects[i] = named.subjects[at]
	}
	return subjects
}

// namedSubjects are the subjects a work names, in the order it first names
// them, with the weight each has so far, never one that names its project.
type namedSubjects struct {
	subjects []subject
	weights  []int
	project  map[string]bool
}

// projectOf are the words a task id names its project by, by the fleet's
// convention: its first word, as in cg-board or acme-report, or every word of
// a root issue's id, root-<project>-<number>. A title names the work, never
// the project every task there shares.
func projectOf(id string) map[string]bool {
	project := map[string]bool{}
	words := phraseWords(id)
	if len(words) < 2 {
		return project
	}
	if words[0].word != "root" {
		words = words[:1]
	}
	for _, word := range words {
		project[keyOf(word.word)] = true
	}
	return project
}

// read adds weight for each subject text names, each time it names it, one
// more for each word of its name past the first, and headWeight more where
// its name ends a phrase when heads count; a name in brackets adds
// areaWeight alone. At each word it takes the longest catalog name that
// starts there, so a family tree counts once, or else the word itself when
// it is the task's own: neither ordinary nor longer than longestWord, nor
// holding a digit.
func (named *namedSubjects) read(text string, weight int, isHeadCounted bool) {
	words := phraseWords(text)
	for at := 0; at < len(words); {
		found, length := catalogSubject(words, at)
		if found == nil {
			if !isOwn(words[at].word) {
				at++
				continue
			}
			found, length = &subject{word: display(words[at].written)}, 1
		}
		// A name of more words, such as merge train, is the more specific.
		last := at + length - 1
		added := weight + length - 1
		if words[at].isAside {
			added = areaWeight
		} else if isHeadCounted && (words[last].isPhraseEnd || last+1 == len(words) || isOrdinary(words[last+1].word)) {
			added += headWeight
		}
		named.add(*found, added)
		at += length
	}
}

func (named *namedSubjects) add(found subject, weight int) {
	if named.project[keyOf(found.word)] {
		return
	}
	at := slices.IndexFunc(named.subjects, func(known subject) bool { return keyOf(known.word) == keyOf(found.word) })
	if at < 0 {
		named.subjects, named.weights = append(named.subjects, found), append(named.weights, 0)
		at = len(named.subjects) - 1
	}
	named.weights[at] += weight
}

// catalogSubject is the catalog subject whose longest name starts at word
// at of words, with that name's length in words, or nil.
func catalogSubject(words []phraseWord, at int) (*subject, int) {
	var best *subject
	length := 0
	for i := range subjects {
		for _, name := range subjects[i].names {
			parts := strings.Fields(name)
			if len(parts) <= length || at+len(parts) > len(words) {
				continue
			}
			if slices.EqualFunc(words[at:at+len(parts)], parts, func(word phraseWord, part string) bool { return forms(word.word, part) }) {
				best, length = &subjects[i], len(parts)
			}
		}
	}
	return best, length
}

// phraseWord is a word of a task's text: as it is written, in lower case,
// whether punctuation after it ends its phrase, and whether it stands in
// brackets, an aside.
type phraseWord struct {
	written     string
	word        string
	isPhraseEnd bool
	isAside     bool
}

// phraseWords are text's words, an apostrophe dropped so "What's" reads as
// "Whats", and a space, hyphen, slash or underscore parting two in one
// phrase, so "no-mistakes" reads as "no mistakes". Any other mark ends a
// phrase.
func phraseWords(text string) []phraseWord {
	text = strings.NewReplacer("'", "", "’", "").Replace(text)
	var words []phraseWord
	var current strings.Builder
	depth := 0
	part := func(isPhraseEnd bool) {
		if current.Len() > 0 {
			words = append(words, phraseWord{written: current.String(), word: strings.ToLower(current.String()), isAside: depth > 0})
			current.Reset()
		}
		if isPhraseEnd && len(words) > 0 {
			words[len(words)-1].isPhraseEnd = true
		}
	}
	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			current.WriteRune(r)
		case r == ' ' || r == '\t' || r == '-' || r == '/' || r == '_':
			part(false)
		case r == '(' || r == '[':
			part(true)
			depth++
		case r == ')' || r == ']':
			part(true)
			depth = max(0, depth-1)
		default:
			part(true)
		}
	}
	part(true)
	return words
}

// isOwn reports whether word can be a subject of its own.
func isOwn(word string) bool {
	length := utf8.RuneCountInString(word)
	return length >= 3 && length <= longestWord && !strings.ContainsFunc(word, unicode.IsDigit) && !isOrdinary(word)
}

func isOrdinary(word string) bool {
	return slices.ContainsFunc(ordinaryWords, func(ordinary string) bool { return forms(word, ordinary) })
}

// display is word as a title prints it: singular, with a capital first
// letter when the task wrote it all in lower case, and as the task wrote it
// otherwise, so "PDF" and "iOS" keep their own capitals.
func display(word string) string {
	word = singular(word)
	if word != strings.ToLower(word) {
		return word
	}
	first, size := utf8.DecodeRuneInString(word)
	return string(unicode.ToUpper(first)) + word[size:]
}

// keyOf is what two subjects compare by: their words in lower case and
// singular, so "Branches", "branch" and "Branch" are one subject.
func keyOf(word string) string {
	fields := strings.Fields(strings.ToLower(word))
	for i, field := range fields {
		fields[i] = singular(field)
	}
	return strings.Join(fields, " ")
}

// singular is word without a plural ending: ies for a y, es after ch, sh, ss,
// x, zz, tus or ias, and a final s but after ss, us, is or ias. A capital final
// S, as in macOS, and "series" stay as written.
func singular(word string) string {
	lower := strings.ToLower(word)
	hasEnding := func(endings ...string) bool {
		return slices.ContainsFunc(endings, func(ending string) bool { return strings.HasSuffix(lower, ending) })
	}
	switch {
	case strings.HasSuffix(word, "S") || lower == "series":
		return word
	case len(lower) > 4 && hasEnding("ies"):
		return word[:len(word)-3] + "y"
	case hasEnding("ches", "shes", "sses", "xes", "zzes", "tuses", "iases"):
		return word[:len(word)-2]
	case len(lower) > 3 && hasEnding("s") && !hasEnding("ss", "us", "is", "ias"):
		return word[:len(word)-1]
	}
	return word
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
// than maxTitle and none whose role starts with the word, such as Guard
// Guardian.
func (s subject) titles() []string {
	var titles []string
	for _, role := range slices.Concat(s.roles, alliterative[unicode.ToLower([]rune(s.word)[0])], roles) {
		isEcho := strings.HasPrefix(strings.ToLower(role), strings.ToLower(s.word))
		if title := s.word + " " + role; !isEcho && utf8.RuneCountInString(title) <= maxTitle && !slices.Contains(titles, title) {
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
