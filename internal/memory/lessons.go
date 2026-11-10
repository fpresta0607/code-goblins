package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// MaxLessons is how many lessons one done report may carry.
const MaxLessons = 3

// Lesson decisions a candidate records.
const (
	LessonAccepted = "accepted"
	LessonRejected = "rejected"
)

const lessonsDir = "lessons"

// lessonLockName is the lock in the home's state that one decision on a
// lesson holds across its read, its check and its writes, the memory
// catalog's included, so two decisions at once take turns.
const lessonLockName = ".lessons.lock"

// lessonLockWait is how long a decision waits for another, which reads and
// writes a few memory files; it outlasts a paused machine. A test that waits
// out a lock nobody lets go shortens it.
var lessonLockWait = 30 * time.Second

// deciding serializes decisions within one process, which the named lock
// lets in again.
var deciding sync.Mutex

// Lesson is what a goblin learned on its task, with the status lines it
// stands on: each cite is a line's stamp or words that line holds.
type Lesson struct {
	Text  string
	Cites []string
}

// CitedLine is one status line a lesson stands on, as it was recorded.
type CitedLine struct {
	Stamp string `json:"stamp"`
	Line  string `json:"line"`
}

// LessonCandidate is a lesson proposed with a done report, held in the
// home's state until the CEO accepts it into memory or rejects it.
type LessonCandidate struct {
	ID         string      `json:"id"`
	Task       string      `json:"task"`
	Goblin     string      `json:"goblin,omitempty"`
	Project    string      `json:"project"`
	PR         string      `json:"pr,omitempty"`
	Text       string      `json:"text"`
	Cites      []CitedLine `json:"cites"`
	ProposedAt time.Time   `json:"proposed_at"`
	Decision   string      `json:"decision,omitempty"`
	DecidedAt  time.Time   `json:"decided_at,omitzero"`
	Reason     string      `json:"reason,omitempty"`
	Fact       string      `json:"fact,omitempty"`
}

// ProposedLesson is a lesson ready to record: its citations resolved
// against the task's status log.
type ProposedLesson struct {
	Text  string
	Cites []CitedLine
}

// ResolveLessons checks a done report's lessons against the task's own
// status lines and resolves each cite to the line it names: a stamp names
// its line, and words name the latest line that holds them.
func ResolveLessons(lessons []Lesson, statusLines []string) ([]ProposedLesson, error) {
	if len(lessons) > MaxLessons {
		return nil, fmt.Errorf("a done report carries at most %d lessons, not %d", MaxLessons, len(lessons))
	}
	resolved := make([]ProposedLesson, 0, len(lessons))
	for number, lesson := range lessons {
		text := strings.TrimSpace(lesson.Text)
		switch {
		case text == "":
			return nil, fmt.Errorf("lesson %d is empty", number+1)
		case strings.ContainsFunc(text, unicode.IsControl):
			// A lesson is printed at the drain, so it is one plain line.
			return nil, fmt.Errorf("lesson %d spans lines or holds a control character; a lesson is one plain line", number+1)
		case len(lesson.Cites) == 0:
			return nil, fmt.Errorf("lesson %d cites no status line; give --cites with the stamp of a line it stands on, or words that line holds", number+1)
		}
		proposed := ProposedLesson{Text: text}
		for _, cite := range lesson.Cites {
			line, err := citedLine(strings.TrimSpace(cite), statusLines)
			if err != nil {
				return nil, fmt.Errorf("lesson %d: %w", number+1, err)
			}
			if !slices.Contains(proposed.Cites, line) {
				proposed.Cites = append(proposed.Cites, line)
			}
		}
		resolved = append(resolved, proposed)
	}
	return resolved, nil
}

func citedLine(cite string, statusLines []string) (CitedLine, error) {
	if cite == "" {
		return CitedLine{}, errors.New("a cite is empty")
	}
	citedAt, stampErr := time.Parse(time.RFC3339, cite)
	isStamp := stampErr == nil
	for index := len(statusLines) - 1; index >= 0; index-- {
		recorded, event := state.SplitStatus(statusLines[index])
		if isStamp && recorded.Equal(citedAt) || !isStamp && strings.Contains(strings.ToLower(event), strings.ToLower(cite)) {
			stamp := ""
			if !recorded.IsZero() {
				stamp = recorded.Format(time.RFC3339)
			}
			return CitedLine{Stamp: stamp, Line: event}, nil
		}
	}
	if isStamp {
		return CitedLine{}, fmt.Errorf("no status line of this task is stamped %s", cite)
	}
	return CitedLine{}, fmt.Errorf("no status line of this task holds %q", cite)
}

// LessonID names a task's lesson by its words, so the same lesson proposed
// again is the same candidate.
func LessonID(task, text string) string {
	sum := sha256.Sum256([]byte(task + "\n" + text))
	return "lesson-" + hex.EncodeToString(sum[:])[:12]
}

func lessonPath(stateDir, id string) string {
	return filepath.Join(stateDir, lessonsDir, id+".json")
}

// ProposeLessons records each lesson as a candidate for the CEO. A lesson
// already proposed, or already decided, is left as it is.
func ProposeLessons(stateDir string, candidate LessonCandidate, lessons []ProposedLesson) ([]string, error) {
	var ids []string
	for _, lesson := range lessons {
		proposed := candidate
		proposed.ID, proposed.Text, proposed.Cites = LessonID(candidate.Task, lesson.Text), lesson.Text, lesson.Cites
		ids = append(ids, proposed.ID)
		path := lessonPath(stateDir, proposed.ID)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := writeCandidate(path, proposed); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func writeCandidate(path string, candidate LessonCandidate) error {
	encoded, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, append(encoded, '\n'))
}

// ReadLesson reads one candidate by its id.
func ReadLesson(stateDir, id string) (LessonCandidate, error) {
	if !strings.HasPrefix(id, "lesson-") || strings.ContainsAny(id, `/\.`) {
		return LessonCandidate{}, fmt.Errorf("%q is no lesson id; a lesson id reads lesson-<12 hex digits>", id)
	}
	content, err := fsx.ReadFile(lessonPath(stateDir, id))
	if errors.Is(err, fs.ErrNotExist) {
		return LessonCandidate{}, fmt.Errorf("no lesson %s was proposed", id)
	}
	if err != nil {
		return LessonCandidate{}, err
	}
	var candidate LessonCandidate
	if err := Decode(content, &candidate); err != nil {
		return LessonCandidate{}, fmt.Errorf("lesson %s: %w", id, err)
	}
	return candidate, nil
}

// PendingLessons is every candidate still waiting for the CEO's word, oldest
// first, and a problem for each candidate file that could not be read.
func PendingLessons(stateDir string) ([]LessonCandidate, []string, error) {
	paths, err := filepath.Glob(filepath.Join(stateDir, lessonsDir, "lesson-*.json"))
	if err != nil {
		return nil, nil, err
	}
	var pending []LessonCandidate
	var problems []string
	for _, path := range paths {
		candidate, err := ReadLesson(stateDir, strings.TrimSuffix(filepath.Base(path), ".json"))
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if candidate.Decision == "" {
			pending = append(pending, candidate)
		}
	}
	slices.SortFunc(pending, func(left, right LessonCandidate) int {
		if compared := left.ProposedAt.Compare(right.ProposedAt); compared != 0 {
			return compared
		}
		return strings.Compare(left.ID, right.ID)
	})
	return pending, problems, nil
}

// Acceptance is how the CEO takes a lesson into memory: as an incident,
// which fades with age, or as a rule, which never fades, and the facts it
// supersedes, which stay as they are and drop out of recall.
type Acceptance struct {
	IsIncident bool
	Supersedes []string
	Now        time.Time
}

type lessonMetadata struct {
	Type       string   `yaml:"type"`
	Projects   []string `yaml:"projects,flow"`
	Incident   bool     `yaml:"incident,omitempty"`
	Lesson     string   `yaml:"lesson"`
	Source     string   `yaml:"source"`
	Supersedes []string `yaml:"supersedes,flow,omitempty"`
	Modified   string   `yaml:"modified"`
}

type lessonFrontmatter struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Metadata    lessonMetadata `yaml:"metadata"`
}

// lockLessons takes the lessons' lock, waiting out another decision, and
// returns what gives it back.
func lockLessons(stateDir string) (func() error, error) {
	unlock, err := lockCatalog(stateDir, "lessons")
	if errors.Is(err, lock.ErrHeld) {
		return nil, fmt.Errorf("another decision on a lesson is still under way, so nothing was decided: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("the lessons' lock could not be taken, so nothing was decided: %w", err)
	}
	return unlock, nil
}

// AcceptLesson writes the lesson as a new fact file in the memory folder,
// scoped to its project, lists it in the startup catalog when the home has
// one, and records the decision, all under the lessons' lock. The cited
// status lines, a goblin's words, stand in a code block and the file ends
// with an aging marker for the day it was accepted, so nothing a goblin
// wrote is read as the fact's tier. A fact it supersedes stays as it is.
func AcceptLesson(dataDir, stateDir, id string, acceptance Acceptance) (name string, err error) {
	unlock, err := lockLessons(stateDir)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	candidate, err := undecided(stateDir, id)
	if err != nil {
		return "", err
	}
	catalogBefore, err := ReadData(dataDir, CatalogPath)
	isCataloged := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if isCataloged {
		if _, err := Load(dataDir, acceptance.Now); err != nil {
			return "", fmt.Errorf("the memory catalog does not load, so no lesson joins it until it does: %w", err)
		}
	}
	if len(acceptance.Supersedes) > 0 {
		index, err := BuildIndex(dataDir, stateDir, acceptance.Now)
		if err != nil {
			return "", fmt.Errorf("the facts it supersedes cannot be checked: %w", err)
		}
		for _, superseded := range acceptance.Supersedes {
			if !slices.ContainsFunc(index.Items, func(item Item) bool { return item.Kind == KindFact && item.ID == superseded }) {
				return "", fmt.Errorf("--supersedes %s names no memory fact; a lesson supersedes facts, never a ruling", superseded)
			}
		}
	}
	kind := "feedback"
	if acceptance.IsIncident {
		kind = "project"
	}
	source := "done report of " + candidate.Task
	if candidate.PR != "" {
		source += ", " + candidate.PR
	}
	directory := filepath.Join(dataDir, "memory")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	base := lessonSlug(candidate.Text)
	for attempt := 1; ; attempt++ {
		name = base
		if attempt > 1 {
			name = fmt.Sprintf("%s-%d", base, attempt)
		}
		header, err := yaml.Marshal(lessonFrontmatter{Name: name, Description: candidate.Text, Metadata: lessonMetadata{
			Type: kind, Projects: []string{candidate.Project}, Incident: acceptance.IsIncident, Lesson: candidate.ID,
			Source: source, Supersedes: acceptance.Supersedes, Modified: acceptance.Now.UTC().Format(time.RFC3339),
		}})
		if err != nil {
			return "", err
		}
		var body strings.Builder
		fmt.Fprintf(&body, "---\n%s---\n\n%s\n\nCited status lines of %s:\n\n```\n", header, candidate.Text, candidate.Task)
		for _, cite := range candidate.Cites {
			fmt.Fprintf(&body, "- %s %s\n", cite.Stamp, cite.Line)
		}
		fmt.Fprintf(&body, "```\n\n<!--a:%s-->\n", acceptance.Now.UTC().Format(time.DateOnly))
		path := filepath.Join(directory, name+".md")
		file, err := fsx.CreateNew(path, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, writeErr := file.WriteString(body.String())
		if closeErr := file.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			return "", writeErr
		}
		if isCataloged {
			if err := catalogLesson(dataDir, catalogBefore, name, candidate, []byte(body.String()), acceptance.Now); err != nil {
				return "", errors.Join(fmt.Errorf("the lesson was not accepted, since its fact would leave the memory catalog unreadable: %w", err), os.Remove(path))
			}
		}
		candidate.Decision, candidate.DecidedAt, candidate.Fact = LessonAccepted, acceptance.Now.UTC(), name
		if err := writeCandidate(lessonPath(stateDir, id), candidate); err != nil {
			return "", fmt.Errorf("fact %s was written, but the lesson's decision could not be recorded, so it still waits at the drain: %w", name, err)
		}
		return name, nil
	}
}

// catalogLesson lists a lesson's new fact in the startup catalog, which must
// cover every memory file, as one cold fact routed to its repository and
// triggered by the lesson's words, so a CEO session's startup memory leaves
// it out and a retrieval for that repository carries it. A catalog that
// would not load is put back.
func catalogLesson(dataDir string, before []byte, name string, candidate LessonCandidate, content []byte, now time.Time) error {
	var catalog Catalog
	if err := Decode(before, &catalog); err != nil {
		return err
	}
	catalog.Sources = append(catalog.Sources, Source{Path: "memory/" + name + ".md", SHA256: Hash(content), Entries: []Entry{{
		ID: candidate.ID, End: len(content), SHA256: Hash(content), Authority: "fact", Location: "cold", Trigger: candidate.Text, Projects: []string{candidate.Project},
	}}})
	return replaceCatalog(dataDir, catalog, before, now)
}

// RejectLesson records the CEO's rejection and its reason, under the
// lessons' lock; nothing reaches memory.
func RejectLesson(stateDir, id, reason string, now time.Time) (err error) {
	unlock, err := lockLessons(stateDir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	candidate, err := undecided(stateDir, id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("a rejection says why")
	}
	candidate.Decision, candidate.DecidedAt, candidate.Reason = LessonRejected, now.UTC(), strings.TrimSpace(reason)
	return writeCandidate(lessonPath(stateDir, id), candidate)
}

func undecided(stateDir, id string) (LessonCandidate, error) {
	candidate, err := ReadLesson(stateDir, id)
	if err != nil {
		return LessonCandidate{}, err
	}
	if candidate.Decision != "" {
		return LessonCandidate{}, fmt.Errorf("lesson %s was already %s on %s", id, candidate.Decision, FormatDate(candidate.DecidedAt))
	}
	return candidate, nil
}

// lessonSlug names a lesson's fact file by its first words.
func lessonSlug(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return r > unicode.MaxASCII || !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	kept, length := []string{}, 0
	for _, word := range words {
		if length+len(word) > 60 {
			break
		}
		kept = append(kept, word)
		length += len(word) + 1
	}
	// A name cut short ends on a word that carries meaning, never on "of".
	for len(kept) > 1 && len(kept) < len(words) && stopWords[kept[len(kept)-1]] {
		kept = kept[:len(kept)-1]
	}
	if len(kept) == 0 {
		return "lesson"
	}
	return strings.Join(kept, "-")
}
